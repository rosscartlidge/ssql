// docsearch is a hybrid (BM25 + embedding) search over the project's
// research docs, claude/ references, journals and user docs, built for
// Claude Code sessions to answer "have we written this down before?"
//
// Usage (from the repo root):
//
//	scripts/docsearch.sh 'catalog pruning negation'
//	scripts/docsearch.sh -k 12 -lexical 'range extraction leak'
//
// (own go.mod → invoke via the wrapper or `go -C scripts/docsearch run .`)
//
// Design (deliberately minimal for a ~2k-chunk corpus):
//   - Chunks are heading-level markdown sections; the corpus is re-chunked
//     on every query (<100ms) — only EMBEDDINGS are cached, keyed by chunk
//     content hash (.docsearch-cache.jsonl, gitignored), so edits re-embed
//     only what changed.
//   - BM25 for ranked keyword match; nomic-embed-text via local ollama for
//     concept match; the two rankings are fused with Reciprocal Rank
//     Fusion (parameter-free, more robust than score blending).
//   - If ollama is unreachable the tool degrades to BM25-only with a
//     warning — no hard dependency.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

var corpusGlobs = []string{
	"doc/research/*.md",
	"doc/*.md",
	"claude/*.md",
	"journal/*.md",
	"*.md", // README, CHANGELOG, CLAUDE.md
}

const (
	ollamaURL  = "http://localhost:11434/api/embeddings"
	embedModel = "nomic-embed-text"
	cacheFile  = ".docsearch-cache.jsonl"
	rrfK       = 60 // standard RRF constant
	maxChunk   = 220
)

type chunk struct {
	File    string
	Line    int
	Heading string
	Text    string
	Hash    string
}

func main() {
	k := flag.Int("k", 8, "number of results")
	lexical := flag.Bool("lexical", false, "BM25 only (skip embeddings)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: docsearch [-k N] [-lexical] QUERY...")
		os.Exit(2)
	}
	query := strings.Join(flag.Args(), " ")

	root := repoRoot()
	chunks := loadChunks(root)
	if len(chunks) == 0 {
		fmt.Fprintln(os.Stderr, "no chunks found — run from the repo (or a subdir of it)")
		os.Exit(1)
	}

	bm25Rank := bm25(chunks, query)

	var fused []scored
	if *lexical {
		fused = bm25Rank
	} else {
		semRank, err := semantic(root, chunks, query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "note: embeddings unavailable (%v) — BM25 only\n", err)
			fused = bm25Rank
		} else {
			fused = rrf(bm25Rank, semRank)
		}
	}

	for i, s := range fused {
		if i >= *k {
			break
		}
		c := chunks[s.idx]
		fmt.Printf("%s:%d  %s\n    %s\n", c.File, c.Line, c.Heading, snippet(c.Text, query))
	}
}

func repoRoot() string {
	dir, _ := os.Getwd()
	for d := dir; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "doc", "research")); err == nil {
				return d
			}
		}
	}
	return dir
}

// ---- chunking ----

func loadChunks(root string) []chunk {
	var out []chunk
	seen := map[string]bool{}
	for _, g := range corpusGlobs {
		files, _ := filepath.Glob(filepath.Join(root, g))
		for _, f := range files {
			if seen[f] {
				continue
			}
			seen[f] = true
			rel, _ := filepath.Rel(root, f)
			out = append(out, chunkFile(f, rel)...)
		}
	}
	return out
}

func chunkFile(path, rel string) []chunk {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var chunks []chunk
	var cur []string
	curHead := rel
	curLine := 1
	flush := func() {
		text := strings.TrimSpace(strings.Join(cur, "\n"))
		if text == "" {
			return
		}
		h := sha256.Sum256([]byte(rel + "\x00" + text))
		chunks = append(chunks, chunk{
			File: rel, Line: curLine, Heading: curHead,
			Text: text, Hash: hex.EncodeToString(h[:16]),
		})
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		t := sc.Text()
		isHead := strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "### ")
		if isHead || len(cur) >= maxChunk {
			flush()
			cur = cur[:0]
			curLine = line
			if isHead {
				curHead = strings.TrimLeft(t, "# ")
			}
		}
		cur = append(cur, t)
	}
	flush()
	return chunks
}

// ---- tokenization ----

// tokenize lowercases and splits on non-alphanumerics, additionally
// splitting code identifiers (exprToGo → expr, to, go; MADV_DONTDUMP →
// madv, dontdump) so queries match either form.
func tokenize(s string) []string {
	var toks []string
	var cur []rune
	emit := func() {
		if len(cur) > 1 { // drop single letters
			toks = append(toks, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	prevLower := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			if unicode.IsUpper(r) && prevLower {
				emit() // camelCase boundary
			}
			cur = append(cur, r)
			prevLower = unicode.IsLower(r)
		case unicode.IsDigit(r):
			cur = append(cur, r)
			prevLower = false
		default:
			emit()
			prevLower = false
		}
	}
	emit()
	return toks
}

// ---- BM25 ----

type scored struct {
	idx   int
	score float64
}

func bm25(chunks []chunk, query string) []scored {
	const k1, b = 1.2, 0.75
	// term frequencies per chunk + document frequencies
	tfs := make([]map[string]int, len(chunks))
	df := map[string]int{}
	totalLen := 0
	lens := make([]int, len(chunks))
	for i, c := range chunks {
		tf := map[string]int{}
		toks := tokenize(c.Heading + " " + c.Text)
		for _, t := range toks {
			tf[t]++
		}
		for t := range tf {
			df[t]++
		}
		tfs[i] = tf
		lens[i] = len(toks)
		totalLen += len(toks)
	}
	avgLen := float64(totalLen) / float64(len(chunks))

	qTerms := tokenize(query)
	n := float64(len(chunks))
	var out []scored
	for i := range chunks {
		var s float64
		for _, t := range qTerms {
			tf := float64(tfs[i][t])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			s += idf * (tf * (k1 + 1)) / (tf + k1*(1-b+b*float64(lens[i])/avgLen))
		}
		if s > 0 {
			out = append(out, scored{i, s})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out
}

// ---- embeddings via ollama ----

type cacheEntry struct {
	Hash string    `json:"hash"`
	Vec  []float32 `json:"vec"`
}

func semantic(root string, chunks []chunk, query string) ([]scored, error) {
	cachePath := filepath.Join(root, cacheFile)
	cache := map[string][]float32{}
	if f, err := os.Open(cachePath); err == nil {
		dec := json.NewDecoder(f)
		for {
			var e cacheEntry
			if dec.Decode(&e) != nil {
				break
			}
			cache[e.Hash] = e.Vec
		}
		f.Close()
	}

	// Embed missing chunks (nomic wants task prefixes).
	var missing []int
	for i, c := range chunks {
		if _, ok := cache[c.Hash]; !ok {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "embedding %d new/changed chunks…\n", len(missing))
		f, err := os.OpenFile(cachePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		enc := json.NewEncoder(f)
		skipped := 0
		for _, i := range missing {
			// Truncate to stay inside the embedding model's context —
			// retrieval only needs a section's opening; oversized inputs
			// made ollama return empty embeddings.
			text := chunks[i].Heading + "\n" + chunks[i].Text
			if len(text) > 6000 {
				text = text[:6000]
			}
			vec, err := embed("search_document: " + text)
			if err != nil {
				// A bad chunk shouldn't kill the semantic layer — skip it
				// (it simply won't rank semantically this run).
				skipped++
				continue
			}
			cache[chunks[i].Hash] = vec
			_ = enc.Encode(cacheEntry{chunks[i].Hash, vec})
		}
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "note: %d chunks failed to embed (skipped)\n", skipped)
		}
		f.Close()
	}

	qv, err := embed("search_query: " + query)
	if err != nil {
		return nil, err
	}

	var out []scored
	for i, c := range chunks {
		if v, ok := cache[c.Hash]; ok {
			out = append(out, scored{i, cosine(qv, v)})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out, nil
}

func embed(text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]string{"model": embedModel, "prompt": text})
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(ollamaURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if len(r.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding from ollama (model %s pulled?)", embedModel)
	}
	return r.Embedding, nil
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// ---- fusion + output ----

// rrf fuses rankings by Reciprocal Rank Fusion: each list contributes
// 1/(rrfK + rank) per item — parameter-free and robust to incomparable
// score scales.
func rrf(lists ...[]scored) []scored {
	acc := map[int]float64{}
	for _, list := range lists {
		for rank, s := range list {
			acc[s.idx] += 1.0 / float64(rrfK+rank+1)
		}
	}
	out := make([]scored, 0, len(acc))
	for idx, sc := range acc {
		out = append(out, scored{idx, sc})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out
}

// snippet returns the most query-relevant line of the chunk, trimmed.
func snippet(text, query string) string {
	qTerms := tokenize(query)
	best, bestHits := "", -1
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(strings.TrimLeft(line, "#-*> "))
		if l == "" {
			continue
		}
		toks := tokenize(l)
		set := map[string]bool{}
		for _, t := range toks {
			set[t] = true
		}
		hits := 0
		for _, q := range qTerms {
			if set[q] {
				hits++
			}
		}
		if hits > bestHits {
			bestHits, best = hits, l
		}
	}
	if len(best) > 160 {
		best = best[:157] + "…"
	}
	return best
}
