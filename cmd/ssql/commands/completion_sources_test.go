package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompletionFieldValueSourceLocal: Ctrl-O value sampling reads
// local line formats through ssql's own readers, so a dotted field
// samples through the path walk and a nested value is offered as its
// JSON text (autocli's line sampler could not open a JSON array file,
// printed Go syntax for lists, and knew no paths — found 2026-10-09).
func TestCompletionFieldValueSourceLocal(t *testing.T) {
	dir := t.TempDir()
	rows := `{"id":1,"name":"ann","addr":{"city":"NYC","geo":{"lat":40.7}},"tags":["go","rust"]}
{"id":2,"name":"bob","addr":{"city":"SF","geo":{"lat":37.7}},"tags":["c"]}
`
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	array := write("n.json", "[\n"+strings.ReplaceAll(strings.TrimSpace(rows), "\n", ",\n")+"\n]\n")
	headed := write("n.jsonl", `{"_schema":{"fields":["id","name","addr","tags"],"types":{"id":"int","name":"string","addr":"json","tags":"json"}}}`+"\n"+rows)
	bare := write("b.jsonl", rows)
	csv := write("c.csv", "name,city\nann,NYC\nbob,SF\n")

	cases := []struct {
		src, field string
		want       string
	}{
		{array, "name", "ann bob"},
		{array, "addr.city", "NYC SF"},
		{array, "addr.geo.lat", "37.7 40.7"},
		{array, "tags", `["c"] ["go","rust"]`},
		{array, "tags.0", "c go"},
		{array, "id", "1 2"},
		{headed, "addr.city", "NYC SF"},
		{headed, "tags", `["c"] ["go","rust"]`},
		{bare, "addr.geo.lat", "37.7 40.7"},
		{csv, "city", "NYC SF"},
	}
	for _, c := range cases {
		got, err := completionFieldValueSource(c.src, c.field, 10, 100)
		if err != nil {
			t.Errorf("%s %s: %v", filepath.Base(c.src), c.field, err)
			continue
		}
		if g := strings.Join(got, " "); g != c.want {
			t.Errorf("%s %s: got %q want %q", filepath.Base(c.src), c.field, g, c.want)
		}
	}
	if _, err := completionFieldValueSource(array, "nosuch", 10, 100); err == nil {
		t.Error("unknown field: want an error (autocli then shows <VALUE>)")
	}
	if _, err := completionFieldValueSource(filepath.Join(dir, "x.log"), "line", 10, 100); !errors.Is(err, errCompletionBuiltin) {
		t.Errorf("a .log stays with autocli's sampler, got %v", err)
	}
}
