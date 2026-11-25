package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestOllamaFilter: the calls the FAQ makes pass, for its models; every
// other call, model, key, option and oversized context is refused before
// Ollama sees it.
func TestOllamaFilter(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer upstream.Close()
	f, err := newOllamaFilter(upstream.URL, []string{"gemma4:26b"}, "nomic-embed-text", 32768)
	if err != nil {
		t.Fatal(err)
	}
	filter := httptest.NewServer(f)
	defer filter.Close()

	chat := `{"model":"gemma4:26b","messages":[{"role":"user","content":"hi"}],"stream":false,"think":false,` +
		`"options":{"num_ctx":32768,"temperature":0,"top_p":1,"seed":42},"keep_alive":"30m"}`
	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"chat", "POST", "/api/chat", chat, 200},
		{"warm-up", "POST", "/api/chat", `{"model":"gemma4:26b","messages":[],"keep_alive":"30m"}`, 200},
		{"embeddings", "POST", "/api/embeddings", `{"model":"nomic-embed-text","prompt":"join"}`, 200},
		{"pull", "POST", "/api/pull", `{"model":"gemma4:26b"}`, 403},
		{"delete", "DELETE", "/api/delete", `{"model":"gemma4:26b"}`, 403},
		{"create", "POST", "/api/create", `{"model":"gemma4:26b","from":"x"}`, 403},
		{"tags", "GET", "/api/tags", ``, 403},
		{"generate", "POST", "/api/generate", `{"model":"gemma4:26b","prompt":"x"}`, 403},
		{"chat by GET", "GET", "/api/chat", ``, 403},
		{"other chat model", "POST", "/api/chat", `{"model":"llama3:70b","messages":[]}`, 403},
		{"chat with the embedding model", "POST", "/api/chat", `{"model":"nomic-embed-text","messages":[]}`, 403},
		{"embeddings with a chat model", "POST", "/api/embeddings", `{"model":"gemma4:26b","prompt":"x"}`, 403},
		{"no model", "POST", "/api/chat", `{"messages":[]}`, 403},
		{"a key the FAQ never sends", "POST", "/api/chat", `{"model":"gemma4:26b","messages":[],"tools":[]}`, 403},
		{"an option the FAQ never sends", "POST", "/api/chat", `{"model":"gemma4:26b","messages":[],"options":{"num_gpu":0}}`, 403},
		{"a larger context", "POST", "/api/chat", `{"model":"gemma4:26b","messages":[],"options":{"num_ctx":262144}}`, 403},
		{"not JSON", "POST", "/api/chat", `model=gemma4:26b`, 403},
		{"oversized", "POST", "/api/chat", `{"model":"gemma4:26b","messages":[{"role":"user","content":"` + strings.Repeat("x", maxFilteredBody) + `"}]}`, 413},
	}
	for _, c := range cases {
		seen = nil
		req, _ := http.NewRequest(c.method, filter.URL+c.path, strings.NewReader(c.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", c.name, resp.StatusCode, c.want)
		}
		if passed := len(seen) > 0; passed != (c.want == 200) {
			t.Errorf("%s: reached Ollama %v (%v), want %v", c.name, passed, seen, c.want == 200)
		}
	}
}

// TestOllamaThroughTheFilter: the FAQ's own client (the warm-up and a chat)
// passes the filter, so the filter's list is the calls the code makes.
func TestOllamaThroughTheFilter(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer upstream.Close()
	f, err := newOllamaFilter(upstream.URL, []string{"m"}, "e", 32768)
	if err != nil {
		t.Fatal(err)
	}
	filter := httptest.NewServer(f)
	defer filter.Close()
	o := ollama{host: filter.URL, model: "m", ctx: 32768}
	if err := o.warm(); err != nil {
		t.Errorf("warm-up through the filter: %v", err)
	}
	if reply, _, err := o.chat([]message{{"user", "hi"}}); err != nil || reply != "ok" {
		t.Errorf("chat through the filter: %q, %v", reply, err)
	}
	o.model = "other"
	if _, _, err := o.chat(nil); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("a model the filter does not name: %v, want the filter's refusal", err)
	}
}

// TestFilterUnitPassesThePublicModels: the filter's unit passes the models
// the public unit asks for, over the address and with the context the
// public unit's FAQ uses, or every question would be refused.
func TestFilterUnitPassesThePublicModels(t *testing.T) {
	flagOf := func(file, name string) string {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		// The unit's settings, not its comments.
		b = regexp.MustCompile(`(?m)^#.*$`).ReplaceAll(b, nil)
		m := regexp.MustCompile(`(?:^|[\s=])` + regexp.QuoteMeta(name) + `[ =](\S+)`).FindSubmatch(b)
		if m == nil {
			return ""
		}
		return string(m[1])
	}
	public, filter := "ssql-faq-public.service", "ssql-faq-ollama-filter.service"
	if p, f := flagOf(public, "-models"), flagOf(filter, "-models"); p == "" || p != f {
		t.Errorf("the public unit asks for -models %q, the filter passes %q", p, f)
	}
	if p, f := flagOf(public, "-ctx"), flagOf(filter, "-ctx"); p != f {
		t.Errorf("the public unit's -ctx %q, the filter's %q", p, f)
	}
	host := flagOf(public, "OLLAMA_HOST")
	addr := flagOf(filter, "-ollama-filter")
	if addr == "" || host != "http://"+addr {
		t.Errorf("the public unit's OLLAMA_HOST %q is not the filter's address %q", host, addr)
	}
	nft, err := os.ReadFile("ssql-faq-public.nft")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(addr)
	if !strings.Contains(string(nft), "tcp dport "+port+" accept") {
		t.Errorf("ssql-faq-public.nft does not let the service reach the filter's port %s", port)
	}
}
