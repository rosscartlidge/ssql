package typed

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// fastRow covers every kind the positional decoder handles.
type fastRow struct {
	Name   string    `json:"name"`
	Age    int64     `json:"age"`
	Score  float64   `json:"score"`
	Active bool      `json:"active"`
	Small  int32     `json:"small"`
	Big    uint64    `json:"big"`
	When   time.Time `json:"when"`
	Note   *string   `json:"note"`
	Count  *int64    `json:"count"`
	Plain  string    // no tag: matches "Plain" exactly, "plain" case-insensitively
	Hidden string    `json:"-"`
	hidden string    // unexported: ignored
}

// TestJSONLPositionalMatchesEncodingJSON is the differential gate: on
// every line where encoding/json succeeds AND the value shapes are the
// ones both accept, the positional decoder must produce the identical
// struct; on malformed lines both must fail.
func TestJSONLPositionalMatchesEncodingJSON(t *testing.T) {
	pl, err := buildJSONLPlan[fastRow]()
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"name":"Alice","age":35,"score":95000.5,"active":true,"small":-7,"big":18446744073709551615,"when":"2026-09-06T10:00:00Z","note":"hi","count":3,"Plain":"p"}`,
		`{"name":"Bob"}`,
		`{"age":-0,"score":1e3,"active":false}`,
		`{"score":2.5E-2,"age":9223372036854775807}`,
		`{"NAME":"case","AGE":1,"plain":"lower"}`,
		`{"name":"esc \"quoted\" \\ backé","note":"tab\there"}`,
		`{"note":null,"count":null,"name":null,"age":null}`,
		`{"unknown":{"deep":[1,2,{"x":"}"}]},"name":"after nested","also":[1,2,3],"more":"z"}`,
		`{"unknown":"}","name":"brace in string"}`,
		`  { "name" : "spaced" , "age" : 2 }  `,
		`{}`,
		`{"hidden":"ignored","Hidden":"ignored too","name":"h"}`,
		`{"when":""}`,
		// malformed: both must error
		`{not json}`,
		`{"name":"unterminated`,
		`{"name":"x",}`,
		`{"name":"x"} trailing`,
		`{"age":"12"}`,      // string into number: both reject
		`{"active":"true"}`, // string into bool: both reject
		`{"age":1.5}`,       // fraction into int64: both reject
		`{"when":"not-a-time"}`,
		`{"age":true}`,
		`{"age":{"nested":1}}`,
	}
	for _, ln := range lines {
		var want fastRow
		wantErr := json.Unmarshal([]byte(ln), &want)
		var got fastRow
		gotErr := pl.decode([]byte(ln), unsafe.Pointer(&got))
		if (wantErr == nil) != (gotErr == nil) {
			t.Errorf("%s\n  encoding/json err=%v\n  positional   err=%v", ln, wantErr, gotErr)
			continue
		}
		if wantErr == nil && !reflect.DeepEqual(got, want) {
			t.Errorf("%s\n  encoding/json: %+v\n  positional:    %+v", ln, want, got)
		}
	}
}

// Where the positional decoder is deliberately more lenient than
// encoding/json: a string field takes any value's raw JSON text (exec
// stores nested and mixed values the same way, and the sampler types a
// mixed column as string). Pinned so the divergence stays intentional.
func TestJSONLPositionalStringFieldTakesRawJSON(t *testing.T) {
	type row struct {
		V string `json:"v"`
	}
	pl, _ := buildJSONLPlan[row]()
	for in, want := range map[string]string{
		`{"v":42}`:             "42",
		`{"v":-1.5e3}`:         "-1.5e3",
		`{"v":true}`:           "true",
		`{"v":{"a":[1,"x"]}}`:  `{"a":[1,"x"]}`,
		`{"v":[1, 2]}`:         `[1, 2]`,
		`{"v":null}`:           "",
		`{"v":"plain string"}`: "plain string",
	} {
		var r row
		if err := pl.decode([]byte(in), unsafe.Pointer(&r)); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if r.V != want {
			t.Errorf("%s: V=%q, want %q", in, r.V, want)
		}
	}
}

// Extension over encoding/json: an `ssql` tag names the JSON key when
// there is no `json` tag, so a CSV-reader struct reads JSONL unchanged.
func TestJSONLPositionalHonoursSSQLTag(t *testing.T) {
	type row struct {
		DeptID string `ssql:"dept_id"`
	}
	pl, err := buildJSONLPlan[row]()
	if err != nil {
		t.Fatal(err)
	}
	var r row
	if err := pl.decode([]byte(`{"dept_id":"D01"}`), unsafe.Pointer(&r)); err != nil || r.DeptID != "D01" {
		t.Errorf("ssql tag should name the key: %+v err=%v", r, err)
	}
}

// Unsupported field kinds fall back to encoding/json for the whole type.
func TestJSONLPositionalFallsBackForUnsupportedKinds(t *testing.T) {
	type withSlice struct {
		Tags []string `json:"tags"`
	}
	if _, err := buildJSONLPlan[withSlice](); err == nil {
		t.Fatal("slice field should have no positional plan")
	}
	got := 0
	for r := range ReadJSONLFromReader[withSlice](strings.NewReader("{\"tags\":[\"a\",\"b\"]}\n")) {
		if len(r.Tags) != 2 {
			t.Errorf("fallback decode wrong: %+v", r)
		}
		got++
	}
	if got != 1 {
		t.Errorf("fallback read %d rows, want 1", got)
	}
}

func BenchmarkReadJSONLPositional(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "{\"name\":\"user%d\",\"age\":%d,\"score\":%d.5,\"active\":%v}\n", i, 20+i%50, i%1000, i%2 == 0)
	}
	src := sb.String()
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for b.Loop() {
		n := 0
		for range ReadJSONLFromReader[fastRow](strings.NewReader(src)) {
			n++
		}
		if n != 20000 {
			b.Fatalf("rows=%d", n)
		}
	}
}

func BenchmarkReadJSONLEncodingJSON(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "{\"name\":\"user%d\",\"age\":%d,\"score\":%d.5,\"active\":%v}\n", i, 20+i%50, i%1000, i%2 == 0)
	}
	src := sb.String()
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for b.Loop() {
		n := 0
		for range readJSONLReflect[fastRow](strings.NewReader(src)) {
			n++
		}
		if n != 20000 {
			b.Fatalf("rows=%d", n)
		}
	}
}
