package ssql

// DFC142 step 2: every value the library panics with is an error, so one
// recover (ssql.Recover) converts any pipeline panic uniformly and
// errors.As works on the typed ones. This is a mechanical rule over the
// sources, like scripts/api-coverage.sh: a `panic("…")` or
// `panic(fmt.Sprintf(…))` anywhere in the root, typed or lib packages
// fails the build's tests. Panics with error values (`panic(err)`,
// `panic(fmt.Errorf(…))`, `panic(&CellError{…})`) are fine.

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPanicValuesAreErrors(t *testing.T) {
	var bad []string
	for _, dir := range []string{".", "typed", filepath.Join("cmd", "ssql", "lib")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			fh, err := os.Open(f)
			if err != nil {
				t.Fatal(err)
			}
			sc := bufio.NewScanner(fh)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			for n := 1; sc.Scan(); n++ {
				line := strings.TrimSpace(sc.Text())
				if strings.HasPrefix(line, "//") {
					continue
				}
				if strings.HasPrefix(line, `panic(fmt.Sprintf(`) || strings.HasPrefix(line, `panic("`) {
					bad = append(bad, f+":"+strconv.Itoa(n)+": "+line)
				}
			}
			fh.Close()
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d panic site(s) do not panic with an error value (wrap in fmt.Errorf / errors.New):\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}
