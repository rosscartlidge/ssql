package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Generated programs the tests build link the whole ssql library: arrow,
// parquet, excelize, and grpc through arrow's pqarrow → flight, 44 MB and
// ~1.4 s a link, where the slim build links 5.8 MB in ~0.35 s. The suite
// links ~2,500 of them, and a link was running for 1,064 s of its 1,475 s
// wall (2026-11-11, a GOFLAGS=-toolexec log). So a program that reaches
// nothing the slim build leaves out is built slim.
//
// The choice is the source's: it names an identifier one of the
// slim-tagged files of the ssql or typed package defines (ReadParquet,
// WriteArrow, ReadXLSX, OpenHTTPStream, typed's parquet readers…), or a
// URL or a parquet/arrow/xlsx path that a generic reader would dispatch
// on. A wrong guess costs time, not a result: a slim build that does not
// compile, or a program that prints a slim refusal, is built again in
// full and that run is the answer (goBuildRunGenerated).
// SSQL_TEST_FULL_BUILD=1 builds every program in full.

var (
	slimOnlyOnce sync.Once
	slimOnlyRE   *regexp.Regexp
	slimOnlyErr  error
)

// slimDifferences is the regexp of what a slim build of a generated
// program cannot be trusted with.
func slimDifferences(t *testing.T) *regexp.Regexp {
	t.Helper()
	slimOnlyOnce.Do(func() {
		var alts []string
		for _, pkg := range []struct{ dir, name string }{{"../..", "ssql"}, {"../../typed", "typed"}} {
			names := map[string]bool{}
			files, err := filepath.Glob(filepath.Join(pkg.dir, "*.go"))
			if err != nil {
				slimOnlyErr = err
				return
			}
			for _, f := range files {
				if strings.HasSuffix(f, "_test.go") {
					continue
				}
				src, err := os.ReadFile(f)
				if err != nil {
					slimOnlyErr = err
					return
				}
				if !slimConstrained(string(src)) {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), f, src, parser.SkipObjectResolution)
				if err != nil {
					slimOnlyErr = err
					return
				}
				for _, d := range file.Decls {
					switch d := d.(type) {
					case *ast.FuncDecl:
						if d.Recv == nil && d.Name.IsExported() {
							names[d.Name.Name] = true
						}
					case *ast.GenDecl:
						for _, s := range d.Specs {
							switch s := s.(type) {
							case *ast.TypeSpec:
								if s.Name.IsExported() {
									names[s.Name.Name] = true
								}
							case *ast.ValueSpec:
								for _, n := range s.Names {
									if n.IsExported() {
										names[n.Name] = true
									}
								}
							}
						}
					}
				}
			}
			if len(names) == 0 {
				slimOnlyErr = fmt.Errorf("%s: no slim-tagged file (a moved package, not a fast suite)", pkg.dir)
				return
			}
			quoted := make([]string, 0, len(names))
			for n := range names {
				quoted = append(quoted, regexp.QuoteMeta(n))
			}
			alts = append(alts, `\b`+pkg.name+`\.(?:`+strings.Join(quoted, "|")+`)\b`)
		}
		slimOnlyRE = regexp.MustCompile(strings.Join(alts, "|") + `|https?://|\.(?:parquet|arrow|feather|ipc|xlsx)\b`)
	})
	if slimOnlyErr != nil {
		t.Fatalf("scanning the slim-tagged files: %v", slimOnlyErr)
	}
	return slimOnlyRE
}

// slimConstrained reports whether a Go file's build constraint names the
// slim tag.
func slimConstrained(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build") {
			return regexp.MustCompile(`\bslim\b`).MatchString(line)
		}
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}

// buildSlim reports whether a generated program's files can be built
// with -tags slim.
func buildSlim(t *testing.T, files map[string]string) bool {
	t.Helper()
	if os.Getenv("SSQL_TEST_FULL_BUILD") != "" {
		return false
	}
	re := slimDifferences(t)
	for _, src := range files {
		if re.MatchString(src) {
			return false
		}
	}
	return true
}

// slimRefused reports whether a program's output is the slim build's
// refusal of something it leaves out.
func slimRefused(out string) bool {
	return strings.Contains(out, "not available in slim build") || strings.Contains(out, "not in this build (slim")
}

// TestSlimBuildChoice: the generated-program helper builds slim only when
// the source reaches nothing slim leaves out, and the scan of the
// slim-tagged files finds the names it must.
func TestSlimBuildChoice(t *testing.T) {
	for _, tc := range []struct {
		src  string
		slim bool
	}{
		{`for r := range ssql.ReadCSV(input) {}`, true},
		{`recs, err := ssql.ReadParquet(path)`, false},
		{`err := ssql.WriteArrow(recs, out)`, false},
		{`rows, err := typed.ReadParquet[Row](path)`, false},
		{`rows := typed.ReadParquetFiles[Row](paths, typed.ParquetStrict())`, false}, // typed's own names
		{`x := typed.ReadXLSX(a)`, true},                                             // ssql's name, not typed's
		{`f, err := ssql.OpenHTTPStream(u)`, false},
		{`input := "https://example.com/a.csv"`, false},
		{`input := "/data/sales.parquet"`, false},
		{`x := ssql.ReadParquetish(a)`, true}, // a longer name is not the identifier
	} {
		if got := buildSlim(t, map[string]string{"main.go": tc.src}); got != tc.slim {
			t.Errorf("buildSlim(%q) = %v, want %v", tc.src, got, tc.slim)
		}
	}
}

// TestSlimBuildFallsBackToFull: a program that reaches what slim leaves
// out without naming it (a URL assembled at run time, so the source scan
// cannot see it) is refused by the slim build, and the helper builds it
// again in full: the run the test sees is the full build's.
func TestSlimBuildFallsBackToFull(t *testing.T) {
	if testing.Short() {
		t.Skip("builds generated programs")
	}
	src := `package main

import (
	"fmt"

	"github.com/rosscartlidge/ssql/v4"
)

func main() {
	u := "ht" + "tp://127.0.0.1:1/x.csv"
	_, err := ssql.OpenInput(u)
	fmt.Println(err)
}
`
	if !buildSlim(t, map[string]string{"main.go": src}) {
		t.Fatal("the scan saw the URL: the test no longer reaches the fallback")
	}
	out, err := goRunGeneratedModule(t, "slimfallback", map[string]string{"main.go": src})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if slimRefused(out) || !strings.Contains(out, "127.0.0.1:1") {
		t.Errorf("want the full build's connection error, got\n%s", out)
	}
}
