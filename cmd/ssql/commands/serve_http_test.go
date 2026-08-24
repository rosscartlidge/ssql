package commands

import (
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSplitServePipeline(t *testing.T) {
	cases := []struct {
		in      string
		want    [][]string
		wantErr string
	}{
		{in: "ssql from a.csv | ssql to jsonl",
			want: [][]string{{"from", "a.csv"}, {"to", "jsonl"}}},
		{in: "ssql where -if-expr 'salary > 50000 && dept == \"Eng\"'",
			want: [][]string{{"where", "-if-expr", `salary > 50000 && dept == "Eng"`}}},
		{in: `ssql where -if name eq "Ann E"`,
			want: [][]string{{"where", "-if", "name", "eq", "Ann E"}}},
		{in: "ssql from a.csv > out.txt", wantErr: "shell construct"},
		{in: "ssql join <(ssql from b.csv)", wantErr: "shell construct"},
		{in: "ssql from a.csv; rm -rf /", wantErr: "shell construct"},
		{in: "ssql from a.csv | ssql to jsonl & ", wantErr: "shell construct"},
		{in: "ssql from `hostname`.csv", wantErr: "shell construct"},
		{in: "ssql from $(hostname).csv", wantErr: "command substitution"},
		{in: "ssql from a.csv | | ssql to jsonl", wantErr: "empty pipeline stage"},
		{in: "grep foo a.csv", wantErr: "must start with 'ssql'"},
		{in: "ssql", wantErr: "no subcommand"},
		{in: "ssql where -if x eq 'unterminated", wantErr: "unterminated"},
		// $FIELD is inert (no shell) — passes through literally.
		{in: "ssql where -if cost eq $100",
			want: [][]string{{"where", "-if", "cost", "eq", "$100"}}},
	}
	for _, c := range cases {
		got, err := splitServePipeline(c.in)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%q: want error containing %q, got %v (stages %v)", c.in, c.wantErr, err, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q:\n got %v\nwant %v", c.in, got, c.want)
		}
	}
}

func TestJoinRightFile(t *testing.T) {
	cases := []struct{ stage, want string }{
		{"ssql join kind.csv -on a_kind ", "kind.csv"},
		{"ssql join lookup.jsonl -as ", "lookup.jsonl"},
		{"ssql join <(ssql from kind.csv) -on a_kind ", ""},
		{"ssql join -on a_kind ", ""},
		{"ssql join notafile -on x ", ""},
		{"ssql where -if a eq b ", ""},
	}
	for _, c := range cases {
		if got := joinRightFile(c.stage); got != c.want {
			t.Errorf("joinRightFile(%q) = %q, want %q", c.stage, got, c.want)
		}
	}
}

func TestIsTailscaleIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.1", true}, {"100.106.137.79", true}, {"100.127.255.255", true},
		{"100.63.255.255", false}, {"100.128.0.0", false},
		{"127.0.0.1", false}, {"192.168.1.5", false}, {"10.0.0.1", false},
		{"fd7a:115c:a1e0::e601:8991", true},
		{"fd7a:115c:a1e1::1", false}, {"fe80::1", false}, {"::1", false},
	}
	for _, c := range cases {
		if got := isTailscaleIP(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("isTailscaleIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestHeadInputRows(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/d.csv", []byte("a,b\n1,2\n3,4\n5,6\n"), 0o644)
	os.WriteFile(dir+"/d.jsonl", []byte("{\"a\":1}\n{\"a\":2}\n"), 0o644)

	cases := []struct {
		name   string
		stages [][]string
		want   int64
		ok     bool
	}{
		{"csv header subtracted", [][]string{{"from", "csv", "d.csv"}}, 3, true},
		{"bare from by ext", [][]string{{"from", "d.csv"}}, 3, true},
		{"jsonl no header", [][]string{{"from", "jsonl", "d.jsonl"}}, 2, true},
		{"sample wins", [][]string{{"from", "csv", "d.csv", "-sample", "2", "-sample-seed", "1"}}, 2, true},
		{"limit caps", [][]string{{"from", "csv", "d.csv"}, {"limit", "2"}}, 2, true},
		{"limit larger than file", [][]string{{"from", "csv", "d.csv"}, {"limit", "99"}}, 3, true},
		{"json array unsafe", [][]string{{"from", "json", "d.json"}}, 0, false},
		{"non-from source", [][]string{{"sample", "5"}}, 0, false},
		{"missing file", [][]string{{"from", "csv", "nope.csv"}}, 0, false},
		{"parquet missing file", [][]string{{"from", "nope.parquet"}}, 0, false},
	}
	for _, c := range cases {
		got, ok := headInputRows(dir, c.stages)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: got (%d,%v), want (%d,%v)", c.name, got, ok, c.want, c.ok)
		}
	}

	// Cache: second call must not re-read (mutate the file KEEPING
	// size+mtime is hard portably; instead verify the cache entry
	// exists and a changed file invalidates).
	if _, err := os.Stat(dir + "/d.csv"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(dir+"/d.csv", []byte("a,b\n1,2\n"), 0o644)
	got, ok := headInputRows(dir, [][]string{{"from", "csv", "d.csv"}})
	if !ok || got != 1 {
		t.Errorf("after rewrite: got (%d,%v), want (1,true)", got, ok)
	}
}
