package commands

import "testing"

// TestUpstreamSafeToRun: a keypress runs only ssql commands that write
// nothing; a quoted operator is data, an unquoted one is the shell's.
func TestUpstreamSafeToRun(t *testing.T) {
	for src, want := range map[string]bool{
		"ssql from csv a.csv | ssql where -if x eq 1":                                  true,
		"ssql from csv a.csv | ssql join <(ssql from csv k.csv) -using k":              true,
		"ssql from csv a.csv | ssql where -if-expr 'x > 3 && y < 2'":                   true,
		"ssql_gpu from csv a.csv | ssql update -set t 1":                               true,
		"ssql from csv a.csv | ssql tee out.jsonl":                                     false,
		"ssql from csv a.csv | ssql join <(ssql from csv k.csv | ssql tee x) -using k": false,
		"ssql from csv a.csv | ssql to csv out.csv":                                    false,
		"cat a.csv | ssql from csv -":                                                  false,
		"ssql from csv a.csv > x":                                                      false,
		"ssql from csv $(rm -rf x)":                                                    false,
		"ssql from csv `x`":                                                            false,
		"ssql from csv a.csv; rm x":                                                    false,
		"ssql from csv a.csv | ssql where -if x eq 'open":                              false,
		"ssql generate go -run -pipeline 'x'":                                          false,
	} {
		if got := upstreamSafeToRun(src); got != want {
			t.Errorf("upstreamSafeToRun(%q) = %v, want %v", src, got, want)
		}
	}
}
