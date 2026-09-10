package commands

import (
	"strings"
	"testing"
)

// TestSplitPipeline: the shell line is divided into what runs before the
// ssql stages, the ssql stages, and what runs after — so Alt-r compiles
// only the middle and the user's pager/redirect/producer stay in place.
func TestSplitPipeline(t *testing.T) {
	cases := []struct{ line, prefix, seg, suffix string }{
		{"ssql from a.csv | ssql to table", "", "ssql from a.csv | ssql to table", ""},
		{"ssql from a.csv | ssql to table | less", "", "ssql from a.csv | ssql to table", "| less"},
		{"ssql from a.csv | ssql to table | head -3 | wc -l", "", "ssql from a.csv | ssql to table", "| head -3 | wc -l"},
		{"cat x.csv | ssql from csv - | ssql count | tee n.txt", "cat x.csv |", "ssql from csv - | ssql count", "| tee n.txt"},
		{"ssql from a.csv | ssql to table > out.txt", "", "ssql from a.csv | ssql to table", "> out.txt"},
		{"ssql from a.csv | ssql to table >> out.txt", "", "ssql from a.csv | ssql to table", ">> out.txt"},
		{"ssql from a.csv | ssql count 2> err.log | less", "", "ssql from a.csv | ssql count", "2> err.log | less"},
		// quotes and process substitution are not stage boundaries
		{"ssql from a.csv | ssql where -if-expr 'a > 1 || b < 2' | ssql to table", "", "ssql from a.csv | ssql where -if-expr 'a > 1 || b < 2' | ssql to table", ""},
		{"ssql from a.csv | ssql join <(ssql from b.csv | ssql where -if x gt 1) -using id | ssql count", "", "ssql from a.csv | ssql join <(ssql from b.csv | ssql where -if x gt 1) -using id | ssql count", ""},
		{`ssql from a.csv | ssql update -set-expr s '"a|b"' | ssql to table`, "", `ssql from a.csv | ssql update -set-expr s '"a|b"' | ssql to table`, ""},
		{"zcat big.csv.gz | ssql from csv - | ssql count", "zcat big.csv.gz |", "ssql from csv - | ssql count", ""},
		{"ssql_gpu from a.csv | ssql_gpu fft -field v | less", "", "ssql_gpu from a.csv | ssql_gpu fft -field v", "| less"},
	}
	for _, c := range cases {
		prefix, seg, suffix, err := SplitPipeline(c.line)
		if err != nil {
			t.Errorf("%q: %v", c.line, err)
			continue
		}
		if prefix != c.prefix || seg != c.seg || suffix != c.suffix {
			t.Errorf("%q:\n  got  %q | %q | %q\n  want %q | %q | %q", c.line, prefix, seg, suffix, c.prefix, c.seg, c.suffix)
		}
	}
	for _, line := range []string{"ls | grep x", "ssql from a.csv | sort | ssql count"} {
		if _, _, _, err := SplitPipeline(line); err == nil {
			t.Errorf("%q should not split", line)
		}
	}
	if _, _, _, err := SplitPipeline("ssql from a.csv | sort | ssql count"); err == nil || !strings.Contains(err.Error(), `"sort"`) {
		t.Errorf("the interrupting stage should be named: %v", err)
	}
}
