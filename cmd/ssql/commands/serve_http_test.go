package commands

import (
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
