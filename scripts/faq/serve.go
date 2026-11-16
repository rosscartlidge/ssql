package main

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The FAQ service (DFC169 phase 2). Ross, 2026-11-16: the local network and
// the tailnet only, no Tailscale Funnel yet. So the service answers only
// clients whose address is private (loopback, RFC 1918, Tailscale's ranges)
// and refuses anything Funnel forwarded, which arrives over loopback but
// carries Tailscale-Funnel-Request. Turning Funnel on would then fail
// loudly instead of quietly publishing a laptop: going public is a code
// change, made with the per-address rate limits a public service needs.

//go:embed page.html
var pageHTML []byte

const (
	maxQuestion = 2 << 10
	maxWaiting  = 4
	playground  = "https://rosscartlidge.github.io/ssql/playground.html"
)

type server struct {
	f       *faq
	llm     ollama
	run     chan struct{} // one question at a time: one GPU
	waiting atomic.Int32  // questions queued or running
	logPath string        // "" keeps nothing
	logMu   sync.Mutex
	tmp     string // where visitors' data.csv directories go
}

type askRequest struct {
	Question string `json:"question"`
	Header   string `json:"header,omitempty"`
}

type askResponse struct {
	Reply      string   `json:"reply"`
	Pipeline   string   `json:"pipeline,omitempty"`
	Verified   bool     `json:"verified"`
	RanOn      string   `json:"ran_on,omitempty"` // sample, your-columns, your-rows
	Output     string   `json:"output,omitempty"`
	Failure    string   `json:"failure,omitempty"`
	Status     string   `json:"status"` // verified, unverified, unsupported, illustration, prose
	Playground string   `json:"playground,omitempty"`
	Docs       []string `json:"docs,omitempty"`
	Seconds    float64  `json:"seconds"`
}

// logLine is what is kept of a question: never the address, never the
// pasted data (only how much of it there was).
type logLine struct {
	Time        string  `json:"time"`
	Question    string  `json:"question"`
	PastedCols  int     `json:"pasted_cols,omitempty"`
	PastedRows  int     `json:"pasted_rows,omitempty"`
	Model       string  `json:"model"`
	Pipeline    string  `json:"pipeline,omitempty"`
	Status      string  `json:"status"`
	Failure     string  `json:"failure,omitempty"`
	Corrections int     `json:"corrections"`
	Seconds     float64 `json:"seconds"`
}

func newServer(f *faq, llm ollama, logPath string) *server {
	return &server{f: f, llm: llm, run: make(chan struct{}, 1), logPath: logPath, tmp: os.TempDir()}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(pageHTML)
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"model": s.llm.model, "busy": s.waiting.Load(), "logging": s.logPath != ""})
	})
	mux.HandleFunc("POST /api/ask", s.ask)
	return privateOnly(mux)
}

// privateOnly answers the LAN and the tailnet and refuses everything else.
func privateOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Tailscale-Funnel-Request") != "" {
			http.Error(w, "this FAQ is not public yet", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !privateIP(ip) {
			http.Error(w, "this FAQ answers the local network and the tailnet only", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}

var tailnet4, tailnet6 = mustCIDR("100.64.0.0/10"), mustCIDR("fd7a:115c:a1e0::/48")

// privateIP: loopback, RFC 1918 and IPv6 ULA, link-local, and the Tailscale
// ranges (the CGNAT block Tailscale assigns from, and its ULA prefix).
func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || tailnet4.Contains(ip) || tailnet6.Contains(ip)
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func (s *server) ask(w http.ResponseWriter, r *http.Request) {
	var req askRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuestion+maxPaste+1024)).Decode(&req); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the question or the pasted data is too long, or not JSON")
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	switch {
	case req.Question == "":
		writeError(w, http.StatusBadRequest, "ask a question")
		return
	case len(req.Question) > maxQuestion:
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the question is over %d KB", maxQuestion>>10))
		return
	}
	var v *visitor
	if strings.TrimSpace(req.Header) != "" {
		var err error
		if v, err = newVisitor(s.tmp, req.Header); err != nil {
			writeError(w, http.StatusBadRequest, "your columns: "+err.Error())
			return
		}
		defer os.RemoveAll(v.dir)
	}
	if s.waiting.Add(1) > maxWaiting+1 {
		s.waiting.Add(-1)
		writeError(w, http.StatusServiceUnavailable, "busy answering other questions; try again in a minute")
		return
	}
	defer s.waiting.Add(-1)
	select {
	case s.run <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	start := time.Now()
	a := s.f.ask(s.llm, req.Question, v)
	<-s.run
	resp := respond(a, v, time.Since(start))
	s.record(req, v, a, resp)
	writeJSON(w, http.StatusOK, resp)
}

// respond is what the page shows of an answer.
func respond(a answer, v *visitor, took time.Duration) askResponse {
	resp := askResponse{Reply: a.Reply, Pipeline: a.Pipeline, RanOn: a.RanOn, Docs: a.Docs, Seconds: took.Seconds()}
	switch {
	case a.Reply == "" && a.Failure != "":
		resp.Status, resp.Failure = "error", "the model did not answer: "+a.Failure
	case a.Unsupported:
		resp.Status = "unsupported"
	case a.Pipeline == "":
		resp.Status = "prose"
	case a.Illustrative:
		resp.Status = "illustration"
	case a.Ran || a.Checked:
		resp.Status, resp.Verified, resp.Output = "verified", true, clip(a.Output, 4000)
	default:
		resp.Status, resp.Failure = "unverified", a.Failure
	}
	// The playground holds the same sample files; a visitor's data.csv
	// is not there.
	if resp.Verified && v == nil {
		resp.Playground = playground + "#p=" + base64.RawURLEncoding.EncodeToString([]byte(a.Pipeline))
	}
	return resp
}

func (s *server) record(req askRequest, v *visitor, a answer, resp askResponse) {
	if s.logPath == "" {
		return
	}
	l := logLine{Time: time.Now().UTC().Format(time.RFC3339), Question: req.Question, Model: s.llm.model,
		Pipeline: a.Pipeline, Status: resp.Status, Failure: firstLine(resp.Failure), Corrections: a.Retries, Seconds: resp.Seconds}
	if v != nil {
		l.PastedCols, l.PastedRows = len(v.cols), v.rows
	}
	b, _ := json.Marshal(l)
	s.logMu.Lock()
	defer s.logMu.Unlock()
	f, err := os.OpenFile(s.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "faq: question log:", err)
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// listenAddr resolves `tailscale:PORT` to this machine's Tailscale address,
// as `ssql serve -listen-http` does.
func listenAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "tailscale" {
		return addr, err
	}
	ifs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, a := range ifs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && tailnet4.Contains(n.IP) {
			return net.JoinHostPort(n.IP.String(), port), nil
		}
	}
	return "", fmt.Errorf("-listen %s: no Tailscale address on any interface (is tailscale up?)", addr)
}

// defaultLog is where questions are kept.
func defaultLog() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "ssql-faq", "questions.jsonl")
}
