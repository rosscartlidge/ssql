package main

import "testing"

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"exprToGo", []string{"expr", "to", "go"}},
		{"MADV_DONTDUMP", []string{"madv", "dontdump"}},
		{"catalog-pruning, range!", []string{"catalog", "pruning", "range"}},
		{"ReadCSVParallel", []string{"read", "csvparallel"}}, // acronym runs stay joined
	}
	for _, c := range cases {
		got := tokenize(c.in)
		if len(got) != len(c.want) {
			t.Errorf("tokenize(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("tokenize(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestBM25Ranking(t *testing.T) {
	chunks := []chunk{
		{Heading: "catalog pruning", Text: "pruning skips shards by metadata; pruning is conservative"},
		{Heading: "sorting", Text: "sort orders rows by a field"},
		{Heading: "misc", Text: "catalog files list shards"},
	}
	ranked := bm25(chunks, "catalog pruning")
	if len(ranked) < 2 || ranked[0].idx != 0 {
		t.Errorf("expected the pruning chunk first, got %+v", ranked)
	}
	// A term absent from the corpus contributes nothing but doesn't break.
	if r := bm25(chunks, "zeppelin"); len(r) != 0 {
		t.Errorf("no-match query should return empty, got %+v", r)
	}
}

func TestRRF(t *testing.T) {
	a := []scored{{1, 9}, {2, 5}, {3, 1}}
	b := []scored{{3, 0.9}, {1, 0.8}, {4, 0.1}}
	fused := rrf(a, b)
	// idx 1 appears high in both lists — must win.
	if fused[0].idx != 1 {
		t.Errorf("expected idx 1 first, got %+v", fused)
	}
}
