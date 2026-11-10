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
		// bash substitutes inside double quotes, and `\'` opens no quote.
		`ssql from csv a.csv | ssql where -if x eq "$(rm x)"`:     false,
		"ssql from csv a.csv | ssql where -if x eq \"`rm x`\"":    false,
		`ssql from csv a.csv | ssql where -if x eq \' $(rm x) \'`: false,
		`ssql from csv a.csv | ssql where -if x eq "a\"b"`:        true,
		`ssql from csv a.csv | ssql where -if x eq 'a$(b)'`:       true,
		`ssql from csv a.csv | ssql where -if x eq a\|b`:          true,
	} {
		if got := upstreamSafeToRun(src); got != want {
			t.Errorf("upstreamSafeToRun(%q) = %v, want %v", src, got, want)
		}
	}
}

// TestPipelineSafeToDescribe: fragment mode writes no sink, so a sink
// naming a file may be described; the shell rules are the same.
func TestPipelineSafeToDescribe(t *testing.T) {
	for src, want := range map[string]bool{
		"ssql from csv a.csv | ssql to csv out.csv":                          true,
		"ssql from csv a.csv | ssql tee t.jsonl | ssql to parquet o.parquet": true,
		"ssql from csv a.csv | ssql generate go":                             false,
		"ssql serve":                                                         false,
		`ssql from csv a.csv | ssql where -if x eq "$(rm x)"`:                false,
		"ssql from csv a.csv | ssql to csv out.csv > x":                      false,
		"ssql from csv a.csv | less":                                         false,
	} {
		if got := pipelineSafeToDescribe(src); got != want {
			t.Errorf("pipelineSafeToDescribe(%q) = %v, want %v", src, got, want)
		}
	}
}
