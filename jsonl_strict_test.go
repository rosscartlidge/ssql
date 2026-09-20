package ssql

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// readAllJSONL drains a reader, returning the records and whatever it
// panicked with.
func readAllJSONL(seq func(func(Record) bool)) (recs []Record, panicked any) {
	defer func() { panicked = recover() }()
	for r := range seq {
		recs = append(recs, r)
	}
	return recs, nil
}

// TestJSONLReaderIsStrict (DFC133 §7.6): a line that is not JSON is an
// ERROR carrying its line number — it used to be skipped, which is how a
// bare NaN made whole rows vanish. The skip-invalid variant is the
// explicit opt-out, and it counts.
func TestJSONLReaderIsStrict(t *testing.T) {
	const dirty = "{\"id\":1}\nnot json\n{\"id\":2}\n\n{\"id\":3}\n"
	recs, p := readAllJSONL(ReadJSONLFromReader(strings.NewReader(dirty)))
	var le *LineError
	err, _ := p.(error)
	if err == nil || !errors.As(err, &le) || le.Line != 2 || !strings.Contains(err.Error(), "line 2 is not JSON") || !strings.Contains(err.Error(), "-skip-invalid") {
		t.Fatalf("want a *LineError for line 2 naming the way out, got %v", p)
	}
	if len(recs) != 1 {
		t.Errorf("records before the bad line are still delivered: got %d", len(recs))
	}

	var skipped int64
	recs, p = readAllJSONL(ReadJSONLFromReaderSkipInvalid(strings.NewReader(dirty), &skipped))
	if p != nil || len(recs) != 3 || skipped != 1 {
		t.Errorf("skip-invalid: %d records, %d skipped, panic %v; want 3, 1, none", len(recs), skipped, p)
	}

	// With a _schema header, the header is line 1.
	headed := "{\"_schema\":{\"fields\":[\"id\"],\"types\":{\"id\":\"int\"}}}\n{\"id\":1}\n{oops\n"
	_, p = readAllJSONL(ReadJSONLFromReader(strings.NewReader(headed)))
	if err, _ := p.(error); err == nil || !errors.As(err, &le) || le.Line != 3 {
		t.Errorf("line numbers count the header: got %v", p)
	}

	// A JSON array fed to the line reader gets a pointer to the right command.
	_, p = readAllJSONL(ReadJSONLFromReader(strings.NewReader("[\n{\"id\":1}\n]\n")))
	if err, _ := p.(error); err == nil || !strings.Contains(err.Error(), "JSON ARRAY") || !strings.Contains(err.Error(), "from json") {
		t.Errorf("an array must be named as one: %v", p)
	}

	// Blank lines are not errors; clean input reads clean.
	recs, p = readAllJSONL(ReadJSONLFromReader(strings.NewReader("\n{\"id\":1}\n\n")))
	if p != nil || len(recs) != 1 {
		t.Errorf("blank lines: %d records, panic %v", len(recs), p)
	}
}

// TestJSONLOverlongLineIsAnError: a line longer than the scanner's limit
// used to END THE READ SILENTLY — every record after it dropped, because
// nothing checked scanner.Err(). The limit is 64 MB now, and crossing it
// is an error that says so.
func TestJSONLOverlongLineIsAnError(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a line longer than MaxJSONLineBytes")
	}
	var b bytes.Buffer
	b.WriteString("{\"id\":1}\n{\"big\":\"")
	b.Write(bytes.Repeat([]byte("x"), MaxJSONLineBytes+1))
	b.WriteString("\"}\n{\"id\":2}\n")
	recs, p := readAllJSONL(ReadJSONLFromReader(&b))
	err, _ := p.(error)
	if err == nil || !strings.Contains(err.Error(), "longer than 64 MB") || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("an over-long line must be an error naming the line and the limit, got %v", p)
	}
	if len(recs) != 1 {
		t.Errorf("got %d records before the error, want 1", len(recs))
	}
	// A 2 MB line — over the OLD 1 MB limit — now simply reads.
	var ok bytes.Buffer
	ok.WriteString("{\"big\":\"")
	ok.Write(bytes.Repeat([]byte("y"), 2<<20))
	ok.WriteString("\"}\n{\"id\":2}\n")
	recs, p = readAllJSONL(ReadJSONLFromReader(&ok))
	if p != nil || len(recs) != 2 {
		t.Errorf("a 2 MB line must read: %d records, panic %v", len(recs), p)
	}
}
