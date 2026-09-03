package main

// The SSH operator console (cmd/ssql/commands/serve_cli.go) has its
// own command list. This pins it against the CLI's: every command in
// cmd/ssql/main.go must be either registered in the console or named
// below with the REASON it is excluded. A new CLI command therefore
// fails here until someone decides — the console silently missed five
// new verbs (describe, unpivot, fill, extract, resample) in one week
// before this test existed.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// consoleExcluded: CLI commands deliberately NOT in the SSH console.
var consoleExcluded = map[string]string{
	"From":        "replaced by from-loaded — the console serves one in-memory dataset",
	"Join":        "reads server-side file paths (v1 exclusion; see serve_cli.go comment)",
	"Merge":       "reads server-side file paths (v1 exclusion)",
	"Union":       "reads server-side file paths (v1 exclusion)",
	"Tee":         "writes server-side files",
	"To":          "console registers its own stream-only subset of `to` (no file-writing sinks)",
	"Generate":    "codegen is a CLI/dev workflow, not an operator action",
	"Serve":       "cannot serve from inside a session",
	"Version":     "meta — not a pipeline stage",
	"Functions":   "meta — expression function reference (could be added)",
	"Conventions": "meta — could be added",
}

func TestServeConsoleRegistration(t *testing.T) {
	cliCalls, err := registrationCalls("main.go")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("commands/serve_cli.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\bRegister([A-Za-z0-9_]+)\(b\)`)
	console := map[string]bool{}
	for _, m := range re.FindAllSubmatch(data, -1) {
		console[string(m[1])] = true
	}
	if len(console) == 0 {
		t.Fatal("console registration regex matched nothing — pattern out of date")
	}
	var undecided, stale []string
	for _, c := range cliCalls {
		_, excluded := consoleExcluded[c]
		if !console[c] && !excluded {
			undecided = append(undecided, c)
		}
		if console[c] && excluded {
			stale = append(stale, c)
		}
	}
	for c := range consoleExcluded {
		found := false
		for _, cli := range cliCalls {
			if cli == c {
				found = true
			}
		}
		if !found {
			stale = append(stale, c+" (not a CLI command any more)")
		}
	}
	sort.Strings(undecided)
	if len(undecided) > 0 {
		t.Errorf("CLI commands neither registered in the SSH console (serve_cli.go) nor excluded with a reason:\n  %s\nAdd `b = Register<X>(b)` to buildServeCLI, or add <X> to consoleExcluded with the reason.", strings.Join(undecided, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("stale consoleExcluded entries: %v", stale)
	}
}
