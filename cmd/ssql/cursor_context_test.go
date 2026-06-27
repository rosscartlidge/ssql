package main

import "testing"

func TestCompleteSource(t *testing.T) {
	cases := []struct {
		name   string
		before string
		want   string
	}{
		{
			"plain downstream field position",
			"ssql from data.csv | ssql group-by ",
			"ssql from data.csv",
		},
		{
			"multi-stage upstream is the whole pipeline before the cursor stage",
			"ssql from data.csv | ssql rename -as a b | ssql group-by ",
			"ssql from data.csv | ssql rename -as a b",
		},
		{
			"first stage has no upstream",
			"ssql from data.csv ",
			"",
		},
		{
			"simple procsub does NOT confuse the top-level split (join left field)",
			"ssql from data.csv | ssql join <(ssql from kind.csv) -on ",
			"ssql from data.csv",
		},
		{
			"pipe INSIDE a procsub is not a top-level pipe (join left field)",
			"ssql from data.csv | ssql join <(ssql from kind.csv | ssql group-by kind) -on ",
			"ssql from data.csv",
		},
		{
			"cursor inside a procsub uses the procsub's internal upstream",
			"ssql from data.csv | ssql join <(ssql from kind.csv | ssql group-by ",
			"ssql from kind.csv",
		},
		{
			"cursor inside a procsub with no inner pipe has no source",
			"ssql from data.csv | ssql join <(ssql from kind.csv ",
			"",
		},
		{
			"join right field (-on 2nd arg) completes from the procsub",
			"ssql from data.csv | ssql join <(ssql from kind.csv) -on a_kind ",
			"ssql from kind.csv",
		},
		{
			"join right field (-on 2nd arg) while typing",
			"ssql from data.csv | ssql join <(ssql from kind.csv) -on a_kind ki",
			"ssql from kind.csv",
		},
		{
			"join -as 1st arg is a right field → procsub",
			"ssql from data.csv | ssql join <(ssql from kind.csv) -on a_kind kind -as ",
			"ssql from kind.csv",
		},
		{
			"join -as 2nd arg (new name) is NOT a right field → upstream",
			"ssql from data.csv | ssql join <(ssql from kind.csv) -on a_kind kind -as kind_name ",
			"ssql from data.csv",
		},
		{
			"join right field after a clause separator",
			"ssql from data.csv | ssql join <(ssql from kind.csv) -on a_kind kind - -on z_kind ",
			"ssql from kind.csv",
		},
		{
			"procsub with inner pipe, right-field slot",
			"ssql from data.csv | ssql join <(ssql from kind.csv | ssql rename -as kind k) -on a_kind ",
			"ssql from kind.csv | ssql rename -as kind k",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completeSource(tc.before); got != tc.want {
				t.Errorf("completeSource(%q)\n  got  %q\n  want %q", tc.before, got, tc.want)
			}
		})
	}
}

func TestExprArgAtCursor(t *testing.T) {
	// args is COMP_WORDS minus the program name (args[0] = command); pos is the
	// COMP_WORDS index of the cursor (program at 0).
	cases := []struct {
		name string
		args []string
		pos  int
		want bool
	}{
		{"where -if-expr expr", []string{"where", "-if-expr", ""}, 3, true},
		{"where -if-expr short -x expr", []string{"where", "-x", ""}, 3, true},
		{"where -if FIELD (not expr)", []string{"where", "-if", ""}, 3, false},
		{"update -set-expr field (not expr)", []string{"update", "-set-expr", ""}, 3, false},
		{"update -set-expr field EXPR", []string{"update", "-set-expr", "total", ""}, 4, true},
		{"update -set-expr short -e EXPR", []string{"update", "-e", "total", ""}, 4, true},
		{"update -if-expr EXPR", []string{"update", "-if-expr", ""}, 3, true},
		{"group-by -expr EXPR", []string{"group-by", "-expr", ""}, 3, true},
		{"group-by -expr EXPR name (not expr)", []string{"group-by", "-expr", "sum(x)", ""}, 4, false},
		{"group-by -sum FIELD (not expr)", []string{"group-by", "dept", "-sum", ""}, 4, false},
		{"group-by -stream-expr init", []string{"group-by", "-stream-expr", ""}, 3, true},
		{"group-by -stream-expr final", []string{"group-by", "-stream-expr", "{s:0}", "{s:s+x}", ""}, 5, true},
		{"group-by -stream-expr name (not expr)", []string{"group-by", "-stream-expr", "{s:0}", "{s:s+x}", "s", ""}, 6, false},
		{"cursor on the flag itself (not an arg)", []string{"update", "-set-expr"}, 2, false},
		{"after a clause separator resets", []string{"where", "-if", "a", "eq", "1", "+", "-if-expr", ""}, 8, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exprArgAtCursor(tc.args, tc.pos); got != tc.want {
				t.Errorf("exprArgAtCursor(%q, %d) = %v, want %v", tc.args, tc.pos, got, tc.want)
			}
		})
	}
}

func TestCursorTopLevelStage(t *testing.T) {
	cases := []struct {
		before, want string
	}{
		{"ssql from data.csv | ssql group-by dept -sum", "ssql group-by dept -sum"},
		{"ssql group-by dept ", "ssql group-by dept "}, // trailing space preserved
		{
			// pipe inside procsub must not split the top-level stage
			"ssql from x | ssql join <(ssql from k | ssql group-by k) -on a ",
			"ssql join <(ssql from k | ssql group-by k) -on a ",
		},
		{
			// cursor inside the procsub → stage is within it
			"ssql from x | ssql join <(ssql from k | ssql group-by ",
			"ssql group-by ",
		},
	}
	for _, tc := range cases {
		if got := cursorTopLevelStage(tc.before); got != tc.want {
			t.Errorf("cursorTopLevelStage(%q)\n  got  %q\n  want %q", tc.before, got, tc.want)
		}
	}
}
