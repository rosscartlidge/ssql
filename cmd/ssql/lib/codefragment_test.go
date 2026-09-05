package lib

import (
	"strings"
	"testing"
)

// TestCollectParamsRename locks the cross-fragment flag-rename semantics.
// The old implementation had two defects, both hit by real pipelines:
//   - references were rewritten with a bare ReplaceAll, so renaming
//     *flagPopGt also corrupted *flagPopGt2 (→ *flagPopGt32, undeclared);
//   - renamed names weren't recorded as taken, so a rename could collide
//     with a name another param already declared (duplicate flag.String
//     registration panics at runtime).
//
// Emission-time naming (where.go) guarantees names are unique WITHIN a
// fragment; collectParams owns uniqueness ACROSS fragments.
func TestCollectParamsRename(t *testing.T) {
	t.Run("cross-fragment rename skips taken suffixes", func(t *testing.T) {
		// Fragment 1 declares pop-gt AND pop-gt2 itself (duplicate field+op
		// conditions get numbered names at emission). Fragment 2's pop-gt
		// must rename past the taken suffix to pop-gt3.
		frag1 := &CodeFragment{
			Code: "return *flagPopGt > 1 && *flagPopGt2 > 2",
			Params: []CodeParam{
				{Name: "pop-gt", VarName: "flagPopGt", Default: "1"},
				{Name: "pop-gt2", VarName: "flagPopGt2", Default: "2"},
			},
		}
		frag2 := &CodeFragment{
			Code: "return *flagPopGt > 3",
			Params: []CodeParam{
				{Name: "pop-gt", VarName: "flagPopGt", Default: "3"},
			},
		}

		params := collectParams([]*CodeFragment{frag1, frag2})

		names := make(map[string]bool)
		for _, p := range params {
			if names[p.Name] {
				t.Fatalf("duplicate flag name %q — flag.String would panic at runtime", p.Name)
			}
			names[p.Name] = true
		}
		if !names["pop-gt"] || !names["pop-gt2"] || !names["pop-gt3"] {
			t.Errorf("expected names pop-gt, pop-gt2, pop-gt3; got %v", names)
		}
		// Fragment 1's references must be untouched — its names were unique.
		if frag1.Code != "return *flagPopGt > 1 && *flagPopGt2 > 2" {
			t.Errorf("fragment 1 code was rewritten: %s", frag1.Code)
		}
		if frag2.Code != "return *flagPopGt3 > 3" {
			t.Errorf("fragment 2 reference not renamed to flagPopGt3: %s", frag2.Code)
		}
	})

	t.Run("rename does not corrupt longer names by prefix", func(t *testing.T) {
		// Renaming *flagPopGt in a fragment that ALSO references *flagPopGt2
		// must leave the longer name alone (the old ReplaceAll produced
		// *flagPopGt32, which is undeclared).
		frag1 := &CodeFragment{
			Code:   "return *flagPopGt > 1",
			Params: []CodeParam{{Name: "pop-gt", VarName: "flagPopGt", Default: "1"}},
		}
		frag2 := &CodeFragment{
			Code: "return *flagPopGt > 3 && *flagPopGt2 > 4",
			Params: []CodeParam{
				{Name: "pop-gt", VarName: "flagPopGt", Default: "3"},
				{Name: "pop-gt2", VarName: "flagPopGt2", Default: "4"},
			},
		}

		params := collectParams([]*CodeFragment{frag1, frag2})

		if strings.Contains(frag2.Code, "flagPopGt32") {
			t.Fatalf("prefix corruption: %s", frag2.Code)
		}
		// frag2's pop-gt renames to the first free suffix (pop-gt3: pop-gt2
		// is taken by frag2's own second param); its pop-gt2 in turn renames.
		if !strings.Contains(frag2.Code, "*flagPopGt3 > 3") {
			t.Errorf("fragment 2 pop-gt reference not renamed cleanly: %s", frag2.Code)
		}
		var varNames []string
		for _, p := range params {
			varNames = append(varNames, p.VarName)
		}
		declared := make(map[string]bool)
		for _, v := range varNames {
			declared[v] = true
		}
		// Every *flagX reference in every fragment must be a declared var.
		for _, frag := range []*CodeFragment{frag1, frag2} {
			for _, tok := range strings.Fields(frag.Code) {
				if strings.HasPrefix(tok, "*flag") {
					if !declared[strings.TrimPrefix(tok, "*")] {
						t.Errorf("reference %s has no declaration (declared: %v); code: %s",
							tok, varNames, frag.Code)
					}
				}
			}
		}
	})
}

// The generated main must turn a reader's fail-fast panic
// (*ssql.CellError, DFC124 §3) into "Error: …" + exit 1, never a stack
// trace — the same shape as a returned error.
func TestWriteMainCallingRunRecoversErrorPanics(t *testing.T) {
	var b strings.Builder
	writeMainCallingRun(&b, true)
	got := b.String()
	for _, want := range []string{"recover()", `r.(error)`, `fmt.Fprintln(os.Stderr, "Error:", err)`, "os.Exit(1)", "panic(r)", "flag.Parse()", "func run() error {"} {
		if !strings.Contains(got, want) {
			t.Errorf("main lacks %q:\n%s", want, got)
		}
	}
}
