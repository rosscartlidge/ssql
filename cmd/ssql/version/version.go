package version

import (
	_ "embed"
	"strings"
)

// Version is the current version of ssql.
// This is embedded from version.txt which is generated from git describe.
var Version = strings.TrimSpace(gitVersion)

//go:embed version.txt
var gitVersion string

// Commit is the short git commit hash, set at build time via:
//
//	go build -ldflags "-X github.com/rosscartlidge/ssql/v4/cmd/ssql/version.Commit=$(git rev-parse --short=8 HEAD)"
//
// Falls back to "dev" if not set.
var Commit = "dev"
