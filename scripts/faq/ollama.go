package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// message is one turn of an Ollama /api/chat conversation.
type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollama talks to a local Ollama server. Deterministic sampling (temperature
// 0, a fixed seed) and no thinking, as DFC043 Appendix E ran the models, so
// a run is repeatable and two models are compared on the same footing.
type ollama struct {
	host  string
	model string
	ctx   int
}

type chatStats struct {
	PromptTokens int
	GenTokens    int
	Wall         time.Duration
}

func (o ollama) chat(msgs []message) (string, chatStats, error) {
	body, _ := json.Marshal(map[string]any{
		"model":    o.model,
		"messages": msgs,
		"stream":   false,
		"think":    false,
		"options": map[string]any{
			"num_ctx":     o.ctx,
			"temperature": 0,
			"top_p":       1,
			"seed":        42,
		},
		"keep_alive": "30m",
	})
	start := time.Now()
	resp, err := http.Post(o.host+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", chatStats{}, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Message         message `json:"message"`
		PromptEvalCount int     `json:"prompt_eval_count"`
		EvalCount       int     `json:"eval_count"`
		Error           string  `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", chatStats{}, fmt.Errorf("ollama: decoding the reply: %w", err)
	}
	if out.Error != "" {
		return "", chatStats{}, fmt.Errorf("ollama: %s", out.Error)
	}
	return out.Message.Content, chatStats{out.PromptEvalCount, out.EvalCount, time.Since(start)}, nil
}
