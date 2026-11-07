package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func joinPushdownDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"l.csv": "id,a_kind,z_kind,name\n1,10,20,x\n2,20,10,y\n",
		"k.csv": "kind,kind_name,other\n10,TEN,p\n20,TWENTY,q\n",
		"r.csv": "id,label\n1,A\n2,B\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func joinPushdownRun(t *testing.T, dir, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// TestJoinPushdownKeepsTheError: a where on a name the stage does not produce
// fails in exec. The rule found the name in the right file and pushed it there,
// so the rewritten pipeline ran and answered (found 2026-11-07). The where
// has to stay where it was, to fail as it does in exec.
func TestJoinPushdownKeepsTheError(t *testing.T) {
	bin := corpusBin(t)
	dir := joinPushdownDir(t)
	for _, c := range []struct{ name, stages string }{
		{"as_old_name", "join k.csv -on a_kind kind -as kind_name kn | where -if kind_name eq TEN"},
		{"suffix", "join k.csv -on a_kind kind -suffix _r | where -if kind_name eq TEN"},
		{"exclude_right", "join k.csv -on a_kind kind -exclude-right | where -if kind_name eq TEN"},
		{"include_dropped_it", "join k.csv -on a_kind kind -as kind_name kn | include id | where -if kn eq TEN"},
		{"exclude_dropped_it", "join k.csv -on a_kind kind -as kind_name kn | exclude kn | where -if kn eq TEN"},
	} {
		t.Run(c.name, func(t *testing.T) {
			stages := bin + " from csv l.csv | " + bin + " " + strings.ReplaceAll(c.stages, " | ", " | "+bin+" ")
			if out, err := joinPushdownRun(t, dir, stages+" | "+bin+" to jsonl"); err == nil {
				t.Fatalf("exec should fail, so this case tests nothing:\n%s", out)
			}
			out, err := joinPushdownRun(t, dir, "export SSQL_MODE=record; "+stages+" | "+bin+" to jsonl | "+bin+" generate ssql")
			if err != nil {
				t.Fatalf("generate ssql: %v\n%s", err, out)
			}
			if strings.Contains(out, "<(") {
				t.Errorf("the where moved into the right side:\n%s", out)
			}
			if !strings.Contains(out, "| ssql where") {
				t.Errorf("the where left its place:\n%s", out)
			}
		})
	}
}

// TestJoinPushdownRewrites pins what the rule does, so that a change that stops
// it firing fails here and not only in a benchmark.
func TestJoinPushdownRewrites(t *testing.T) {
	bin := corpusBin(t)
	dir := joinPushdownDir(t)
	for _, c := range []struct {
		name, stages string
		want, not    []string
	}{
		{
			name:   "lookup_clause_splits",
			stages: "join k.csv -on a_kind kind -as kind_name a - -on z_kind kind -as kind_name z | where -if a eq TEN",
			want:   []string{"join <(ssql from k.csv | ssql where -if kind_name eq TEN) -on a_kind kind -as kind_name a | ssql join k.csv -on z_kind kind -as kind_name z"},
		},
		{
			name:   "second_clause_keeps_column_order",
			stages: "join k.csv -on a_kind kind -as kind_name a - -on z_kind kind -as kind_name z | where -if z eq TEN",
			want:   []string{"join k.csv -on a_kind kind -as kind_name a | ssql join <(ssql from k.csv | ssql where -if kind_name eq TEN) -on z_kind kind -as kind_name z"},
		},
		{
			name:   "past_include",
			stages: "join k.csv -on a_kind kind -as kind_name a | include id a | where -if a eq TEN",
			want:   []string{"join <(ssql from k.csv | ssql where -if kind_name eq TEN) -on a_kind kind -as kind_name a | ssql include id a"},
			not:    []string{"include id a | ssql where"},
		},
		{
			name:   "across_two_joins",
			stages: "join k.csv -on a_kind kind -as kind_name a | join r.csv -using id | where -if a eq TEN -if label eq A",
			want:   []string{"join <(ssql from k.csv | ssql where -if kind_name eq TEN)", "join <(ssql from r.csv | ssql where -if label eq A) -using id"},
			not:    []string{"-using id | ssql where"},
		},
		{
			name:   "using_key_stays",
			stages: "join r.csv -using id | where -if id eq 1",
			want:   []string{"-using id | ssql where -if id eq 1"},
		},
		{
			name:   "left_join_stays",
			stages: "join k.csv -on a_kind kind -type left | where -if kind_name eq TEN",
			want:   []string{"-type left | ssql where -if kind_name eq TEN"},
			not:    []string{"<("},
		},
		{
			name:   "or_stays",
			stages: "join k.csv -on a_kind kind -as kind_name a | where -if a eq TEN + -if id eq 2",
			want:   []string{"ssql where -if a eq TEN + -if id eq 2"},
			not:    []string{"<("},
		},
		{
			name:   "stops_at_an_update",
			stages: "join k.csv -on a_kind kind -as kind_name a | update -set a Q | where -if a eq Q",
			want:   []string{"ssql where -if a eq Q"},
			not:    []string{"<("},
		},
		{
			name:   "filtered_side_is_not_read_twice",
			stages: "join <(" + bin + " from k.csv | " + bin + " include kind kind_name) -on a_kind kind -as kind_name a - -on z_kind kind -as kind_name z | where -if a eq TEN",
			want:   []string{"-on a_kind kind -as kind_name a - -on z_kind kind -as kind_name z | ssql where -if a eq TEN"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			stages := bin + " from csv l.csv | " + bin + " " + strings.ReplaceAll(c.stages, " | where", " | "+bin+" where")
			stages = strings.ReplaceAll(stages, " | join", " | "+bin+" join")
			stages = strings.ReplaceAll(stages, " | include", " | "+bin+" include")
			stages = strings.ReplaceAll(stages, " | update", " | "+bin+" update")
			out, err := joinPushdownRun(t, dir, "export SSQL_MODE=record; "+stages+" | "+bin+" to jsonl | "+bin+" generate ssql")
			if err != nil {
				t.Fatalf("generate ssql: %v\n%s", err, out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in\n%s", w, out)
				}
			}
			for _, n := range c.not {
				if strings.Contains(out, n) {
					t.Errorf("unexpected %q in\n%s", n, out)
				}
			}
		})
	}
}

// TestJoinPushdownNoMatchKeepsTheHeader: a filter that empties the right side
// must not lose the output's columns: the file's header stays exec's.
func TestJoinPushdownNoMatchKeepsTheHeader(t *testing.T) {
	bin := corpusBin(t)
	dir := joinPushdownDir(t)
	stages := bin + " from csv l.csv | " + bin + " join k.csv -on a_kind kind -as kind_name a | " + bin + " where -if a eq NOTHING | " + bin + " include id a"
	want, err := joinPushdownRun(t, dir, stages+" | "+bin+" to csv")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, want)
	}
	pipeline := strings.ReplaceAll(stages, bin+" ", "ssql ") + " | ssql to csv"
	got, err := joinPushdownRun(t, dir, "PATH="+filepath.Dir(bin)+":$PATH "+bin+" generate go -run -pipeline '"+pipeline+"'")
	if err != nil {
		t.Fatalf("generate go -run: %v\n%s", err, got)
	}
	if got != want {
		t.Errorf("typed %q, exec %q", got, want)
	}
}

// TestJoinSideKeepsItsOrder: a join's right side with a where is a parallel read,
// and fanning it in with Serial made the order of one left row's matches change
// from run to run. The planner's rule makes a side keep shard order.
func TestJoinSideKeepsItsOrder(t *testing.T) {
	bin := corpusBin(t)
	dir := joinPushdownDir(t)
	if err := os.WriteFile(filepath.Join(dir, "side.json"), []byte(`[{"kind":10,"kind_name":"TEN"},{"kind":20,"kind_name":"TWENTY"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := func(side string) string {
		t.Helper()
		pipeline := "ssql from csv l.csv | ssql join <(ssql from " + side + " | ssql where -if kind_name ne nothing) -on a_kind kind -as kind_name a | ssql to jsonl"
		out, err := joinPushdownRun(t, dir, "PATH="+filepath.Dir(bin)+":$PATH "+bin+" generate go -pipeline '"+pipeline+"'")
		if err != nil {
			t.Fatalf("generate go: %v\n%s", err, out)
		}
		return out
	}
	body := func(src, fn string) string {
		start := strings.Index(src, "func "+fn+"()")
		if start < 0 {
			t.Fatalf("no %s in\n%s", fn, src)
		}
		end := strings.Index(src[start:], "\n}\n")
		return src[start : start+end]
	}
	csv := body(gen("csv k.csv"), "rightSource1")
	if !strings.Contains(csv, ".InOrder()") || strings.Contains(csv, ".Serial()") {
		t.Errorf("a CSV side should be parallel and InOrder:\n%s", csv)
	}
	// A JSON array's shards are batches, not runs: only a serial read has an order.
	js := body(gen("json side.json"), "rightSource1")
	if strings.Contains(js, ".Serial()") || strings.Contains(js, ".InOrder()") {
		t.Errorf("a JSON array side should be read serially:\n%s", js)
	}
}
