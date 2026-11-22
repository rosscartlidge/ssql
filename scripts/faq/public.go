package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Public mode (DFC169 §11.4). Ross, 2026-11-22: open the FAQ through
// Tailscale Funnel, but safely. A public instance is a second server,
// started with -public on a loopback address, and the only thing Funnel
// points at; the LAN and tailnet instance keeps privateOnly unchanged.
//
// Loopback matters: tailscaled's Funnel proxy sets X-Forwarded-For to the
// public client's address and Tailscale-Funnel-Request to ?1, overwriting
// whatever the client sent (ipn/ipnlocal/serve.go: addProxyForwardedHeaders,
// addTailscaleIdentityHeaders). Those headers can be trusted only when
// nothing but tailscaled (and this machine) can connect, so -public refuses
// any listen address that is not loopback.

// publicOnly answers loopback connections only: tailscaled's proxy and
// this machine.
func publicOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "the public FAQ is reached through Tailscale Funnel only", http.StatusForbidden)
			return
		}
		securityHeaders(w)
		next.ServeHTTP(w, r)
	})
}

// loopbackListen reports whether a -listen address binds loopback only.
func loopbackListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("-public needs a loopback -listen address such as 127.0.0.1:8485, not %q: the forwarded client address is trustworthy only when tailscaled is the only one who can connect", addr)
}

// clientKey is who a question is counted against: the address Funnel
// forwarded (an IPv6 client by its /64, which one subscriber usually
// holds whole), else the connection's own address.
func clientKey(r *http.Request) string {
	if ip := net.ParseIP(r.Header.Get("X-Forwarded-For")); ip != nil {
		if ip.To4() == nil {
			ip = ip.Mask(net.CIDRMask(64, 128))
		}
		return ip.String()
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// limiter caps questions per client per hour and in total per UTC day.
// It holds only timestamps keyed by address, in memory, never logged.
type limiter struct {
	mu       sync.Mutex
	perHour  int
	perDay   int
	now      func() time.Time
	hits     map[string][]time.Time // each client's questions in the last hour
	day      string
	dayCount int // questions let through today (UTC)
	refused  int // questions turned away today: the sign of abuse
}

// maxClients bounds the limiter's memory: past it, stale clients are swept.
const maxClients = 10000

func newLimiter(perHour, perDay int) *limiter {
	return &limiter{perHour: perHour, perDay: perDay, now: time.Now, hits: map[string][]time.Time{}}
}

// allow counts a question from key if the limits let it run; otherwise it
// says why and how long to wait.
func (l *limiter) allow(key string) (msg string, retry time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	if d := now.Format(time.DateOnly); d != l.day {
		l.day, l.dayCount, l.refused = d, 0, 0
	}
	if l.dayCount >= l.perDay {
		l.refused++
		tomorrow := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
		return fmt.Sprintf("the FAQ has answered its %d questions for today; try again tomorrow (UTC)", l.perDay), tomorrow.Sub(now)
	}
	recent := l.recent(key, now)
	if len(recent) >= l.perHour {
		l.refused++
		l.hits[key] = recent
		return fmt.Sprintf("that's %d questions in the last hour, the most one visitor can ask; try again later", l.perHour), recent[0].Add(time.Hour).Sub(now)
	}
	if len(l.hits) >= maxClients {
		for k := range l.hits {
			if len(l.recent(k, now)) == 0 {
				delete(l.hits, k)
			}
		}
	}
	l.hits[key] = append(recent, now)
	l.dayCount++
	return "", 0
}

// today is the day's counts, for /api/health.
func (l *limiter) today() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.day != l.now().UTC().Format(time.DateOnly) {
		return map[string]any{"answered": 0, "refused": 0, "cap": l.perDay}
	}
	return map[string]any{"answered": l.dayCount, "refused": l.refused, "cap": l.perDay}
}

// recent is key's questions within the last hour.
func (l *limiter) recent(key string, now time.Time) []time.Time {
	ts := l.hits[key]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) >= time.Hour {
		i++
	}
	return ts[i:]
}

// The page's footer, as each mode says it.
var (
	lanFooter    = []byte("Runs on a laptop on this network.")
	publicFooter = []byte("This is a public FAQ, run on one person's laptop with a local model, so it may be slow or away, and each visitor can ask a limited number of questions an hour. Don't paste anything confidential.")
)

// publicPage is the page with the public footer; it fails loudly if the
// page no longer carries the text it replaces.
func publicPage(page []byte) ([]byte, error) {
	if !bytes.Contains(page, lanFooter) {
		return nil, fmt.Errorf("page.html no longer says %q; update publicPage", lanFooter)
	}
	return bytes.Replace(page, lanFooter, publicFooter, 1), nil
}
