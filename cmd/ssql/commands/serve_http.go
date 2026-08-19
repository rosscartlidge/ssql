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
	"encoding/json"
	"fmt"
	"html"
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
	"time"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

type serveHTTPOptions struct {
	Addr    string        // listen address, e.g. "127.0.0.1:8080"
	Dir     string        // working directory for pipelines and /api/files
	Token   string        // optional bearer token; required for non-loopback
	Timeout time.Duration // per-request pipeline wall clock
	Stderr  io.Writer     // startup/diagnostic log
}

// serveDataExtensions is what /api/files lists.
var serveDataExtensions = map[string]bool{
	".csv": true, ".tsv": true, ".json": true, ".jsonl": true,
	".parquet": true, ".arrow": true, ".wav": true, ".xlsx": true,
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

	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		return "", nil, fmt.Errorf("serve -listen-http: %w", err)
	}
	if o.Token == "" && !listenerIsLoopback(ln) {
		ln.Close()
		return "", nil, fmt.Errorf(
			"serve -listen-http %s binds a non-loopback address without -token — refusing to start (pipelines run with this process's authority; add -token TOKEN or bind 127.0.0.1)", o.Addr)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w, r, o)
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
		if e.IsDir() || !serveDataExtensions[strings.ToLower(filepath.Ext(e.Name()))] {
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
	self, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	buffered := r.URL.Query().Get("mode") == "buffered"

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

	if buffered {
		// Browser mode: fetch() can't read HTTP trailers, so collect
		// everything and answer in one JSON envelope. Workspace head
		// runs are snapshot-shaped anyway (the result lands in the
		// page's data.jsonl); streaming clients keep the default mode.
		out, _ := io.ReadAll(ch.out)
		code := 0
		if runErr := ch.wait(); runErr != nil {
			code = 1
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"output": string(out), "stderr": ch.stderr(), "code": code,
		})
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
func startStageChain(ctx context.Context, self, dir string, stages [][]string) (*stageChain, error) {
	ch := &stageChain{stderrs: make([]bytes.Buffer, len(stages))}
	ch.cmds = make([]*exec.Cmd, len(stages))
	for i, args := range stages {
		ch.cmds[i] = exec.CommandContext(ctx, self, args...)
		ch.cmds[i].Dir = dir
		ch.cmds[i].Stderr = &ch.stderrs[i]
		if i > 0 {
			pipe, err := ch.cmds[i-1].StdoutPipe()
			if err != nil {
				return nil, err
			}
			ch.cmds[i].Stdin = pipe
		}
	}
	out, err := ch.cmds[len(ch.cmds)-1].StdoutPipe()
	if err != nil {
		return nil, err
	}
	ch.out = out
	for _, c := range ch.cmds {
		if err := c.Start(); err != nil {
			return nil, err
		}
	}
	return ch, nil
}

// wait waits for every stage; the first stage error (if any) wins.
func (ch *stageChain) wait() error {
	var firstErr error
	for _, c := range ch.cmds {
		if err := c.Wait(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// stderr joins the non-empty per-stage stderr captures.
func (ch *stageChain) stderr() string {
	var parts []string
	for i := range ch.stderrs {
		if t := strings.TrimSpace(ch.stderrs[i].String()); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
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
		Argv []string `json:"argv"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || len(req.Argv) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {\"argv\": [\"-complete\", …]}"})
		return
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
		if !e.IsDir() && serveDataExtensions[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// serveIndex renders a minimal landing page: the served directory's
// data files, each linking to its explore workspace. Any configured
// token is threaded through the links (the Jupyter ?token= pattern).
func serveIndex(w http.ResponseWriter, r *http.Request, o serveHTTPOptions) {
	names, err := listDataFiles(o.Dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	tokenQ := ""
	if o.Token != "" {
		tokenQ = "&token=" + url.QueryEscape(o.Token)
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>ssql serve</title>
<style>body{font-family:system-ui,sans-serif;max-width:640px;margin:3rem auto;padding:0 1rem}
li{margin:.4rem 0}code{background:#eee;padding:1px 5px;border-radius:3px}</style></head><body>
<h1>ssql serve</h1><p>Data files in <code>` + html.EscapeString(o.Dir) + `</code> — click one to open its explore workspace:</p><ul>`)
	for _, n := range names {
		esc := html.EscapeString(n)
		b.WriteString(`<li><a href="/explore?file=` + url.QueryEscape(n) + tokenQ + `">` + esc + `</a></li>`)
	}
	b.WriteString(`</ul><p>Or run a head pipeline server-side and explore its result:<br>
<code>/explore?pipeline=` + html.EscapeString(url.QueryEscape("ssql from FILE.csv | ssql where -if …")) + `</code></p>
<p>API: <code>POST /api/execute</code> · <code>POST /api/cursor</code> · <code>GET /api/files</code> · <code>GET /api/health</code></p>
</body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(b.String()))
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "need ?file=NAME or ?pipeline=ssql …"})
		return
	}

	tmp, err := os.CreateTemp("", "ssql-serve-explore-*.html")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	stages = append(stages, []string{"to", "explore", "-title", title, tmpPath})

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
