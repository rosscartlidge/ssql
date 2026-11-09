package commands

import "testing"

// TestPreprocessScript: a script's lines become one bash command line.
// A backslash continuation is dropped before the join; kept, it escaped
// the joining space and a stage received " -set-expr" as an argument.
func TestPreprocessScript(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"trailing pipe", "ssql from csv a.csv |\n  ssql to csv", "ssql from csv a.csv | ssql to csv"},
		{"leading pipe", "ssql from csv a.csv\n  | ssql to csv", "ssql from csv a.csv | ssql to csv"},
		{"backslash continuation", "ssql update -set a 1 \\\n    -set b 2 |\n  ssql to csv", "ssql update -set a 1 -set b 2 | ssql to csv"},
		{"leading pipe after a backslash", "ssql from csv a.csv \\\n  | ssql to csv", "ssql from csv a.csv | ssql to csv"},
		{"escaped backslash ends the line", `ssql where -if s eq \\` + "\n| ssql to csv", `ssql where -if s eq \\ | ssql to csv`},
		{"backslash inside single quotes", "ssql update -set-expr a 'x \\'\n| ssql to csv", "ssql update -set-expr a 'x \\' | ssql to csv"},
		{"comment", "ssql from csv a.csv | # the source\n  ssql to csv", "ssql from csv a.csv | ssql to csv"},
		{"hash in quotes", "ssql where -if-expr 'tag == \"#x\"' |\n ssql to csv", "ssql where -if-expr 'tag == \"#x\"' | ssql to csv"},
	} {
		if got := preprocessScript(c.src); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
