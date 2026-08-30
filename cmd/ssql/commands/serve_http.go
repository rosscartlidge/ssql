package commands

// serve Phase 2a (DFC079 rev 2 / DFC108): the HTTP protocol.
//
// `ssql serve -listen-http ADDR [-dir DIR]` exposes stateless
// per-request pipeline execution over REST:
//
//	GET  /api/health   — server status
//	GET  /api/files    — data files in -dir
//	POST /api/execute  — {"pipeline":"ssql from x.csv | ssql …"} → streamed output
//	POST /api/cursor   — {"argv":["-complete","3","ssql","where",…]} → cursor protocol
//
// Execution is self-exec: each pipeline stage runs os.Executable()
// with a tokenized argv, stdout→stdin chained — the exec lane's exact
// semantics, no shell anywhere (metacharacters are inert; the strict
// tokenizer rejects shell-isms loudly rather than let users believe
// redirection or substitution happened). /api/cursor forwards the
// cursor-context protocol (completion, help-at, value sampling) to a
// subprocess the same way the CLI keybindings and the playground do.
//
// Trust model (deliberate, Jupyter-style): pipelines run with the
// server process's authority — -dir is the working directory, NOT a
// sandbox. The listener therefore binds loopback by default and
// REFUSES to bind non-loopback without -token.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

type serveHTTPOptions struct {
	Addr     string        // listen address, e.g. "127.0.0.1:8080"
	Dir      string        // working directory for pipelines and /api/files
	Token    string        // optional bearer token; required for non-loopback
	Timeout  time.Duration // per-request pipeline wall clock
	Readonly bool          // reject pipelines that write files (tee, to FMT FILE, generate -run/-build)
	Stderr   io.Writer     // startup/diagnostic log
}

// serveSampleThreshold: above this size, the explore workspace for a
// bare file opens with a VISIBLE `limit` stage in the head instead of
// materializing the whole file into the page (a 1.2GB CSV made a
// multi-GB page). The user sees the limit in the head input and can
// edit or remove it — honest sampling, not silent truncation.
const serveSampleThreshold = 32 << 20

const serveSampleRows = 1000

// serveDataExt reports whether /api/files lists a file — any format
// the authority table knows (DFC116: a new format registers there
// and appears in serve listings automatically).
func serveDataExt(ext string) bool {
	_, ok := formatByExt[ext]
	return ok
}

// startServeHTTP binds the listener (so ":0" resolves to a real port,
// which the integration tests rely on), logs the address, and serves
// in a goroutine. The returned channel yields the terminal error when
// the server stops; ctx cancellation shuts it down gracefully.
func startServeHTTP(ctx context.Context, o serveHTTPOptions) (string, <-chan error, error) {
	if o.Dir == "" {
		o.Dir = "."
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return "", nil, fmt.Errorf("serve -dir: %w", err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", nil, fmt.Errorf("serve -dir: %s is not a directory", dir)
	}
	o.Dir = dir

	// `tailscale:PORT` sugar: bind this machine's Tailscale address
	// without looking it up (DFC079's "or with Tailscale, access
	// directly").
	if host, port, err := net.SplitHostPort(o.Addr); err == nil && host == "tailscale" {
		ip, err := tailscaleAddr()
		if err != nil {
			return "", nil, fmt.Errorf("serve -listen-http tailscale:%s: %w", port, err)
		}
		o.Addr = net.JoinHostPort(ip.String(), port)
	}

	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		return "", nil, fmt.Errorf("serve -listen-http: %w", err)
	}
	if o.Token == "" && !listenerIsLoopback(ln) {
		// A tailnet is WireGuard-authenticated and encrypted — treat a
		// Tailscale bind as trusted, but say the trust assumption out
		// loud. Any OTHER non-loopback bind still refuses.
		if ta, ok := ln.Addr().(*net.TCPAddr); ok && isTailscaleIP(ta.IP) {
			fmt.Fprintf(o.Stderr, "ssql serve: tailnet-trusted mode — anyone on your tailnet (including shared nodes) can run pipelines as this user; add -token for defense in depth\n")
		} else {
			ln.Close()
			return "", nil, fmt.Errorf(
				"serve -listen-http %s binds a non-loopback address without -token — refusing to start (pipelines run with this process's authority; add -token TOKEN, bind 127.0.0.1, or bind your Tailscale address / tailscale:PORT)", o.Addr)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		// Straight to the workspace: head completion lists the server's
		// files (Tab after "ssql from "), so a click-a-file index page
		// is a needless detour.
		q := url.Values{}
		if o.Token != "" {
			q.Set("token", o.Token)
		}
		target := "/explore"
		if enc := q.Encode(); enc != "" {
			target += "?" + enc
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
	mux.HandleFunc("GET /explore", func(w http.ResponseWriter, r *http.Request) {
		serveExplorePage(w, r, o)
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok", "version": version.Version, "dir": o.Dir,
		})
	})
	mux.HandleFunc("GET /api/files", func(w http.ResponseWriter, r *http.Request) {
		serveListFiles(w, o.Dir)
	})
	mux.HandleFunc("POST /api/execute", func(w http.ResponseWriter, r *http.Request) {
		serveExecute(w, r, o)
	})
	mux.HandleFunc("POST /api/cursor", func(w http.ResponseWriter, r *http.Request) {
		serveCursor(w, r, o)
	})
	mux.HandleFunc("POST /api/schema-fields", func(w http.ResponseWriter, r *http.Request) {
		serveSchemaFields(w, r, o)
	})
	mux.HandleFunc("POST /api/optimize", func(w http.ResponseWriter, r *http.Request) {
		serveOptimize(w, r, o)
	})
	mux.HandleFunc("GET /api/raw", func(w http.ResponseWriter, r *http.Request) {
		serveRawFile(w, r, o)
	})

	srv := &http.Server{Handler: serveAuth(o.Token, mux)}
	addr := ln.Addr().String()
	fmt.Fprintf(o.Stderr, "ssql serve: http listening on %s (dir %s)\n", addr, o.Dir)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	return addr, done, nil
}

func listenerIsLoopback(ln net.Listener) bool {
	if ta, ok := ln.Addr().(*net.TCPAddr); ok {
		return ta.IP.IsLoopback()
	}
	return false
}

// serveAuth enforces `Authorization: Bearer TOKEN` (or ?token=) on
// every endpoint when a token is configured.
func serveAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if got != token {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing or wrong token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func serveListFiles(w http.ResponseWriter, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	type fileInfo struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	files := []fileInfo{}
	for _, e := range entries {
		if e.IsDir() || !serveDataExt(strings.ToLower(filepath.Ext(e.Name()))) {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, fileInfo{Name: e.Name(), Size: info.Size()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// splitServePipeline tokenizes a pipeline string into per-stage argv
// lists. Strict by design: quotes (single/double) group words and are
// inert otherwise; there is NO shell, so anything that only a shell
// could honour — redirection, command substitution, process
// substitution, `;`/`&` chaining — is a loud error rather than a
// silently inert argument.
func splitServePipeline(s string) ([][]string, error) {
	var stages [][]string
	var cur []string
	var word strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			cur = append(cur, word.String())
			word.Reset()
			inWord = false
		}
	}
	flushStage := func() error {
		flushWord()
		if len(cur) == 0 {
			return fmt.Errorf("empty pipeline stage")
		}
		if cur[0] != "ssql" {
			return fmt.Errorf("each stage must start with 'ssql' (got %q)", cur[0])
		}
		if len(cur) == 1 {
			return fmt.Errorf("stage 'ssql' has no subcommand")
		}
		stages = append(stages, cur[1:])
		cur = nil
		return nil
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'', '"':
			quote := c
			inWord = true
			j := i + 1
			for ; j < len(s) && s[j] != quote; j++ {
				word.WriteByte(s[j])
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated %c-quote", quote)
			}
			i = j
		case ' ', '\t', '\n':
			flushWord()
		case '|':
			if err := flushStage(); err != nil {
				return nil, err
			}
		case '>', '<', ';', '&', '`':
			return nil, fmt.Errorf("%q is a shell construct — serve pipelines run without a shell (no redirection, substitution, or chaining); quote it if you meant a literal value", string(c))
		case '$':
			if i+1 < len(s) && s[i+1] == '(' {
				return nil, fmt.Errorf("command substitution $() is not supported over serve")
			}
			inWord = true
			word.WriteByte(c)
		default:
			inWord = true
			word.WriteByte(c)
		}
	}
	if err := flushStage(); err != nil {
		return nil, err
	}
	return stages, nil
}

// serveExecute runs a pipeline and streams the last stage's stdout.
// Errors before the first output byte are proper HTTP errors; after
// streaming starts, the exit status travels in the X-Ssql-Exit-Code
// trailer (an early-terminated body plus a non-"0" trailer signals a
// mid-stream failure).
func serveExecute(w http.ResponseWriter, r *http.Request, o serveHTTPOptions) {
	var req struct {
		Pipeline string `json:"pipeline"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Pipeline) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {\"pipeline\": \"ssql … | ssql …\"}"})
		return
	}
	stages, err := splitServePipeline(req.Pipeline)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if o.Readonly {
		if err := validateReadonly(stages); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
			return
		}
	}
	self, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	buffered := r.URL.Query().Get("mode") == "buffered"
	engine := r.URL.Query().Get("engine")
	if engine != "" && engine != "exec" && engine != "typed" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("engine %q not supported (exec or typed)", engine)})
		return
	}

	ctx := r.Context()
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}

	engineUsed := "exec"
	var compileMs int64
	var ch *stageChain
	if engine == "typed" {
		bin, cached, dur, cerr := serveTypedHeadBinary(ctx, self, o.Dir, stages)
		if cerr != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error": "typed compile failed", "detail": cerr.Error()})
			return
		}
		compileMs = dur.Milliseconds()
		if cached {
			engineUsed = "typed-cached"
		} else {
			engineUsed = "typed-compiled"
		}
		ch, err = startStageChain(ctx, bin, o.Dir, [][]string{{}})
	} else {
		ch, err = startStageChain(ctx, self, o.Dir, stages)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("X-Ssql-Engine", engineUsed)

	if buffered {
		// Browser mode: fetch() can't read HTTP trailers, so collect
		// everything and answer in one JSON envelope. Workspace head
		// runs are snapshot-shaped anyway (the result lands in the
		// page's data.jsonl); streaming clients keep the default mode.
		t0 := time.Now()
		out, _ := io.ReadAll(ch.out)
		code := 0
		if runErr := ch.wait(); runErr != nil {
			code = 1
		}
		resp := map[string]any{
			"output": string(out), "stderr": ch.stderr(), "code": code,
			"engine": engineUsed, "compileMs": compileMs,
			"runMs": time.Since(t0).Milliseconds(),
		}
		if rows, ok := headInputRows(r.Context(), self, o.Dir, stages); ok {
			resp["inputRows"] = rows
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	w.Header().Set("Trailer", "X-Ssql-Exit-Code, X-Ssql-Error")
	w.Header().Set("Content-Type", "application/octet-stream")

	// Hold back until the first Read (NOT a full-buffer read — that
	// would stall streaming pipelines) so a pipeline that fails before
	// producing output becomes a clean HTTP 422 with stderr, not an
	// empty 200.
	buf := make([]byte, 32*1024)
	var n int
	var readErr error
	for {
		n, readErr = ch.out.Read(buf)
		if n > 0 || readErr != nil {
			break
		}
	}
	firstChunk := buf[:n]

	if n == 0 {
		if runErr := ch.wait(); runErr != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error": "pipeline failed", "detail": ch.stderr(),
			})
			return
		}
		w.Header().Set("X-Ssql-Exit-Code", "0")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(firstChunk)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if readErr == nil { // more output than one buffer
		io.Copy(&flushingWriter{w: w}, ch.out)
	}
	if runErr := ch.wait(); runErr != nil {
		w.Header().Set("X-Ssql-Exit-Code", "1")
		w.Header().Set("X-Ssql-Error", firstLine(ch.stderr()))
	} else {
		w.Header().Set("X-Ssql-Exit-Code", "0")
	}
}

// stageChain is a started pipeline of self-exec stages with per-stage
// stderr capture (a shared buffer would be a data race).
type stageChain struct {
	cmds    []*exec.Cmd
	stderrs []bytes.Buffer
	out     io.ReadCloser // last stage's stdout
}

// startStageChain wires and starts `self stage[0] | self stage[1] | …`
// in dir under ctx.
//
// Intermediate links are explicit os.Pipe()s and the parent CLOSES its
// copies right after starting the children — like a shell, the
// children must be the only holders. (The first version used
// exec.StdoutPipe, whose read end the parent keeps until Wait: when a
// downstream stage exited early — `… | limit 10` on a 1.2GB file —
// upstream never got EPIPE, filled the 64KB pipe buffer, and the
// chain deadlocked. Found live by Ross; pinned by
// TestServeExecuteEarlyExit.)
func startStageChain(ctx context.Context, self, dir string, stages [][]string, extraEnv ...string) (*stageChain, error) {
	ch := &stageChain{stderrs: make([]bytes.Buffer, len(stages))}
	ch.cmds = make([]*exec.Cmd, len(stages))
	var parentCopies []*os.File
	closeParentCopies := func() {
		for _, f := range parentCopies {
			f.Close()
		}
	}
	for i, args := range stages {
		ch.cmds[i] = exec.CommandContext(ctx, self, args...)
		ch.cmds[i].Dir = dir
		if len(extraEnv) > 0 {
			ch.cmds[i].Env = append(os.Environ(), extraEnv...)
		}
		ch.cmds[i].Stderr = &ch.stderrs[i]
		if i > 0 {
			r, w, err := os.Pipe()
			if err != nil {
				closeParentCopies()
				return nil, err
			}
			ch.cmds[i-1].Stdout = w
			ch.cmds[i].Stdin = r
			parentCopies = append(parentCopies, r, w)
		}
	}
	out, err := ch.cmds[len(ch.cmds)-1].StdoutPipe()
	if err != nil {
		closeParentCopies()
		return nil, err
	}
	ch.out = out
	for _, c := range ch.cmds {
		if err := c.Start(); err != nil {
			closeParentCopies()
			return nil, err
		}
	}
	// The children hold dups now; the parent must not keep the pipes
	// alive or early-exiting consumers can't EPIPE their producers.
	closeParentCopies()
	return ch, nil
}

// wait waits for every stage, with shell status semantics: the LAST
// stage's error wins; an upstream stage killed by a signal (SIGPIPE
// after its consumer exited early — `limit`, `head`-like flows) is
// normal termination, not failure. Upstream real exit codes still
// count when the last stage succeeded.
func (ch *stageChain) wait() error {
	var lastErr, upstreamErr error
	for i, c := range ch.cmds {
		err := c.Wait()
		if err == nil {
			continue
		}
		if i == len(ch.cmds)-1 {
			lastErr = err
			continue
		}
		// ExitCode() == -1 means death by signal — expected upstream
		// when downstream closed the pipe early.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == -1 {
			continue
		}
		if upstreamErr == nil {
			upstreamErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return upstreamErr
}

// stderr joins the non-empty per-stage stderr captures, each labelled
// with its stage command, FAILING stages first — a stage-0 notice
// (e.g. the sample seed) must not bury the actual error below it
// (found by Ross: "Head pipeline failed" led with the seed line).
func (ch *stageChain) stderr() string {
	var failed, notes []string
	for i := range ch.stderrs {
		t := strings.TrimSpace(ch.stderrs[i].String())
		if t == "" {
			continue
		}
		if ws := ch.cmds[i].ProcessState; ws != nil && ws.ExitCode() != 0 {
			name := "stage"
			if i < len(ch.cmds) && len(ch.cmds[i].Args) > 1 {
				name = ch.cmds[i].Args[1]
			}
			failed = append(failed, name+" failed: "+t)
		} else {
			// Verbatim: passing-stage stderr is a protocol surface —
			// /api/optimize parses [rewrite-name] annotation lines out
			// of it (labelling broke that; caught by TestServeOptimize).
			notes = append(notes, t)
		}
	}
	return strings.Join(append(failed, notes...), "\n")
}

type flushingWriter struct{ w http.ResponseWriter }

func (fw *flushingWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if f, ok := fw.w.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// serveCursorVerbs is the allow-list for /api/cursor — the read-only
// cursor-context protocol (completion, help, value sampling), exactly
// what the CLI keybindings and playground use. Nothing else reaches
// the subprocess through this endpoint.
var serveCursorVerbs = map[string]bool{
	"-cursor-stage": true, "-help-at": true, "-complete": true,
	"-complete-source": true, "-value-source": true,
}

func serveCursor(w http.ResponseWriter, r *http.Request, o serveHTTPOptions) {
	var req struct {
		Argv []string          `json:"argv"`
		Env  map[string]string `json:"env,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || len(req.Argv) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {\"argv\": [\"-complete\", …]}"})
		return
	}
	// Env is allow-listed: completion needs exactly the value-sampling
	// parameter the bash script and the playground pass. Anything else
	// is rejected loudly rather than silently dropped.
	for k := range req.Env {
		if k != "AUTOCLI_CACHE_FILE" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("env %q not allowed on /api/cursor (only AUTOCLI_CACHE_FILE)", k)})
			return
		}
	}
	if !serveCursorVerbs[req.Argv[0]] {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("argv[0] must be a cursor-protocol verb (%q not allowed)", req.Argv[0]),
		})
		return
	}
	self, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, req.Argv...)
	cmd.Dir = o.Dir
	if len(req.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stdout": stdout.String(), "stderr": stderr.String(), "code": code,
	})
}

// listDataFiles returns the data files (by extension) in dir, sorted.
func listDataFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && serveDataExt(strings.ToLower(filepath.Ext(e.Name()))) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// serveExplorePage runs `<head pipeline> | ssql to explore TMP` through
// the stage chain and serves the generated workspace — byte-identical
// to a locally generated `to explore` artifact (wasm engine included on
// full builds; slim serve builds degrade to the light viewer). ?file=X
// is shorthand for the head `ssql from X` and is allow-listed against
// the directory listing; ?pipeline=… goes through the same strict
// tokenizer as /api/execute.
func serveExplorePage(w http.ResponseWriter, r *http.Request, o serveHTTPOptions) {
	var stages [][]string
	var title string
	var emptyOK bool
	switch {
	case r.URL.Query().Get("file") != "":
		file := r.URL.Query().Get("file")
		names, err := listDataFiles(o.Dir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !slices.Contains(names, file) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": fmt.Sprintf("%q is not a data file in the served directory", file)})
			return
		}
		if st, err := os.Stat(filepath.Join(o.Dir, file)); err == nil && st.Size() > serveSampleThreshold {
			// Redirect to the sampled pipeline form so the limit is
			// VISIBLE in the head input rather than silently applied.
			q := url.Values{}
			// Byte-offset sampling at the source (Ross's algorithm,
			// DFC110 amendment): whole-file representative AND fast
			// (14ms vs 21s for the exact reservoir on a 1.2GB CSV).
			// Visible and dialable in the head as always. Line formats
			// only; other big formats (parquet/arrow) fall back to the
			// exact sample stage — slower but honest.
			var pipe string
			if fi, ok := formatForPath(file); ok && fi.Sampleable {
				pipe = fmt.Sprintf("ssql from %s %s -sample %d", fi.Name, file, serveSampleRows)
			} else {
				pipe = fmt.Sprintf("ssql from %s | ssql sample %d", file, serveSampleRows)
			}
			q.Set("pipeline", pipe)
			if o.Token != "" {
				q.Set("token", o.Token)
			}
			http.Redirect(w, r, "/explore?"+q.Encode(), http.StatusFound)
			return
		}
		stages = [][]string{{"from", file}}
		title = file
	case r.URL.Query().Get("pipeline") != "":
		var err error
		stages, err = splitServePipeline(r.URL.Query().Get("pipeline"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		title = "pipeline result"
	default:
		// Empty workspace: no data yet — the user types the head
		// ("ssql from <Tab>" completes the server's files) and runs it.
		stages = [][]string{{"from", "jsonl", "/dev/null"}}
		title = "ssql workspace"
		emptyOK = true
	}

	tmp, err := os.CreateTemp("", "ssql-serve-explore-*.html")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	exploreStage := []string{"to", "explore", "-title", title}
	if emptyOK {
		exploreStage = append(exploreStage, "-allow-empty")
	}
	stages = append(stages, append(exploreStage, tmpPath))

	self, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ctx := r.Context()
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	ch, err := startStageChain(ctx, self, o.Dir, stages)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	io.Copy(io.Discard, ch.out) // "Explorer created" notice
	if runErr := ch.wait(); runErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "pipeline failed", "detail": ch.stderr(),
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, tmpPath)
}

// serveSchemaFields answers pipeline-aware field-name completion (the
// CLI's Ctrl-O, the wasm tail's schemaFieldsAtCursor) for HTTP-bound
// inputs: run the given upstream pipeline under SSQL_MODE=schema —
// commands transform a schema header instead of data, so only file
// headers are read — then `generate schema` prints the field names.
// The pipeline goes through the same strict tokenizer as /api/execute.
func serveSchemaFields(w http.ResponseWriter, r *http.Request, o serveHTTPOptions) {
	var req struct {
		Pipeline string `json:"pipeline"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Pipeline) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {\"pipeline\": \"ssql …\"}"})
		return
	}
	stages, err := splitServePipeline(req.Pipeline)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	stages = append(stages, []string{"generate", "schema"})
	self, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ch, err := startStageChain(ctx, self, o.Dir, stages, "SSQL_MODE=schema")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	out, _ := io.ReadAll(ch.out)
	if runErr := ch.wait(); runErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "schema pipeline failed", "detail": ch.stderr()})
		return
	}
	fields := []string{}
	for _, l := range strings.Split(string(out), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			fields = append(fields, t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": fields})
}

// serveRawFile hands a data file's raw bytes to the browser so the
// workspace can load side tables (join inputs) into its virtual FS
// under their own names. Allow-listed against the directory listing;
// files over serveSampleThreshold refuse loudly — reduce them through
// a head pipeline instead of shipping them whole to a browser.
func serveRawFile(w http.ResponseWriter, r *http.Request, o serveHTTPOptions) {
	file := r.URL.Query().Get("file")
	names, err := listDataFiles(o.Dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if file == "" || !slices.Contains(names, file) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": fmt.Sprintf("%q is not a data file in the served directory", file)})
		return
	}
	path := filepath.Join(o.Dir, file)
	st, err := os.Stat(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if st.Size() > serveSampleThreshold {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": fmt.Sprintf("%s is %dMB — too large to load raw into a browser; reduce it through a head pipeline (e.g. ssql from %s | ssql sample 1000) and use data.jsonl",
				file, st.Size()>>20, file)})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

// serveTypedHeadBinary returns a compiled typed-mode binary for the
// given (already-validated) stages, building into a per-user cache on
// miss. The cache key is the rendered script plus this binary's
// version+commit, so upgrades never serve stale codegen. This is the
// ssh pushdown's codegen-symmetric design one hop closer: the same
// `generate go -script -mode typed` the remote end runs.
func serveTypedHeadBinary(ctx context.Context, self, dir string, stages [][]string) (string, bool, time.Duration, error) {
	var sb strings.Builder
	for i, args := range stages {
		if i > 0 {
			sb.WriteString("| ")
		}
		sb.WriteString("ssql")
		for _, a := range args {
			sb.WriteString(" ")
			sb.WriteString(ssql.ShellQuote(a))
		}
		sb.WriteString("\n")
	}
	script := sb.String()

	sum := sha256.Sum256([]byte(script + "\x00" + version.Version + "\x00" + version.Commit))
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		cacheRoot = os.TempDir()
	}
	cacheDir := filepath.Join(cacheRoot, "ssql-serve")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", false, 0, err
	}
	bin := filepath.Join(cacheDir, fmt.Sprintf("head-%x", sum[:12]))
	if _, err := os.Stat(bin); err == nil {
		return bin, true, 0, nil
	}

	t0 := time.Now()
	scriptFile, err := os.CreateTemp("", "ssql-serve-head-*.ssql")
	if err != nil {
		return "", false, 0, err
	}
	scriptPath := scriptFile.Name()
	defer os.Remove(scriptPath)
	if _, err := scriptFile.WriteString(script); err != nil {
		scriptFile.Close()
		return "", false, 0, err
	}
	scriptFile.Close()

	tmpBin := bin + ".tmp"
	cmd := exec.CommandContext(ctx, self, "generate", "go", "-script", scriptPath, "-mode", "typed", "-build", tmpBin)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmpBin)
		return "", false, 0, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := os.Rename(tmpBin, bin); err != nil {
		return "", false, 0, err
	}
	return bin, false, time.Since(t0), nil
}

// isTailscaleIP reports whether ip belongs to Tailscale's address
// space: the CGNAT IPv4 range 100.64.0.0/10 or the tailnet IPv6 ULA
// prefix fd7a:115c:a1e0::/48.
func isTailscaleIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	if v6 := ip.To16(); v6 != nil {
		return v6[0] == 0xfd && v6[1] == 0x7a && v6[2] == 0x11 && v6[3] == 0x5c && v6[4] == 0xa1 && v6[5] == 0xe0
	}
	return false
}

// tailscaleAddr returns this machine's Tailscale IP (IPv4 preferred).
func tailscaleAddr() (net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var v6 net.IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || !isTailscaleIP(ipn.IP) {
			continue
		}
		if ipn.IP.To4() != nil {
			return ipn.IP, nil
		}
		if v6 == nil {
			v6 = ipn.IP
		}
	}
	if v6 != nil {
		return v6, nil
	}
	return nil, fmt.Errorf("no Tailscale address found on any interface (is tailscale up?)")
}

// serveOptimize runs the strict-tokenized pipeline through the stage
// chain in fragment mode (SSQL_MODE=record) with a `generate ssql
// -explain` tail and returns the optimizer's rewritten pipeline plus
// the named rewrite annotations. The same peephole optimizer the CLI
// and playground use — one implementation (DFC065).
func serveOptimize(w http.ResponseWriter, r *http.Request, o serveHTTPOptions) {
	var req struct {
		Pipeline string `json:"pipeline"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Pipeline) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {\"pipeline\": \"ssql …\"}"})
		return
	}
	stages, err := splitServePipeline(req.Pipeline)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	stages = append(stages, []string{"generate", "ssql", "-explain"})
	self, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ch, err := startStageChain(ctx, self, o.Dir, stages, "SSQL_MODE=record")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	out, _ := io.ReadAll(ch.out)
	if runErr := ch.wait(); runErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "optimize failed", "detail": ch.stderr()})
		return
	}
	// stdout carries the final pipeline; the "[rewrite-name] before →
	// after" -explain annotations ride stderr.
	var rewrites []string
	var optimized string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			optimized = l
		}
	}
	for _, l := range strings.Split(ch.stderr(), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "[") {
			rewrites = append(rewrites, l)
		}
	}
	if rewrites == nil {
		rewrites = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"optimized": optimized, "rewrites": rewrites,
		"changed": optimized != strings.TrimSpace(req.Pipeline),
	})
}

// --- input-row accounting for head throughput display ---
//
// "8.9M rows/s" is the number that shows what a head run actually did
// (a group-by reads millions and emits dozens). The COUNT comes from
// the source command itself: `from … -records` prints the record
// count of that exact invocation, computed the cheapest way the
// format allows (parquet footer, newline scan for line formats,
// -sample interplay — all parsed by from's own flag grammar). This
// replaced a hand-rolled re-parse of from-args that accumulated three
// drift bugs in a week (parquet subcommand, -columns values, -sample
// handling); serve now execs the knowledge owner instead of copying
// it.
//
// Line-format counts cost ~0.15s/GB per call, so serve caches by the
// stage-0 argv (opaque — no parsing), invalidated by a fingerprint of
// the served directory's data files (one readdir; any change flushes
// the cache). Coarse but correct, and grammar-free.

type recordsCacheEntry struct {
	fingerprint string
	count       int64
}

var (
	recordsCacheMu sync.Mutex
	recordsCache   = map[string]recordsCacheEntry{}
)

// dirDataFingerprint summarizes the data files in dir (name, size,
// mtime) — the invalidation key for the records cache.
func dirDataFingerprint(dir string) string {
	names, err := listDataFiles(dir)
	if err != nil {
		return "err:" + err.Error()
	}
	var b strings.Builder
	for _, n := range names {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", n, st.Size(), st.ModTime().UnixNano())
		}
	}
	return b.String()
}

// headInputRows asks the head's SOURCE stage for its record count via
// the -records protocol. (0, false) when the stage isn't a from, the
// format has no cheap count, or the exec fails — omission over
// guessing, always.
func headInputRows(ctx context.Context, self, dir string, stages [][]string) (int64, bool) {
	if len(stages) == 0 || len(stages[0]) == 0 || stages[0][0] != "from" {
		return 0, false
	}
	key := strings.Join(stages[0], "\x00")
	fp := dirDataFingerprint(dir)
	recordsCacheMu.Lock()
	if e, ok := recordsCache[key]; ok && e.fingerprint == fp {
		recordsCacheMu.Unlock()
		return capByEarlyLimit(e.count, stages), true
	}
	recordsCacheMu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := append(append([]string{}, stages[0]...), "-records")
	cmd := exec.CommandContext(cctx, self, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err != nil {
		return 0, false
	}
	recordsCacheMu.Lock()
	recordsCache[key] = recordsCacheEntry{fp, n}
	recordsCacheMu.Unlock()
	return capByEarlyLimit(n, stages), true
}

// capByEarlyLimit caps rows by a `limit N` immediately after the
// source — the pipeline then reads only N rows.
func capByEarlyLimit(rows int64, stages [][]string) int64 {
	if len(stages) > 1 && len(stages[1]) >= 2 && stages[1][0] == "limit" {
		var lim int64
		if _, err := fmt.Sscanf(stages[1][1], "%d", &lim); err == nil && lim > 0 && lim < rows {
			return lim
		}
	}
	return rows
}

// validateReadonly rejects USER pipeline stages that write files.
// Conservative by design: in -readonly mode a `to` sink is allowed
// only in its plain to-stdout form — any bare token after the format
// (which may be a FILE, or may be a flag's value we can't classify
// without copying the command's grammar — DFC115 says don't) rejects,
// as does -o. tee always writes; generate go -run/-build write and
// execute. False positives are safe and the errors say exactly why.
func validateReadonly(stages [][]string) error {
	for _, st := range stages {
		if len(st) == 0 {
			continue
		}
		switch st[0] {
		case "tee":
			return fmt.Errorf("readonly serve: `tee` writes a file — not permitted (-readonly)")
		case "to":
			for i := 2; i < len(st); i++ {
				a := st[i]
				if a == "-o" {
					return fmt.Errorf("readonly serve: `to %s -o` writes a file — not permitted (-readonly)", st[1])
				}
				if !strings.HasPrefix(a, "-") {
					return fmt.Errorf("readonly serve: `to %s %s` may write a file — only plain to-stdout sinks are permitted (-readonly is conservative: flag values also trigger this)", st[1], a)
				}
			}
		case "generate":
			for _, a := range st[1:] {
				if a == "-run" || a == "-r" || a == "-build" || a == "-b" {
					return fmt.Errorf("readonly serve: `generate %s` compiles/writes — not permitted (-readonly)", a)
				}
			}
		}
	}
	return nil
}
