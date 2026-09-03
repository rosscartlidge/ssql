package lib

import (
	"os"
	"testing"
)

// Constructor stamping (DFC123 slice 2): every stage fragment carries
// the Op built from the emitting process's own argv; continuation
// fragments (command == "") stay Op-less.

func withArgs(t *testing.T, args []string, fn func()) {
	t.Helper()
	saved := os.Args
	os.Args = args
	defer func() { os.Args = saved }()
	fn()
}

func TestConstructorsStampOp(t *testing.T) {
	withArgs(t, []string{"/usr/bin/ssql", "where", "-if", "name", "eq", "O'Brien", "-generate"}, func() {
		frag := NewStmtFragment("filtered", "records", "x := y", nil, "ssql where …")
		if frag.Op == nil {
			t.Fatal("stage fragment must carry Op")
		}
		if frag.Op.Kind != "where" {
			t.Errorf("Kind = %q", frag.Op.Kind)
		}
		// -generate filtered out (mirrors getCommandString); the
		// quoted value arrives verbatim — no shell round-trip.
		want := []string{"-if", "name", "eq", "O'Brien"}
		if len(frag.Op.Argv) != len(want) {
			t.Fatalf("Argv = %q, want %q", frag.Op.Argv, want)
		}
		for i := range want {
			if frag.Op.Argv[i] != want[i] {
				t.Errorf("Argv[%d] = %q, want %q", i, frag.Op.Argv[i], want[i])
			}
		}
	})
}

func TestContinuationFragmentHasNoOp(t *testing.T) {
	withArgs(t, []string{"/usr/bin/ssql", "group-by", "dept"}, func() {
		frag := NewStmtFragment("aggregated", "grouped", "x := y", nil, "")
		if frag.Op != nil {
			t.Error("continuation fragment (empty command) must not carry Op — one stage, one Op")
		}
	})
}

func TestAllStageConstructorsStamp(t *testing.T) {
	withArgs(t, []string{"ssql", "from", "data.csv"}, func() {
		cases := map[string]*CodeFragment{
			"init":         NewInitFragment("records", "c", nil, "ssql from data.csv"),
			"stmt":         NewStmtFragment("v", "records", "c", nil, "ssql from data.csv"),
			"stmt-runtime": NewStmtFragmentWithRuntimeImport("v", "records", "c", nil, "ssql from data.csv"),
			"final":        NewFinalFragment("v", "c", nil, "ssql from data.csv"),
		}
		for name, frag := range cases {
			if frag.Op == nil || frag.Op.Kind != "from" {
				t.Errorf("%s: Op = %+v, want Kind=from", name, frag.Op)
			}
		}
	})
}
