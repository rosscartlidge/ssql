package main

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestPageAsksTheLinksQuestion opens the page with ?q= in headless Chrome,
// against the real handler and a fake model, and reads the DOM after the
// answer: the link's question was asked once and answered. (A DOM dump
// does not show a textarea's value, so the emptied box is not checked.)
func TestPageAsksTheLinksQuestion(t *testing.T) {
	chrome := ""
	for _, c := range []string{"google-chrome", "chromium", "google-chrome-stable"} {
		if p, err := exec.LookPath(c); err == nil {
			chrome = p
			break
		}
	}
	if chrome == "" {
		t.Skip("no chrome/chromium")
	}
	const pipeline = "ssql from employees.csv | ssql where -if dept eq Engineering | ssql count"
	s, _ := testServer(t, "Count them with linkreply:\n```bash\n"+pipeline+"\n```\n")
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	const question = "how many engineers & <who>?"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=30000",
		"--dump-dom", srv.URL+"/?q="+url.QueryEscape(question)).Output()
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	dom := string(out)
	if !strings.Contains(dom, "linkreply") {
		t.Fatalf("the answer is not on the page:\n%s", dom)
	}
	if hint := srv.URL + "/api/ask?format=sh' &gt; answer.sh"; !strings.Contains(dom, hint) {
		t.Errorf("the terminal hint does not name this server (%s)", hint)
	}
	if n := strings.Count(dom, `<div class="q">how many engineers &amp; &lt;who&gt;?</div>`); n != 1 {
		t.Errorf("the question is shown %d times, want once", n)
	}
}
