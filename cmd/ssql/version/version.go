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
// Primary: Go's built-in VCS info (works for local builds from git repo).
// Fallback: commit.txt (works for go install from module proxy).
var Commit = detectCommit()

//go:embed commit.txt
var gitCommit string

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
