package typed

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catchReadError runs fn and returns the *ReadError it panicked with,
// failing the test when fn returned normally or panicked with anything
// else. The typed readers' fail-fast contract (read_error.go).
func catchReadError(t *testing.T, fn func()) (re *ReadError) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a *ReadError panic; the reader returned normally")
		}
		var ok bool
		re, ok = r.(*ReadError)
		if !ok {
			t.Fatalf("expected a *ReadError panic, got %T: %v", r, r)
		}
	}()
	fn()
	return nil
}

type lateRow struct {
	ID int64 `ssql:"id" json:"id"`
	V  int64 `ssql:"v" json:"v"`
}

// lateFixture writes n good rows then one whose v is "1.5" — the value
// that used to become 0 (CSV/Delim) or vanish (JSONL) without a word.
func lateFixture(t *testing.T, name string, n int, line func(i int) string, header string) string {
	t.Helper()
	var b strings.Builder
	if header != "" {
		b.WriteString(header + "\n")
	}
	for i := 1; i <= n; i++ {
		b.WriteString(line(i) + "\n")
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func csvLine(i int) string {
	if i == 1002 {
		return "1002,1.5"
	}
	return fmt.Sprintf("%d,%d", i, i)
}

func tsvLine(i int) string { return strings.ReplaceAll(csvLine(i), ",", "\t") }

func jsonLine(i int) string {
	if i == 1002 {
		return `{"id":1002,"v":1.5}`
	}
	return fmt.Sprintf(`{"id":%d,"v":%d}`, i, i)
}

// drain runs every shard to completion in the caller's goroutine.
func drain[T any](s Stream[T]) (n int) {
	for _, sh := range s.shards {
		for range sh {
			n++
		}
	}
	return n
}

func TestReadCSVLateMismatchFailsLoud(t *testing.T) {
	p := lateFixture(t, "late.csv", 1002, csvLine, "id,v")

	re := catchReadError(t, func() {
		for range ReadCSV[lateRow](p) {
		}
	})
	if re.Row != 1002 || re.Op != "typed.ReadCSV" || re.Source != p {
		t.Errorf("serial: %+v", re)
	}
	for _, want := range []string{"row 1002", `column "v"`, `"1.5" is not int64`, p} {
		if !strings.Contains(re.Error(), want) {
			t.Errorf("serial message should contain %q: %s", want, re)
		}
	}

	for _, n := range []int{1, 3, 8} {
		re := catchReadError(t, func() { drain(ReadCSVParallel[lateRow](p, n)) })
		if re.Row != 1002 || re.Op != "typed.ReadCSVParallel" {
			t.Errorf("parallel n=%d: %+v", n, re)
		}
	}
}

func TestReadDelimLateMismatchFailsLoud(t *testing.T) {
	p := lateFixture(t, "late.tsv", 1002, tsvLine, "id\tv")
	re := catchReadError(t, func() {
		for range ReadDelim[lateRow](p) {
		}
	})
	if re.Row != 1002 || !strings.Contains(re.Error(), `"1.5" is not int64`) {
		t.Errorf("serial: %+v", re)
	}
	for _, n := range []int{1, 4} {
		re := catchReadError(t, func() { drain(ReadDelimParallel[lateRow](p, n)) })
		if re.Row != 1002 {
			t.Errorf("parallel n=%d: %+v", n, re)
		}
	}
}

func TestReadJSONLLateMismatchFailsLoud(t *testing.T) {
	// A header line and a blank line shift the physical line number:
	// row 1002 is line 1004.
	p := lateFixture(t, "late.jsonl", 1002, jsonLine, "{\"_schema\":{\"fields\":[\"id\",\"v\"],\"types\":{\"id\":\"int\",\"v\":\"int\"}}}\n")
	re := catchReadError(t, func() {
		for range ReadJSONL[lateRow](p) {
		}
	})
	if re.Line != 1004 || re.Op != "typed.ReadJSONL" {
		t.Errorf("serial: %+v", re)
	}
	for _, want := range []string{"line 1004", `field "v"`, `"1.5" is not int64`} {
		if !strings.Contains(re.Error(), want) {
			t.Errorf("serial message should contain %q: %s", want, re)
		}
	}
	for _, n := range []int{1, 3, 8} {
		re := catchReadError(t, func() { drain(ReadJSONLParallel[lateRow](p, n)) })
		if re.Line != 1004 || re.Op != "typed.ReadJSONLParallel" {
			t.Errorf("parallel n=%d: %+v", n, re)
		}
	}

	// A malformed line is fatal too — the lossy reader no longer skips it.
	bad := lateFixture(t, "bad.jsonl", 0, nil, `{"id":1,"v":1}`+"\n{not json}")
	re = catchReadError(t, func() {
		for range ReadJSONL[lateRow](bad) {
		}
	})
	if re.Line != 2 {
		t.Errorf("malformed line: %+v", re)
	}
}

// A file that cannot be opened is an error, not an empty stream with
// exit status 0 (which is what every lossy reader did before).
func TestReadMissingFileFailsLoud(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.csv")
	cases := map[string]func(){
		"ReadCSV":           func() { drainSeq(ReadCSV[lateRow](missing)) },
		"ReadCSVParallel":   func() { ReadCSVParallel[lateRow](missing, 2) },
		"ReadDelim":         func() { drainSeq(ReadDelim[lateRow](missing)) },
		"ReadDelimParallel": func() { ReadDelimParallel[lateRow](missing, 2) },
		"ReadJSONL":         func() { drainSeq(ReadJSONL[lateRow](missing)) },
		"ReadJSONLParallel": func() { ReadJSONLParallel[lateRow](missing, 2) },
	}
	for name, fn := range cases {
		re := catchReadError(t, fn)
		if re.Op != "typed."+name || re.Row != 0 || re.Line != 0 || !errors.Is(re, os.ErrNotExist) {
			t.Errorf("%s: %+v", name, re)
		}
	}
}

func drainSeq[T any](seq func(func(T) bool)) {
	for range seq {
	}
}

// The Safe readers keep yielding the error and continuing.
func TestSafeReadersYieldLateMismatch(t *testing.T) {
	p := lateFixture(t, "late.csv", 1002, csvLine, "id,v")
	rows, errs := 0, 0
	for _, err := range ReadCSVSafe[lateRow](p) {
		if err != nil {
			errs++
			if !strings.Contains(err.Error(), `"1.5" is not int64`) {
				t.Errorf("safe error text: %v", err)
			}
			continue
		}
		rows++
	}
	if rows != 1001 || errs != 1 {
		t.Errorf("safe CSV: rows=%d errs=%d", rows, errs)
	}
}

// Every fan-out consumer must surface a shard's panic in the caller's
// goroutine (where a generated program's main can recover it), not
// crash the process from a worker. Uses the parallel CSV reader with a
// late mismatch so the panic originates inside a shard.
func TestStreamConsumersPropagateShardPanic(t *testing.T) {
	p := lateFixture(t, "late.csv", 1002, csvLine, "id,v")
	src := func() Stream[lateRow] { return ReadCSVParallel[lateRow](p, 4) }
	consumers := map[string]func(){
		"Serial":      func() { drainSeq(src().Serial()) },
		"SerialCount": func() { src().SerialCount() },
		"GroupByParallel": func() {
			drainSeq(GroupByParallel(src(), func(r lateRow) int64 { return r.ID % 3 }, NewCounter[lateRow](),
				func(k int64, n int64) int64 { return n }))
		},
		"WriteCSV": func() { _ = src().WriteCSVToWriter(&strings.Builder{}) },
		"WriteDelim": func() {
			_ = src().WriteDelimToWriter(&strings.Builder{})
		},
		"TopN": func() {
			drainSeq(TopByParallel(src(), 3, func(r lateRow) int64 { return r.V }))
		},
		"DistinctParallel": func() {
			drainSeq(DistinctParallel(src(), func(r lateRow) int64 { return r.V % 7 }))
		},
		"Where+Serial": func() {
			drainSeq(src().Where(func(r lateRow) bool { return r.V > 0 }).Serial())
		},
		"Parallel(feeder)": func() {
			drainSeq(Parallel(ReadCSV[lateRow](p), 4).Serial())
		},
	}
	for name, fn := range consumers {
		re := catchReadError(t, fn)
		if re.Row != 1002 {
			t.Errorf("%s: %+v", name, re)
		}
	}
}
