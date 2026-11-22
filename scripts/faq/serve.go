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
	logMax  int64         // past this size the log moves to .1 (0: no cap)
	logPerm os.FileMode   // 0600, or 0640 with -log-shared
	logMu   sync.Mutex
	tmp     string   // where visitors' data.csv directories go
	page    []byte   // the page as this mode says it
	public  bool     // -public: Funnel's instance (public.go)
	limit   *limiter // -public: questions per client and per day
}

type askRequest struct {
	Question string    `json:"question"`
	Header   string    `json:"header,omitempty"`
	Previous *exchange `json:"previous,omitempty"` // the exchange a follow-up follows
}

// maxPrevReply caps the earlier reply a follow-up carries.
const maxPrevReply = 8 << 10

type askResponse struct {
	Reply      string   `json:"reply"`
	Pipeline   string   `json:"pipeline,omitempty"`
	Verified   bool     `json:"verified"`
	RanOn      string   `json:"ran_on,omitempty"` // sample, your-columns, your-rows, setup
	Output     string   `json:"output,omitempty"`
	Failure    string   `json:"failure,omitempty"`
	Status     string   `json:"status"` // verified, unverified, unsupported, illustration, setup, prose
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
	FollowUp    bool    `json:"follow_up,omitempty"`
	Model       string  `json:"model"`
	Pipeline    string  `json:"pipeline,omitempty"`
	Status      string  `json:"status"`
	Failure     string  `json:"failure,omitempty"`
	Reply       string  `json:"reply,omitempty"`   // the model's answer, so prose can be reviewed (2026-11-21)
	Revised     bool    `json:"revised,omitempty"` // the review turn changed the pipeline
	Public      bool    `json:"public,omitempty"`  // asked of the public instance
	Corrections int     `json:"corrections"`
	Seconds     float64 `json:"seconds"`
}

func newServer(f *faq, llm ollama, logPath string) *server {
	return &server{f: f, llm: llm, run: make(chan struct{}, 1), logPath: logPath, logMax: 64 << 20, logPerm: 0o600, tmp: os.TempDir(), page: pageHTML}
}

// makePublic turns s into the public instance: the public footer and the
// limits per client and per day.
func (s *server) makePublic(perHour, perDay int) error {
	page, err := publicPage(pageHTML)
	if err != nil {
		return err
	}
	s.page, s.public, s.limit = page, true, newLimiter(perHour, perDay)
	return nil
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(s.page)
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		h := map[string]any{"model": s.llm.model, "busy": s.waiting.Load(), "logging": s.logPath != "", "public": s.public}
		if s.limit != nil {
			h["today"] = s.limit.today()
		}
		writeJSON(w, http.StatusOK, h)
	})
	mux.HandleFunc("POST /api/ask", s.ask)
	if s.public {
		return publicOnly(mux)
	}
	return privateOnly(mux)
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
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
		securityHeaders(w)
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxQuestion+maxPaste+maxPrevReply+4096)).Decode(&req); err != nil {
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
	if p := req.Previous; p != nil {
		p.Question, p.Reply = strings.TrimSpace(p.Question), strings.TrimSpace(p.Reply)
		if p.Question == "" || p.Reply == "" || len(p.Question) > maxQuestion || len(p.Reply) > maxPrevReply {
			req.Previous = nil // an unusable earlier exchange: answer the question alone
		}
	}
	var v *visitor
	if strings.TrimSpace(req.Header) != "" {
		var err error
		if v, err = newVisitor(s.tmp, s.f.box.dataDir, req.Header); err != nil {
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
	// Counted only once the question is accepted: a malformed or turned
	// away one costs the visitor nothing.
	if s.limit != nil {
		if msg, retry := s.limit.allow(clientKey(r)); msg != "" {
			w.Header().Set("Retry-After", fmt.Sprint(int(retry.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, msg)
			return
		}
	}
	select {
	case s.run <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	start := time.Now()
	a := s.f.askAfter(s.llm, req.Question, v, req.Previous)
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
	case a.Setup && a.Ran:
		resp.Status, resp.Verified = "verified", true
	case a.Setup && a.Failure == "":
		resp.Status = "setup" // install lines only: nothing here can run them
	case a.Ran || a.Checked:
		resp.Status, resp.Verified, resp.Output = "verified", true, clip(a.Output, 4000)
	default:
		resp.Status, resp.Failure = "unverified", a.Failure
	}
	// The playground holds the same sample files; a visitor's data.csv
	// is not there.
	if resp.Verified && !a.Setup && !readsFAQOnly(a.Pipeline) && (v == nil || !v.readBy(a.Pipeline)) {
		resp.Playground = playground + "#p=" + base64.RawURLEncoding.EncodeToString([]byte(runnable(a.Pipeline)))
	}
	return resp
}

func (s *server) record(req askRequest, v *visitor, a answer, resp askResponse) {
	if s.logPath == "" {
		return
	}
	l := logLine{Time: time.Now().UTC().Format(time.RFC3339), Question: req.Question, Model: s.llm.model,
		Pipeline: a.Pipeline, Status: resp.Status, Failure: firstLine(resp.Failure), Reply: clip(a.Reply, 8<<10), Revised: a.Revised, Corrections: a.Retries, Seconds: resp.Seconds}
	if v != nil {
		l.PastedCols, l.PastedRows = len(v.cols), v.rows
	}
	l.FollowUp, l.Public = req.Previous != nil, s.public
	b, _ := json.Marshal(l)
	s.logMu.Lock()
	defer s.logMu.Unlock()
	// One old generation is kept, so the log cannot fill the disk.
	if fi, err := os.Stat(s.logPath); err == nil && s.logMax > 0 && fi.Size() >= s.logMax {
		if err := os.Rename(s.logPath, s.logPath+".1"); err != nil {
			fmt.Fprintln(os.Stderr, "faq: question log:", err)
		}
	}
	f, err := os.OpenFile(s.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, s.logPerm)
	if err != nil {
		fmt.Fprintln(os.Stderr, "faq: question log:", err)
		return
	}
	defer f.Close()
	// Chmod, not the create mode, so the service's umask (the public
	// unit's is 0077) cannot take the group's read away.
	if err := f.Chmod(s.logPerm); err != nil {
		fmt.Fprintln(os.Stderr, "faq: question log:", err)
	}
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

// shareLog makes the log's directory, and sets the directory's and any
// existing log's permissions: the group may read them with -log-shared,
// no one but the owner without it. Chmod, so the umask has no say.
func shareLog(path string, shared bool) error {
	dirPerm, filePerm := os.FileMode(0o700), os.FileMode(0o600)
	if shared {
		dirPerm, filePerm = 0o750, 0o640
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return err
	}
	for _, f := range []string{path, path + ".1"} {
		if err := os.Chmod(f, filePerm); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
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
