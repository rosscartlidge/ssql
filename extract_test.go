package ssql

import (
	"strings"
	"testing"
)

func TestReadLinesFromReaderNumbering(t *testing.T) {
	var got []string
	for r := range ReadLinesFromReader(strings.NewReader("a\nb\n\nc")) {
		got = append(got, GetOr(r, "line", "?"))
		if r.Keys()[0] != "line_number" || r.Keys()[1] != "line" {
			t.Errorf("schema order = %v", r.Keys())
		}
	}
	if len(got) != 4 || got[2] != "" || got[3] != "c" {
		t.Errorf("lines = %q", got)
	}
	var first int64
	for r := range ReadLinesFromReader(strings.NewReader("x\n")) {
		first = GetOr(r, "line_number", int64(0))
	}
	if first != 1 {
		t.Errorf("line_number is 1-based (like sed/awk); got %d", first)
	}
}

func TestExtractGolden(t *testing.T) {
	in := ReadLinesFromReader(strings.NewReader("2026-01-01T00:00:01Z INFO started\n2026-01-01T00:00:02Z WARN disk 91%\n"))
	out, err := ExtractRecords(in, ExtractConfig{Field: "line", Pattern: `^(?P<ts>\S+) (?P<lvl>\w+) (?P<msg>.*)$`})
	if err != nil {
		t.Fatal(err)
	}
	var rows []Record
	for r := range out {
		rows = append(rows, r)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[1]
	if GetOr(r, "ts", "") != "2026-01-01T00:00:02Z" || GetOr(r, "lvl", "") != "WARN" || GetOr(r, "msg", "") != "disk 91%" {
		t.Errorf("captures wrong: %v", r)
	}
	if r.Has("line") {
		t.Error("source field must be removed unless Keep")
	}
	if GetOr(r, "line_number", int64(0)) != 2 {
		t.Error("other fields must be kept")
	}
}

func TestExtractSkipAndKeep(t *testing.T) {
	in := ReadLinesFromReader(strings.NewReader("k=1\nnope\nk=2\n"))
	out, err := ExtractRecords(in, ExtractConfig{Field: "line", Pattern: `^k=(?P<k>\d+)$`, Skip: true, Keep: true})
	if err != nil {
		t.Fatal(err)
	}
	var ks []string
	for r := range out {
		ks = append(ks, GetOr(r, "k", ""))
		if !r.Has("line") {
			t.Error("Keep must retain the source field")
		}
	}
	if len(ks) != 2 || ks[0] != "1" || ks[1] != "2" {
		t.Errorf("Skip should drop the non-match: %v", ks)
	}
}

func TestExtractConfigErrors(t *testing.T) {
	if _, _, err := CompileExtract(ExtractConfig{Pattern: `(`}); err == nil {
		t.Error("bad regex must error")
	}
	if _, _, err := CompileExtract(ExtractConfig{Pattern: `(\d+)`}); err == nil || !strings.Contains(err.Error(), "named groups") {
		t.Errorf("unnamed-only groups must error loudly, got %v", err)
	}
}
