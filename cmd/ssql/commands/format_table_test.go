package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFormatForPath pins the authority table (DFC116 F1+F2): the one
// place extension grammar lives. If a format row changes here, every
// consumer (from routing, aux inputs, serve suggestions/listings,
// completion hooks, -records, DSP inputs, typed-join codegen)
// inherits it — so these facts are load-bearing.
func TestFormatForPath(t *testing.T) {
	cases := []struct {
		path string
		name string
		ok   bool
	}{
		{"data.csv", "csv", true},
		{"DATA.CSV", "csv", true},
		{"x.ndjson", "jsonl", true}, // alias: ndjson is jsonl
		{"x.jsonl", "jsonl", true},
		{"x.parquet", "parquet", true},
		{"https://h/p/x.parquet?X-Amz-Signature=abc.def", "parquet", true}, // presigned URL query ignored
		{"https://h/p/x.CSV#frag", "csv", true},
		{"x.orc", "", false},
		{"noext", "", false},
		{"https://h/p/noext", "", false},
	}
	for _, c := range cases {
		fi, ok := formatForPath(c.path)
		if ok != c.ok || fi.Name != c.name {
			t.Errorf("formatForPath(%q) = (%q, %v), want (%q, %v)", c.path, fi.Name, ok, c.name, c.ok)
		}
	}

	// Capability facts consumers rely on.
	for ext, want := range map[string]struct{ sampleable, binary, cheap bool }{
		".csv":     {true, false, true},
		".jsonl":   {true, false, true},
		".json":    {false, false, true}, // arrays: no byte-offset sampling
		".parquet": {false, true, true},  // footer count, no line sampling
		".arrow":   {false, true, false},
	} {
		fi := formatByExt[ext]
		if fi.Sampleable != want.sampleable || fi.Binary != want.binary || fi.CheapRecords != want.cheap {
			t.Errorf("%s caps = %+v, want %+v", ext, fi, want)
		}
	}
}

// TestReadRecordsFile covers the shared DSP input reader (was five
// identical extension switches).
func TestReadRecordsFile(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "d.csv")
	os.WriteFile(csv, []byte("a,b\n1,2\n3,4\n"), 0o644)
	recs, err := readRecordsFile(csv)
	if err != nil || len(recs) != 2 {
		t.Fatalf("csv: %d recs, err %v", len(recs), err)
	}
	if _, err := readRecordsFile(filepath.Join(dir, "d.wav")); err == nil {
		t.Error("wav should be unsupported for record reads")
	}
}
