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
// does not show a textarea's value, so the boxes are judged by what the
// server answered.)
func TestPageAsksTheLinksQuestion(t *testing.T) {
	const pipeline = "ssql from employees.csv | ssql where -if dept eq Engineering | ssql count"
	srv := pageServer(t, "Count them with linkreply:\n```bash\n"+pipeline+"\n```\n")
	dom := dumpPage(t, srv.URL+"/?q="+url.QueryEscape("how many engineers & <who>?"))
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

// TestPageSendsTheLinksColumns: &columns= reaches the server as the
// visitor's columns, so the answer is checked against them.
func TestPageSendsTheLinksColumns(t *testing.T) {
	const pipeline = "ssql from data.csv | ssql group-by customer -sum amount total | ssql to table"
	srv := pageServer(t, "Sum them with colsreply:\n```bash\n"+pipeline+"\n```\n")
	dom := dumpPage(t, srv.URL+"/?q="+url.QueryEscape("total amount per customer")+"&columns="+url.QueryEscape("order_id,customer,amount"))
	if !strings.Contains(dom, "colsreply") {
		t.Fatalf("the answer is not on the page:\n%s", dom)
	}
	if !strings.Contains(dom, `<span class="badge verified">checked against your columns`) {
		t.Errorf("the answer was not checked against the link's columns")
	}
}

// pageServer is the real handler over a fake model.
func pageServer(t *testing.T, replies ...string) *httptest.Server {
	s, _ := testServer(t, replies...)
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return srv
}

// dumpPage opens a page in headless Chrome and returns its DOM once the
// page has settled, its fetches included.
func dumpPage(t *testing.T, target string) string {
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=30000", "--dump-dom", target).Output()
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	return string(out)
}
