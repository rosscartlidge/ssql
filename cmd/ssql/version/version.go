package version

import (
	_ "embed"
	"runtime/debug"
	"strings"
)

// Version is the current version of ssql, embedded from version.txt.
var Version = strings.TrimSpace(gitVersion)

//go:embed version.txt
var gitVersion string

// Commit is the short git commit hash.
// Set by the linker (-X ...version.Commit=HASH) for release artifacts
// such as the .deb, whose build tree is necessarily dirty (the target
// removes the old packages first). Otherwise detected at init:
// Go's built-in VCS info (local builds, shows -dirty), then commit.txt
// (go install from the module proxy).
var Commit string

// Local is true when this binary was built from a checkout (Go's VCS
// info named the revision) rather than installed from the module proxy
// or stamped by a release build: its generated code may need the
// checkout's module (SSQL_MODULE_DIR) rather than the released one.
var Local bool

//go:embed commit.txt
var gitCommit string

func init() {
	if Commit == "" {
		Commit = detectCommit()
	}
}

func detectCommit() string {
	// Try Go's built-in VCS info first (local builds)
	if info, ok := debug.ReadBuildInfo(); ok {
		var revision string
		var dirty bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if revision != "" {
			Local = true
			short := revision
			if len(short) > 8 {
				short = short[:8]
			}
			if dirty {
				short += "-dirty"
			}
			return short
		}
	}

	// Fallback to embedded commit.txt (module proxy installs)
	if c := strings.TrimSpace(gitCommit); c != "" {
		return c
	}

	return "dev"
}
