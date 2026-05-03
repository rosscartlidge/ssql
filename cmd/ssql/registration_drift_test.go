package main

// Drift guard for the two top-level entry points. Both
// cmd/ssql/main.go (the regular CLI) and cmd/ssql-playground/main.go
// (the WASM browser playground) carry their own command-registration
// list, and they MUST stay in sync — otherwise commands silently
// disappear from the playground (this happened: count + signal
// commands sat in cmd/ssql for months without being registered in
// the playground).
//
// The test reads both files, extracts every `commands.Register<X>(cmd)`
// call, and asserts the two sets are equal. Failures name the
// specific commands that drifted.

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

func TestPlaygroundMainMatchesCLIRegistration(t *testing.T) {
	cliCalls, err := registrationCalls("main.go")
	if err != nil {
		t.Fatalf("read cmd/ssql/main.go: %v", err)
	}
	playgroundCalls, err := registrationCalls("../ssql-playground/main.go")
	if err != nil {
		t.Fatalf("read cmd/ssql-playground/main.go: %v", err)
	}

	if len(cliCalls) == 0 || len(playgroundCalls) == 0 {
		t.Fatalf("registration regex matched zero entries — pattern probably out of date (cli=%d, playground=%d)",
			len(cliCalls), len(playgroundCalls))
	}

	cliSet := toSet(cliCalls)
	playgroundSet := toSet(playgroundCalls)

	missingFromPlayground := setDiff(cliSet, playgroundSet)
	missingFromCLI := setDiff(playgroundSet, cliSet)

	if len(missingFromPlayground) > 0 {
		t.Errorf("commands registered in cmd/ssql/main.go but NOT in cmd/ssql-playground/main.go:\n  %s\n\nAdd matching `cmd = commands.Register<X>(cmd)` lines to cmd/ssql-playground/main.go (and rebuild + redeploy the WASM playground).",
			missingFromPlayground)
	}
	if len(missingFromCLI) > 0 {
		t.Errorf("commands registered in cmd/ssql-playground/main.go but NOT in cmd/ssql/main.go:\n  %s\n\nThis is unusual — the playground should be a subset (or equal) of the CLI. Add the missing line to cmd/ssql/main.go or remove it from the playground.",
			missingFromCLI)
	}
}

// registrationCalls extracts every `commands.Register<Name>(cmd)`
// call from the given file. Returns the set of <Name> identifiers
// (e.g. "Count", "Where", "FFT").
func registrationCalls(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`commands\.Register([A-Za-z0-9_]+)\(cmd\)`)
	matches := re.FindAllSubmatch(data, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, string(m[1]))
	}
	return out, nil
}

func toSet(xs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		out[x] = struct{}{}
	}
	return out
}

// setDiff returns elements of a not in b, sorted.
func setDiff(a, b map[string]struct{}) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

