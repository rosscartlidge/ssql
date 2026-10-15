package main

// Dependency drift gate for the slim build (DFC151). The slim wasm is
// what every playground visitor downloads and what every `to explore`
// page carries; a third of it was net/http (with TLS and x509) that
// nothing a browser can reach needed, pulled in by one ungated file.
// This is the registration-drift idea applied to dependencies: `go
// list -deps -tags slim` for both entry points must not contain a
// forbidden package, and a failure names the import chain that brought
// it in, so the file to gate is the first line of the message.

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// slimForbidden are the packages the slim build exists to leave out:
// the Arrow/Parquet and xlsx trees (the original reason), net/http
// with TLS (DFC151) and go/format (library-mode gofmt, never reached
// in a browser). A prefix match, so every sub-package counts.
var slimForbidden = []string{
	"net/http",
	"crypto/tls",
	"crypto/x509",
	"go/format",
	"github.com/apache/arrow-go",
	"github.com/apache/arrow/go",
	"github.com/xuri/excelize",
}

func TestSlimBuildDependencies(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	for _, target := range []struct{ goos, goarch, pkg string }{
		{"js", "wasm", "./cmd/ssql-playground"},
		{"wasip1", "wasm", "./cmd/ssql"},
		{"linux", "amd64", "./cmd/ssql"},
	} {
		t.Run(target.goos+"/"+target.pkg, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", "-tags", "slim", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", target.pkg)
			cmd.Dir = "../.."
			cmd.Env = append(cmd.Environ(), "GOOS="+target.goos, "GOARCH="+target.goarch)
			out, err := cmd.Output()
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					t.Fatalf("go list: %v\n%s", err, ee.Stderr)
				}
				t.Fatalf("go list: %v", err)
			}
			imports := map[string][]string{}
			var root string
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				imports[fields[0]] = fields[1:]
				root = fields[0] // -deps lists dependencies first, the package itself last
			}
			var offenders []string
			for pkg := range imports {
				for _, f := range slimForbidden {
					if pkg == f || strings.HasPrefix(pkg, f+"/") {
						offenders = append(offenders, pkg)
						break
					}
				}
			}
			sort.Strings(offenders)
			for _, pkg := range offenders {
				t.Errorf("slim build of %s links %s:\n  %s", target.pkg, pkg, strings.Join(importChain(imports, root, pkg), "\n  -> "))
			}
		})
	}
}

// importChain is the shortest import path from root to target over the
// `go list -deps` graph (BFS), as a list of package paths.
func importChain(imports map[string][]string, root, target string) []string {
	prev := map[string]string{root: ""}
	queue := []string{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			var chain []string
			for p := cur; p != ""; p = prev[p] {
				chain = append([]string{p}, chain...)
			}
			return chain
		}
		for _, next := range imports[cur] {
			if _, seen := prev[next]; !seen {
				prev[next] = cur
				queue = append(queue, next)
			}
		}
	}
	return []string{target + " (no chain found)"}
}
