package commands

import (
	"context"
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

func TestHeadInputRowsShape(t *testing.T) {
	// The count itself now comes from `from -records` (covered by the
	// CLI integration tests) — here we pin the thin wrapper's pure
	// parts: non-from stages return false without exec'ing anything
	// (self="" would fail loudly otherwise), and the early-limit cap.
	if _, ok := headInputRows(context.Background(), "", t.TempDir(), [][]string{{"sample", "5"}}); ok {
		t.Error("non-from stage must return false")
	}
	if _, ok := headInputRows(context.Background(), "", t.TempDir(), nil); ok {
		t.Error("empty stages must return false")
	}
	if got := capByEarlyLimit(100, [][]string{{"from", "x.csv"}, {"limit", "7"}}); got != 7 {
		t.Errorf("limit cap: %d", got)
	}
	if got := capByEarlyLimit(5, [][]string{{"from", "x.csv"}, {"limit", "7"}}); got != 5 {
		t.Errorf("limit larger than count: %d", got)
	}
	if got := capByEarlyLimit(100, [][]string{{"from", "x.csv"}, {"sort", "a"}}); got != 100 {
		t.Errorf("no limit: %d", got)
	}
}

func TestDirDataFingerprint(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/a.csv", []byte("x\n1\n"), 0o644)
	f1 := dirDataFingerprint(dir)
	f2 := dirDataFingerprint(dir)
	if f1 != f2 || f1 == "" {
		t.Fatalf("unstable fingerprint: %q vs %q", f1, f2)
	}
	os.WriteFile(dir+"/a.csv", []byte("x\n1\n2\n"), 0o644)
	if dirDataFingerprint(dir) == f1 {
		t.Error("file change did not change fingerprint")
	}
}

func TestValidateReadonly(t *testing.T) {
	cases := []struct {
		name    string
		stages  [][]string
		wantErr string
	}{
		{"plain read allowed", [][]string{{"from", "a.csv"}, {"where", "-if", "x", "gt", "1"}}, ""},
		{"to stdout allowed", [][]string{{"from", "a.csv"}, {"to", "jsonl"}}, ""},
		{"tee rejected", [][]string{{"from", "a.csv"}, {"tee", "out.jsonl"}}, "tee"},
		{"to FILE rejected", [][]string{{"from", "a.csv"}, {"to", "csv", "out.csv"}}, "may write a file"},
		{"to -o rejected", [][]string{{"from", "a.csv"}, {"to", "markdown", "-o", "x.md"}}, "-o"},
		{"generate -run rejected", [][]string{{"from", "a.csv"}, {"generate", "go", "-run"}}, "compiles/writes"},
		{"generate plain allowed", [][]string{{"from", "a.csv"}, {"generate", "sql"}}, ""},
		{"conservative flag-value rejection", [][]string{{"from", "a.csv"}, {"to", "explore", "-title", "My"}}, "may write a file"},
	}
	for _, c := range cases {
		err := validateReadonly(c.stages)
		if c.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected %v", c.name, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: got %v, want containing %q", c.name, err, c.wantErr)
		}
	}
}
