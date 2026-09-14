package ssql

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	"golang.org/x/mod/zip"
)

// gitFile adapts a tracked path to zip.File so zip.CheckFiles can apply
// the exact rules `go install …@vX.Y.Z` applies when it builds the module
// zip from a tag.
type gitFile string

func (f gitFile) Path() string                 { return string(f) }
func (f gitFile) Lstat() (os.FileInfo, error)  { return os.Lstat(string(f)) }
func (f gitFile) Open() (io.ReadCloser, error) { return os.Open(string(f)) }

// TestModuleZipPaths: every tracked file must be acceptable to the Go
// module zip. v4.98.0 was un-installable — `create zip: … malformed file
// path "doc/codelab-data/select 1 as a, 'x' as b": invalid char '\”` —
// because a duckdb invocation had taken its SQL as a database filename
// and `git add doc/` swept the result in. Nothing in the test suites,
// doc-check or the build noticed: the file was not embedded and compiled
// fine locally; only the proxy's zip step rejected it, after the
// immutable tag was pushed. This runs the same check before the tag.
func TestModuleZipPaths(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	// Every tracked PATH is checked by name; only files present on disk go
	// through the content/size checks — mid-release the worktree can have
	// tracked-but-deleted files (make deb removes the previous debs before
	// the bake commit), and the proxy zips the commit, not the worktree.
	var files []zip.File
	n := 0
	for _, p := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if p == "" {
			continue
		}
		n++
		if err := module.CheckFilePath(p); err != nil {
			t.Errorf("tracked path cannot go in the module zip (go install @tag would fail): %v", err)
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			files = append(files, gitFile(p))
		}
	}
	if n == 0 {
		t.Skip("no tracked files listed")
	}
	cf, err := zip.CheckFiles(files)
	if err != nil {
		t.Fatalf("zip.CheckFiles: %v", err)
	}
	for _, e := range cf.Invalid {
		t.Errorf("tracked file cannot go in the module zip (go install @tag would fail): %v", e)
	}
	if cf.SizeError != nil {
		t.Errorf("module zip size: %v", cf.SizeError)
	}
}
