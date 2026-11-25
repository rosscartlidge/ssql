package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"
)

// ollamaFilter stands between the public FAQ and Ollama (DFC171 §5).
// Ollama's API has no authentication, and besides chat it pulls, creates,
// copies and deletes models. The public instance needs two calls, so the
// filter passes those two for the models it names and refuses the rest:
//
//	POST /api/chat        a chat model; model, messages, stream, think,
//	                      options (num_ctx up to maxCtx, temperature,
//	                      top_p, seed) and keep_alive
//	POST /api/embeddings  the embedding model (docsearch); model, prompt
//
// It is a separate process under another user. The public service's user
// may connect to it but not to Ollama (the nftables rule that
// install-public.sh loads), so code that escapes the jail and runs as that
// user gets these two calls and nothing else.
type ollamaFilter struct {
	upstream *url.URL
	chat     []string // chat models
	embed    string   // the embedding model
	maxCtx   int      // the largest num_ctx a chat may ask for
	proxy    *httputil.ReverseProxy
}

// maxFilteredBody is far above a FAQ turn (a 32 k context of docs is about
// 130 KB of JSON) and far below what would hurt.
const maxFilteredBody = 2 << 20

var (
	chatKeys    = []string{"model", "messages", "stream", "think", "options", "keep_alive"}
	chatOptions = []string{"num_ctx", "temperature", "top_p", "seed"}
	embedKeys   = []string{"model", "prompt"}
)

func newOllamaFilter(upstream string, chat []string, embed string, maxCtx int) (*ollamaFilter, error) {
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, fmt.Errorf("the Ollama server %q: want http://HOST:PORT", upstream)
	}
	if len(chat) == 0 || embed == "" {
		return nil, errors.New("the filter needs the chat models and the embedding model it passes")
	}
	f := &ollamaFilter{upstream: u, chat: chat, embed: embed, maxCtx: maxCtx}
	f.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.Out.Host = u.Host
		},
		Transport: &http.Transport{ResponseHeaderTimeout: 180 * time.Second},
	}
	return f, nil
}

func (f *ollamaFilter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var keys, models []string
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/chat":
		keys, models = chatKeys, f.chat
	case r.Method == http.MethodPost && r.URL.Path == "/api/embeddings":
		keys, models = embedKeys, []string{f.embed}
	default:
		http.Error(w, fmt.Sprintf("refused: %s %s is not one of the calls the FAQ makes", r.Method, r.URL.Path), http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFilteredBody))
	if err != nil {
		http.Error(w, "refused: the body is over "+fmt.Sprint(maxFilteredBody)+" bytes", http.StatusRequestEntityTooLarge)
		return
	}
	if err := f.check(body, keys, models); err != nil {
		http.Error(w, "refused: "+err.Error(), http.StatusForbidden)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.URL.RawQuery = ""
	f.proxy.ServeHTTP(w, r)
}

// check is the body's half of the filter: only the keys the FAQ sends, a
// model the filter names, and no context larger than the FAQ's own.
func (f *ollamaFilter) check(body []byte, keys, models []string) error {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("the body is not a JSON object: %v", err)
	}
	for k := range req {
		if !slices.Contains(keys, k) {
			return fmt.Errorf("the key %q (allowed: %s)", k, strings.Join(keys, ", "))
		}
	}
	var model string
	if err := json.Unmarshal(req["model"], &model); err != nil || !slices.Contains(models, model) {
		return fmt.Errorf("the model %s (allowed: %s)", req["model"], strings.Join(models, ", "))
	}
	if raw, ok := req["options"]; ok {
		var opts map[string]json.RawMessage
		if err := json.Unmarshal(raw, &opts); err != nil {
			return fmt.Errorf("options is not an object")
		}
		for k := range opts {
			if !slices.Contains(chatOptions, k) {
				return fmt.Errorf("the option %q (allowed: %s)", k, strings.Join(chatOptions, ", "))
			}
		}
		if raw, ok := opts["num_ctx"]; ok {
			var n int
			if err := json.Unmarshal(raw, &n); err != nil || n < 1 || n > f.maxCtx {
				return fmt.Errorf("num_ctx %s (at most %d)", raw, f.maxCtx)
			}
		}
	}
	return nil
}
