package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestRemoteReachCoversSSHCallers: every file in the package that makes
// an ssh connection is named by a remoteReach row, so the language
// server's "nothing remote while typing" rule cannot miss a command
// added later.
func TestRemoteReachCoversSSHCallers(t *testing.T) {
	callRe := regexp.MustCompile(`ssql\.BuildRemoteCommand\(|ssql\.RemoteScriptCommand\(|exec\.Command(Context)?\("ssh"`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var covered []string
	for _, r := range remoteReach {
		covered = append(covered, r.Files...)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if callRe.Match(src) {
			found++
			if _, local := remoteReachLocal[f]; !slices.Contains(covered, f) && !local {
				t.Errorf("%s connects over ssh but no remoteReach row names it (remote_reach.go)", f)
			}
		}
	}
	if found == 0 {
		t.Fatal("no ssh caller found — the pattern or the layout changed")
	}
}

func TestTextReachesRemote(t *testing.T) {
	for text, want := range map[string]bool{
		"ssql from csv a.csv | ssql to csv":                                  false,
		"ssql from ssh host:/data/a.csv | ssql to csv":                       true,
		"ssql from catalog shards.csv | ssql count":                          true,
		"ssql merge -catalog shards.csv -by ts":                              true,
		"ssql merge a.csv b.csv -by ts":                                      false,
		"ssql from csv https://example.com/a.csv | ssql to csv":              true,
		"ssql from csv a.csv | ssql join <(ssql from ssh h:/k.csv) -using k": true,
		// A URL is taken as fetched whichever argument holds it: which
		// arguments are files is the command's grammar, so err towards not
		// connecting.
		"ssql from csv a.csv | ssql where -if url eq 'https://example.com'": true,
		"ssql from csv a.csv | ssql where -if-expr 'name == \"from ssh\"'":  false,
	} {
		if _, why := textReachesRemote(text); (why != "") != want {
			t.Errorf("%q: %q, want remote %v", text, why, want)
		}
	}
}
