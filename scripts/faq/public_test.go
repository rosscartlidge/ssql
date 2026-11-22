package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The public instance answers loopback only (tailscaled's Funnel proxy and
// this machine), and the LAN instance still refuses Funnel.
func TestPublicOnly(t *testing.T) {
	h := publicOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, c := range []struct {
		addr string
		want int
	}{
		{"127.0.0.1:5000", 200},
		{"[::1]:5000", 200},
		{"192.168.4.20:5000", 403}, // the LAN reaches the LAN instance, not this one
		{"100.106.137.79:5000", 403},
		{"8.8.8.8:5000", 403},
		{"garbage", 403},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.addr
		r.Header.Set("Tailscale-Funnel-Request", "?1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.addr, w.Code, c.want)
		}
	}
}

// -public refuses an address anything but tailscaled could connect to:
// there, a client could write its own X-Forwarded-For.
func TestLoopbackListen(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:8485": true, "[::1]:8485": true, "localhost:8485": true,
		":8485": false, "0.0.0.0:8485": false, "192.168.4.132:8485": false, "100.106.137.79:8485": false, "nonsense": false,
	} {
		if err := loopbackListen(addr); (err == nil) != ok {
			t.Errorf("%s: %v, want ok=%v", addr, err, ok)
		}
	}
}

func TestClientKey(t *testing.T) {
	for _, c := range []struct{ xff, remote, want string }{
		{"203.0.113.9", "127.0.0.1:4000", "203.0.113.9"},
		{"2001:db8:1:2:aaaa::1", "127.0.0.1:4000", "2001:db8:1:2::"}, // a /64 is one visitor
		{"2001:db8:1:2:bbbb::7", "127.0.0.1:4000", "2001:db8:1:2::"},
		{"", "127.0.0.1:4000", "127.0.0.1"},
		{"not an ip", "127.0.0.1:4000", "127.0.0.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientKey(r); got != c.want {
			t.Errorf("%q from %s: %q, want %q", c.xff, c.remote, got, c.want)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	l := newLimiter(3, 5)
	l.now = func() time.Time { return now }
	for i := range 3 {
		if msg, _ := l.allow("a"); msg != "" {
			t.Fatalf("a's question %d refused: %s", i+1, msg)
		}
	}
	msg, retry := l.allow("a")
	if !strings.Contains(msg, "in the last hour") || retry != time.Hour {
		t.Errorf("a's 4th: %q, retry %v", msg, retry)
	}
	if msg, _ := l.allow("b"); msg != "" {
		t.Errorf("b is someone else: %s", msg)
	}
	now = now.Add(61 * time.Minute)
	if msg, _ := l.allow("a"); msg != "" {
		t.Errorf("an hour on, a may ask again: %s", msg)
	}
	// 5 answered today: a 3, b 1, a 1. The day is full for everyone.
	msg, retry = l.allow("c")
	if !strings.Contains(msg, "for today") || retry != 13*time.Hour-time.Minute {
		t.Errorf("past the daily cap: %q, retry %v", msg, retry)
	}
	if got := l.today(); got["answered"] != 5 || got["refused"] != 2 {
		t.Errorf("today: %v, want 5 answered and 2 refused", got)
	}
	now = time.Date(2026, 11, 23, 0, 0, 1, 0, time.UTC)
	if msg, _ := l.allow("c"); msg != "" {
		t.Errorf("a new UTC day: %s", msg)
	}
	if got := l.today(); got["answered"] != 1 || got["refused"] != 0 {
		t.Errorf("a new day's counts: %v", got)
	}
}

// The limiter's memory is bounded: past maxClients, clients with nothing in
// the last hour are dropped.
func TestLimiterSweeps(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	l := newLimiter(10, 1<<30)
	l.now = func() time.Time { return now }
	for i := range maxClients {
		l.allow(string(rune(i)))
	}
	now = now.Add(2 * time.Hour)
	l.allow("late")
	if len(l.hits) != 1 {
		t.Errorf("%d clients held, want 1", len(l.hits))
	}
}

func publicPost(t *testing.T, h http.Handler, xff, question string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(askRequest{Question: question})
	r := httptest.NewRequest("POST", "/api/ask", bytes.NewReader(b))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Tailscale-Funnel-Request", "?1")
	r.Header.Set("X-Forwarded-For", xff)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// End to end through the handler: Funnel's requests are answered, a visitor
// past the hourly limit gets 429 with Retry-After, another visitor is not
// affected, the log marks the public questions and never keeps the address,
// and the page carries the public footer.
func TestPublicInstance(t *testing.T) {
	s, logPath := testServer(t, "UNSUPPORTED: no")
	if err := s.makePublic(2, 100); err != nil {
		t.Fatal(err)
	}
	h := s.handler()
	for i := range 2 {
		if w := publicPost(t, h, "203.0.113.9", "read my pdf"); w.Code != 200 {
			t.Fatalf("question %d: %d %s", i+1, w.Code, w.Body)
		}
	}
	w := publicPost(t, h, "203.0.113.9", "read my pdf")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("the 3rd in an hour: %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if w := publicPost(t, h, "198.51.100.4", "read my pdf"); w.Code != 200 {
		t.Errorf("another visitor: %d", w.Code)
	}
	log, _ := os.ReadFile(logPath)
	if n := strings.Count(string(log), `"public":true`); n != 3 {
		t.Errorf("%d public log lines, want 3:\n%s", n, log)
	}
	if strings.Contains(string(log), "203.0.113") || strings.Contains(string(log), "198.51.100") {
		t.Errorf("the log kept an address:\n%s", log)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	pw := httptest.NewRecorder()
	h.ServeHTTP(pw, r)
	if !bytes.Contains(pw.Body.Bytes(), publicFooter) || bytes.Contains(pw.Body.Bytes(), lanFooter) {
		t.Errorf("the public page does not say it is public")
	}
	// A LAN address cannot reach it, even with Funnel's headers.
	b, _ := json.Marshal(askRequest{Question: "hi"})
	lr := httptest.NewRequest("POST", "/api/ask", bytes.NewReader(b))
	lr.RemoteAddr = "192.168.4.20:5555"
	lr.Header.Set("Tailscale-Funnel-Request", "?1")
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, lr)
	if lw.Code != http.StatusForbidden {
		t.Errorf("a LAN address on the public instance: %d", lw.Code)
	}
}

// The LAN page still says the LAN footer, so publicPage has text to replace.
func TestPublicPageReplacesTheFooter(t *testing.T) {
	if _, err := publicPage(pageHTML); err != nil {
		t.Fatal(err)
	}
	if _, err := publicPage([]byte("<footer>something else</footer>")); err == nil {
		t.Error("a page without the LAN footer was accepted")
	}
}

// The log moves to .1 past its cap, keeping one old generation.
func TestLogRotates(t *testing.T) {
	s, logPath := testServer(t, "UNSUPPORTED: no")
	s.logMax = 300
	h := s.handler()
	for range 4 {
		if w, _ := post(t, h, askRequest{Question: strings.Repeat("q", 100)}); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	cur, err1 := os.Stat(logPath)
	old, err2 := os.Stat(logPath + ".1")
	if err1 != nil || err2 != nil {
		t.Fatalf("%v %v", err1, err2)
	}
	if cur.Size() >= 2*s.logMax || old.Size() < s.logMax {
		t.Errorf("current %d bytes, old %d", cur.Size(), old.Size())
	}
}

// -log-shared: the public instance's owner reads its log as a member of
// the ssql-faq group, so the log is 0640 in a 0750 directory even under
// the unit's UMask=0077, and an existing 0600 log is opened up at start.
// Without it, both stay the owner's alone.
func TestLogShared(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dir := filepath.Join(t.TempDir(), "state")
	logPath := filepath.Join(dir, "q.jsonl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath+".1", []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := shareLog(logPath, true); err != nil {
		t.Fatal(err)
	}
	s, _ := testServer(t, "UNSUPPORTED: no")
	s.logPath, s.logPerm = logPath, 0o640
	if w, _ := post(t, s.handler(), askRequest{Question: "read my pdf"}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for path, want := range map[string]os.FileMode{dir: 0o750, logPath: 0o640, logPath + ".1": 0o640} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", path, fi.Mode().Perm(), err, want)
		}
	}
	if err := shareLog(logPath, false); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, logPath: 0o600} {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != want {
			t.Errorf("unshared %s: %v, want %v", path, fi.Mode().Perm(), want)
		}
	}
}
