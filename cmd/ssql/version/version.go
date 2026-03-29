package version

import (
	_ "embed"
	"strings"
)

// Version is the current version of ssql, embedded from version.txt.
var Version = strings.TrimSpace(gitVersion)

//go:embed version.txt
var gitVersion string

// Commit is the short git commit hash, embedded from commit.txt.
// Updated before each release tag. Falls back to "dev" if file is empty.
var Commit = func() string {
	c := strings.TrimSpace(gitCommit)
	if c == "" {
		return "dev"
	}
	return c
}()

//go:embed commit.txt
var gitCommit string
