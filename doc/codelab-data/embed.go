// Package codelabdata embeds the fixture files every example in
// doc/cli-codelab.md runs against, so an installed ssql can write them
// out with `ssql codelab DIR` — the tutorial used to ask readers to
// clone the whole repository (gigabytes of history and baked artifacts)
// for 44 KB of CSV.
//
// The checked-in files in this directory ARE the embedded ones: the
// codelab runner (scripts/codelab-run.sh) and the tests read the same
// bytes the binary ships, so the two cannot drift.
package codelabdata

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Files holds the fixture files (data plus the README that describes
// them). embed.go itself is deliberately not included.
//
//go:embed *.csv *.parquet *.log README.md
var Files embed.FS

// Names lists the embedded fixture files, sorted.
func Names() []string {
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		panic(fmt.Sprintf("codelabdata: read embedded dir: %v", err))
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// Write copies every fixture file into dir, creating it if needed, and
// returns the paths written. An existing file is never overwritten
// unless force is set: the error names every file that would have been
// clobbered so a reader who has edited the fixtures loses nothing by
// accident (the first codelab baseline run overwrote employees.csv).
func Write(dir string, force bool) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	names := Names()
	if !force {
		var clash []string
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				clash = append(clash, name)
			}
		}
		if len(clash) > 0 {
			return nil, fmt.Errorf("%s already holds %v; use -force to overwrite, or choose another directory", dir, clash)
		}
	}
	written := make([]string, 0, len(names))
	for _, name := range names {
		data, err := Files.ReadFile(name)
		if err != nil {
			return written, err
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}
