package lib

import (
	"strings"
	"testing"
)

func stmtFrag(v, in, code string) *CodeFragment {
	return &CodeFragment{Type: "stmt", Var: v, Input: in, Code: code}
}

func TestResolveBindingsCollisionChain(t *testing.T) {
	// Three stages all emitting the base name "included" (what
	// commands do now that uniqueVarName is retired). The binder must
	// rename the later two AND rewrite every downstream reference to
	// the most recent binding.
	frags := []*CodeFragment{
		{Type: "init", Var: "records", Code: `records := read()`},
		stmtFrag("included", "records", `included := typed.Project[A](records)`),
		stmtFrag("included", "included", `included := typed.Project[B](included)`),
		stmtFrag("included", "included", `included := typed.Project[C](included)`),
		{Type: "final", Var: "", Input: "included", Code: `write(included)`},
	}
	ResolveBindings(frags)

	if frags[1].Var != "included" || frags[2].Var != "included2" || frags[3].Var != "included3" {
		t.Fatalf("vars = %q %q %q, want included/included2/included3", frags[1].Var, frags[2].Var, frags[3].Var)
	}
	if frags[2].Code != `included2 := typed.Project[B](included)` {
		t.Errorf("stage 2 code = %q", frags[2].Code)
	}
	if frags[3].Code != `included3 := typed.Project[C](included2)` {
		t.Errorf("stage 3 code = %q", frags[3].Code)
	}
	if frags[4].Input != "included3" || frags[4].Code != `write(included3)` {
		t.Errorf("final = input %q code %q, want the newest binding", frags[4].Input, frags[4].Code)
	}
}

func TestResolveBindingsNoCollisionUntouched(t *testing.T) {
	frags := []*CodeFragment{
		{Type: "init", Var: "records", Code: `records := read()`},
		stmtFrag("filtered", "records", `filtered := where(records)`),
		stmtFrag("sorted", "filtered", `sorted := sort(filtered)`),
	}
	ResolveBindings(frags)
	if frags[1].Code != `filtered := where(records)` || frags[2].Code != `sorted := sort(filtered)` {
		t.Errorf("collision-free chain was modified: %q / %q", frags[1].Code, frags[2].Code)
	}
}

func TestResolveBindingsRewritesAltCode(t *testing.T) {
	// The planner may swap AltCodeIfSeq in AFTER binding resolution,
	// so the alternative template must carry the same final names.
	frags := []*CodeFragment{
		stmtFrag("limited", "records", `limited := typed.Limit(records)`),
		&CodeFragment{
			Type: "stmt", Var: "limited", Input: "limited",
			Code:         `limited := parallel(limited)`,
			AltCodeIfSeq: `limited := serial(limited)`,
		},
	}
	ResolveBindings(frags)
	if frags[1].Code != `limited2 := parallel(limited)` {
		t.Errorf("Code = %q", frags[1].Code)
	}
	if frags[1].AltCodeIfSeq != `limited2 := serial(limited)` {
		t.Errorf("AltCodeIfSeq = %q — the planner would swap in stale names", frags[1].AltCodeIfSeq)
	}
}

func TestRenameIdentTokenAware(t *testing.T) {
	cases := []struct{ name, code, want string }{
		{"plain", `x := f(old)`, `x := f(new)`},
		{"string literal untouched", `x := f(old, "old")`, `x := f(new, "old")`},
		{"comment untouched", `old := 1 // old value`, `new := 1 // old value`},
		{"longer ident untouched", `oldRecords := old`, `oldRecords := new`},
		{"selector field untouched", `y := s.old + old`, `y := s.new + old`},
	}
	// NOTE the selector case expectation: s.old must NOT be renamed,
	// old must be. (want string above is intentionally wrong for it —
	// fixed here to keep the table readable.)
	cases[4].want = `y := s.old + new`
	for _, c := range cases {
		if got := renameIdent(c.code, "old", "new"); got != c.want {
			t.Errorf("%s: renameIdent(%q) = %q, want %q", c.name, c.code, got, c.want)
		}
	}
	// Multi-line fragment with struct literal and format string.
	code := "outVar := ssql.Chain(\n\tf1,\n)(outVar0)\nfmt.Printf(\"outVar0: %v\\n\", outVar0)"
	got := renameIdent(code, "outVar0", "base")
	if !strings.Contains(got, `)(base)`) || !strings.Contains(got, `"outVar0: %v\n", base)`) {
		t.Errorf("multi-line rename wrong:\n%s", got)
	}
}
