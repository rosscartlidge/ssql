package ssql

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandFilePatterns turns a source's file arguments into the files
// they name, in order: a glob pattern (`*`, `?`, `[…]`) becomes its
// matches in sorted order, anything else is kept as given. The shell
// expands an unquoted glob before ssql sees it; a quoted one, a run
// document's (which has no shell) and a generated program's arrive
// here, so the pattern is expanded when the pipeline runs — new files
// that match are read, as DuckDB's read_parquet('…/*.parquet') does.
//
// A URL is never expanded (an http server has no listing). A pattern
// that matches nothing is an error, never an empty read, and so is
// `**`: Go's matcher reads it as `*`, which would silently skip the
// deeper directories DuckDB's recursive `**` reads.
func ExpandFilePatterns(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		if IsHTTPURL(a) || !strings.ContainsAny(a, "*?[") {
			out = append(out, a)
			continue
		}
		if _, err := os.Stat(a); err == nil {
			out = append(out, a) // a file whose name holds a metacharacter
			continue
		}
		if strings.Contains(a, "**") {
			return nil, fmt.Errorf("%s: recursive ** is not supported — name each directory level with *", a)
		}
		matches, err := filepath.Glob(a)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("%s matches no files", a)
		}
		out = append(out, matches...) // filepath.Glob sorts
	}
	return out, nil
}
