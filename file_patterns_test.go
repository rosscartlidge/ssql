package ssql

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestExpandFilePatterns: a pattern becomes its sorted matches in place,
// literals and URLs pass through, a pattern matching nothing and ** are
// errors (never an empty read, never a silently shallow one).
func TestExpandFilePatterns(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b.parquet", "a.parquet", "sub/c.parquet"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0o755)
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	got, err := ExpandFilePatterns([]string{"https://h/x*.parquet", dir + "/*.parquet", "lit.parquet"})
	want := []string{"https://h/x*.parquet", dir + "/a.parquet", dir + "/b.parquet", "lit.parquet"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("got %v (%v), want %v", got, err, want)
	}
	if _, err := ExpandFilePatterns([]string{dir + "/*.csv"}); err == nil || !strings.Contains(err.Error(), "matches no files") {
		t.Errorf("no match: %v", err)
	}
	if _, err := ExpandFilePatterns([]string{dir + "/**/*.parquet"}); err == nil || !strings.Contains(err.Error(), "**") {
		t.Errorf("**: %v", err)
	}
}
