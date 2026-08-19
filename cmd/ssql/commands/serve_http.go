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
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

	ctx := r.Context()
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}

	// Per-stage stderr buffers: a shared bytes.Buffer would be a data
	// race with stages writing concurrently.
	stderrs := make([]bytes.Buffer, len(stages))
	collectStderr := func() string {
		var parts []string
		for i := range stderrs {
			if t := strings.TrimSpace(stderrs[i].String()); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	cmds := make([]*exec.Cmd, len(stages))
	for i, args := range stages {
		cmds[i] = exec.CommandContext(ctx, self, args...)
		cmds[i].Dir = o.Dir
		cmds[i].Stderr = &stderrs[i]
		if i > 0 {
			pipe, err := cmds[i-1].StdoutPipe()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			cmds[i].Stdin = pipe
		}
	}
	last := cmds[len(cmds)-1]
	out, err := last.StdoutPipe()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	for _, c := range cmds {
		if err := c.Start(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
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
		n, readErr = out.Read(buf)
		if n > 0 || readErr != nil {
			break
		}
	}
	firstChunk := buf[:n]

	waitAll := func() error {
		var firstErr error
		for _, c := range cmds {
			if err := c.Wait(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	if n == 0 {
		runErr := waitAll()
		if runErr != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error": "pipeline failed", "detail": collectStderr(),
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
		io.Copy(&flushingWriter{w: w}, out)
	}
	runErr := waitAll()
	if runErr != nil {
		w.Header().Set("X-Ssql-Exit-Code", "1")
		w.Header().Set("X-Ssql-Error", firstLine(collectStderr()))
	} else {
		w.Header().Set("X-Ssql-Exit-Code", "0")
	}
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
