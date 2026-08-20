package main

// Integration tests for serve Phase 2a (the HTTP protocol): start the
// real binary with -listen-http on an ephemeral port and drive the
// REST endpoints. The exec lane through /api/execute must match the
// same pipeline run directly — the transport must not change results.

import (
	"bufio"
	"bytes"
	"os"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startServeHTTPProcess launches `ssql serve -listen-http 127.0.0.1:0`
// and returns the resolved address. The process is killed on cleanup.
func startServeHTTPProcess(t *testing.T, extraArgs ...string) string {
	t.Helper()
	bin := corpusBin(t)
	data := corpusData(t)
	args := append([]string{"serve", "-listen-http", "127.0.0.1:0", "-dir", data}, extraArgs...)
	cmd := exec.Command(bin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	sc := bufio.NewScanner(stderr)
	addrCh := make(chan string, 1)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if i := strings.Index(line, "listening on "); i >= 0 {
				addrCh <- strings.Fields(line[i+len("listening on "):])[0]
				return
			}
		}
		addrCh <- ""
	}()
	select {
	case addr := <-addrCh:
		if addr == "" {
			t.Fatal("serve exited without announcing an address")
		}
		return addr
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for serve to announce its address")
		return ""
	}
}

func postJSON(t *testing.T, url string, body any, headers map[string]string) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(out)
}

func TestServeHTTPEndpoints(t *testing.T) {
	addr := startServeHTTPProcess(t)
	base := "http://" + addr

	t.Run("health", func(t *testing.T) {
		resp, err := http.Get(base + "/api/health")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var h struct{ Status, Version string }
		json.NewDecoder(resp.Body).Decode(&h)
		if h.Status != "ok" || h.Version == "" {
			t.Errorf("health = %+v", h)
		}
	})

	t.Run("files", func(t *testing.T) {
		resp, err := http.Get(base + "/api/files")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(b), "employees.csv") {
			t.Errorf("files missing employees.csv: %s", b)
		}
	})

	t.Run("execute-matches-direct", func(t *testing.T) {
		pipeline := "ssql from employees.csv | ssql group-by dept -count n | ssql sort dept"
		resp, got := postJSON(t, base+"/api/execute", map[string]string{"pipeline": pipeline}, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, got)
		}
		if resp.Trailer.Get("X-Ssql-Exit-Code") != "0" {
			t.Errorf("exit trailer = %q", resp.Trailer.Get("X-Ssql-Exit-Code"))
		}
		// Direct exec lane — the oracle.
		bin, data := corpusBin(t), corpusData(t)
		cmdline := strings.ReplaceAll(pipeline, "ssql ", bin+" ")
		direct, err := execBashIn(data, cmdline)
		if err != nil {
			t.Fatalf("direct lane: %v", err)
		}
		if got != direct {
			t.Errorf("transport changed results\n--- http:\n%s--- direct:\n%s", got, direct)
		}
	})

	t.Run("execute-pipeline-error", func(t *testing.T) {
		resp, body := postJSON(t, base+"/api/execute", map[string]string{"pipeline": "ssql from nope.csv | ssql to jsonl"}, nil)
		if resp.StatusCode != 422 || !strings.Contains(body, "no such file") {
			t.Errorf("status %d body %s", resp.StatusCode, body)
		}
	})

	t.Run("execute-rejects-shellisms", func(t *testing.T) {
		for _, p := range []string{
			"ssql from employees.csv > out.txt",
			"ssql join <(ssql from b.csv)",
			"ssql from employees.csv; rm x",
			"ssql from $(hostname).csv",
		} {
			resp, body := postJSON(t, base+"/api/execute", map[string]string{"pipeline": p}, nil)
			if resp.StatusCode != 400 {
				t.Errorf("%q: status %d body %s", p, resp.StatusCode, body)
			}
		}
	})

	t.Run("execute-unknown-field-fails-in-trailer", func(t *testing.T) {
		// A field typo errors AFTER the _schema header is already on
		// the wire, so it is a mid-stream failure: HTTP 200, but the
		// X-Ssql-Exit-Code/X-Ssql-Error trailers carry the verdict.
		resp, _ := postJSON(t, base+"/api/execute",
			map[string]string{"pipeline": "ssql from employees.csv | ssql where -if nosuch eq x"}, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if resp.Trailer.Get("X-Ssql-Exit-Code") == "0" {
			t.Error("exit trailer claims success for a failed pipeline")
		}
		if !strings.Contains(resp.Trailer.Get("X-Ssql-Error"), "nosuch") {
			t.Errorf("error trailer = %q", resp.Trailer.Get("X-Ssql-Error"))
		}
	})

	t.Run("cursor-complete", func(t *testing.T) {
		resp, body := postJSON(t, base+"/api/cursor",
			map[string]any{"argv": []string{"-complete", "1", "where", ""}}, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		var out struct {
			Stdout string `json:"stdout"`
			Code   int    `json:"code"`
		}
		json.Unmarshal([]byte(body), &out)
		if out.Code != 0 || !strings.Contains(out.Stdout, "where") {
			t.Errorf("cursor complete = %+v", out)
		}
	})

	t.Run("cursor-rejects-non-protocol-argv", func(t *testing.T) {
		resp, _ := postJSON(t, base+"/api/cursor",
			map[string]any{"argv": []string{"from", "employees.csv"}}, nil)
		if resp.StatusCode != 400 {
			t.Errorf("status %d", resp.StatusCode)
		}
	})
}

func TestServeHTTPTokenAuth(t *testing.T) {
	addr := startServeHTTPProcess(t, "-token", "sekrit")
	base := "http://" + addr

	resp, err := http.Get(base + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("no token: status %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest("GET", base+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer sekrit")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("with token: status %d, want 200", resp2.StatusCode)
	}
}

func TestServeHTTPNonLoopbackRequiresToken(t *testing.T) {
	bin := corpusBin(t)
	out, err := exec.Command(bin, "serve", "-listen-http", "0.0.0.0:0").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "refusing to start") {
		t.Errorf("want startup refusal, got err=%v out=%s", err, out)
	}
}

// execBashIn runs a shell command in dir and returns stdout.
func execBashIn(dir, cmdline string) (string, error) {
	cmd := exec.Command("bash", "-c", cmdline)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, errb.String())
	}
	return out.String(), nil
}

func TestServeExplorePage(t *testing.T) {
	addr := startServeHTTPProcess(t)
	base := "http://" + addr

	t.Run("index-lists-files", func(t *testing.T) {
		resp, err := http.Get(base + "/")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(b), "employees.csv") || !strings.Contains(string(b), "/explore?file=") {
			t.Errorf("index missing file links: %.300s", b)
		}
	})

	t.Run("explore-file-is-the-artifact", func(t *testing.T) {
		resp, err := http.Get(base + "/explore?file=employees.csv")
		if err != nil {
			t.Fatal(err)
		}
		served, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		// The served workspace must be byte-identical to a locally
		// generated `to explore` artifact — serve adds no second
		// implementation of anything.
		bin, data := corpusBin(t), corpusData(t)
		local := t.TempDir() + "/local.html"
		if _, err := execBashIn(data, fmt.Sprintf(
			"%s from employees.csv | %s to explore -title employees.csv %s", bin, bin, local)); err != nil {
			t.Fatalf("local artifact: %v", err)
		}
		want, err := exec.Command("cat", local).Output()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(served, want) {
			t.Errorf("served page differs from local artifact (served %d bytes, local %d bytes)",
				len(served), len(want))
		}
	})

	t.Run("explore-pipeline-head-runs-server-side", func(t *testing.T) {
		resp, err := http.Get(base + "/explore?pipeline=" +
			"ssql%20from%20employees.csv%20%7C%20ssql%20where%20-if%20dept%20eq%20Engineering")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d: %.300s", resp.StatusCode, b)
		}
		s := string(b)
		if !strings.Contains(s, "Engineering") || strings.Contains(s, `"Sales"`) {
			t.Error("head pipeline did not filter server-side")
		}
	})

	t.Run("explore-rejects-traversal", func(t *testing.T) {
		resp, body := func() (*http.Response, string) {
			resp, err := http.Get(base + "/explore?file=..%2F..%2Fetc%2Fpasswd")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return resp, string(b)
		}()
		if resp.StatusCode != 404 {
			t.Errorf("status %d body %s", resp.StatusCode, body)
		}
	})
}

func TestServeExecuteBufferedMode(t *testing.T) {
	addr := startServeHTTPProcess(t)
	base := "http://" + addr

	resp, body := postJSON(t, base+"/api/execute?mode=buffered",
		map[string]string{"pipeline": "ssql from employees.csv | ssql where -if nosuch eq x"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Output, Stderr string
		Code           int
	}
	json.Unmarshal([]byte(body), &out)
	if out.Code == 0 || !strings.Contains(out.Stderr, "nosuch") {
		t.Errorf("buffered failure envelope = %+v", out)
	}

	_, body2 := postJSON(t, base+"/api/execute?mode=buffered",
		map[string]string{"pipeline": "ssql from employees.csv | ssql group-by dept -count n | ssql sort dept"}, nil)
	var ok2 struct {
		Output string
		Code   int
	}
	json.Unmarshal([]byte(body2), &ok2)
	if ok2.Code != 0 || !strings.Contains(ok2.Output, `"dept"`) {
		t.Errorf("buffered success envelope = %+v", ok2)
	}
}

// TestServeCutPointEquivalence is the DFC108 seam gate: for every cut
// of a pipeline, running the head via /api/execute, landing the result
// as data.jsonl (exactly what the served workspace does), and running
// the tail locally over it must equal running the whole pipeline in
// one place. The wire (schema-headed JSONL) must not change results.
func TestServeCutPointEquivalence(t *testing.T) {
	addr := startServeHTTPProcess(t)
	base := "http://" + addr
	bin, data := corpusBin(t), corpusData(t)

	pipelines := [][]string{
		{"ssql from employees.csv", "ssql where -if age gt 30", "ssql group-by dept -count n -sum salary total", "ssql sort dept"},
		{"ssql from employees.csv", "ssql sort salary -desc", "ssql limit 7", "ssql include name salary dept"},
		{"ssql from employees.csv", "ssql update -set-expr pay 'salary / 12'", "ssql where -if-expr 'pay > 4000'", "ssql sort name"},
	}
	for pi, stages := range pipelines {
		full := strings.Join(stages, " | ")
		want, err := execBashIn(data, strings.ReplaceAll(full, "ssql ", bin+" "))
		if err != nil {
			t.Fatalf("pipeline %d full run: %v", pi, err)
		}
		for cut := 1; cut < len(stages); cut++ {
			t.Run(fmt.Sprintf("p%d-cut%d", pi, cut), func(t *testing.T) {
				head := strings.Join(stages[:cut], " | ")
				resp, body := postJSON(t, base+"/api/execute?mode=buffered",
					map[string]string{"pipeline": head}, nil)
				if resp.StatusCode != 200 {
					t.Fatalf("head status %d: %s", resp.StatusCode, body)
				}
				var env struct {
					Output string
					Code   int
				}
				json.Unmarshal([]byte(body), &env)
				if env.Code != 0 {
					t.Fatalf("head failed: %s", body)
				}
				scratch := t.TempDir()
				if err := writeFile(scratch+"/data.jsonl", env.Output); err != nil {
					t.Fatal(err)
				}
				tail := bin + " from data.jsonl"
				for _, st := range stages[cut:] {
					tail += " | " + strings.Replace(st, "ssql ", bin+" ", 1)
				}
				got, err := execBashIn(scratch, tail)
				if err != nil {
					t.Fatalf("tail run: %v", err)
				}
				if normalizeWireNumerics(got) != normalizeWireNumerics(want) {
					t.Errorf("cut at %d changed results\n--- split:\n%s--- whole:\n%s", cut, got, want)
				}
			})
		}
	}
}

// normalizeWireNumerics folds the _schema header's int/float
// distinction — the one thing a JSON wire hop cannot preserve (a sum
// of ints is float64 in-process but its whole values re-infer as int
// after serialization; values themselves compare exactly). The same
// normalization the equivalence harness applies. Everything else —
// field order, field names, every data byte — must survive the seam
// untouched.
func normalizeWireNumerics(out string) string {
	lines := strings.SplitN(out, "\n", 2)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], `{"_schema"`) {
		return out
	}
	hdr := strings.ReplaceAll(lines[0], `"float"`, `"int"`)
	if len(lines) == 2 {
		return hdr + "\n" + lines[1]
	}
	return hdr
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestServeCursorEnv(t *testing.T) {
	addr := startServeHTTPProcess(t)
	base := "http://" + addr

	// Value sampling: AUTOCLI_CACHE_FILE is the one allowed env var —
	// completing a value slot with it set returns real data values.
	resp, body := postJSON(t, base+"/api/cursor", map[string]any{
		"argv": []string{"-complete", "5", "where", "-if", "dept", "eq", ""},
		"env":  map[string]string{"AUTOCLI_CACHE_FILE": "employees.csv"},
	}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Stdout string
		Code   int
	}
	json.Unmarshal([]byte(body), &out)
	if out.Code != 0 || !strings.Contains(out.Stdout, "Engineering") {
		t.Errorf("value completion = %+v", out)
	}

	// Any other env var is rejected loudly.
	resp2, body2 := postJSON(t, base+"/api/cursor", map[string]any{
		"argv": []string{"-complete", "1", "where", ""},
		"env":  map[string]string{"PATH": "/tmp/evil"},
	}, nil)
	if resp2.StatusCode != 400 || !strings.Contains(body2, "not allowed") {
		t.Errorf("env allowlist: status %d body %s", resp2.StatusCode, body2)
	}
}

func TestServeSchemaFields(t *testing.T) {
	addr := startServeHTTPProcess(t)
	base := "http://" + addr

	resp, body := postJSON(t, base+"/api/schema-fields",
		map[string]string{"pipeline": "ssql from employees.csv | ssql group-by dept -count n"}, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"dept"`) || !strings.Contains(body, `"n"`) ||
		strings.Contains(body, `"salary"`) {
		t.Errorf("status %d body %s", resp.StatusCode, body)
	}

	resp2, _ := postJSON(t, base+"/api/schema-fields",
		map[string]string{"pipeline": "ssql from nope.csv"}, nil)
	if resp2.StatusCode != 422 {
		t.Errorf("missing file: status %d", resp2.StatusCode)
	}

	resp3, _ := postJSON(t, base+"/api/schema-fields",
		map[string]string{"pipeline": "ssql from a.csv > x"}, nil)
	if resp3.StatusCode != 400 {
		t.Errorf("shellism: status %d", resp3.StatusCode)
	}
}
