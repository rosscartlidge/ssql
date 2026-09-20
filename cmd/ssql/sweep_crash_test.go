package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestCrashSweep is DFC133 instrument 2: every pipeline command, every flag,
// fed degenerate values and degenerate input — driven by `ssql -spec-json`,
// the CLI's own description of itself, so there is no second copy of any
// command's grammar to drift (DFC115). It asserts only what must ALWAYS
// hold: no Go runtime error reaches the user (main recovers panics into an
// `Error:` line, so a recovered panic still shows its text), nothing
// hangs, exit 0 means well-formed output, and a field name that does not
// exist is an error rather than an empty or unchanged result. `to table
// -max-width 0` (a slice-bounds panic) and a window clause that looped
// forever were both this class. Opt-in: SSQL_SWEEP=1.
func TestCrashSweep(t *testing.T) {
	if os.Getenv("SSQL_SWEEP") == "" {
		t.Skip("opt-in: SSQL_SWEEP=1 (DFC133)")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	specOut, err := exec.Command(bin, "-spec-json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var spec sweepSpec
	if err := json.Unmarshal(specOut, &spec); err != nil {
		t.Fatal(err)
	}

	// Inputs, as the JSONL a `from` stage would hand the command. Column
	// names include ones that collide with expression builtins and SQL.
	const header = "name,age,salary,dept,date,count,type"
	inputs := map[string]string{
		"rows":        header + "\nAlice,35,95000,Eng,2020-01-05,3,a\nBob,28,65000,Sales,2021-06-01,1,b\nCarol,41,105000,Eng,2019-07-09,2,a\n",
		"one-row":     header + "\nAlice,35,95000,Eng,2020-01-05,3,a\n",
		"header-only": header + "\n",
		"all-null":    header + "\n,,,,,,\n,,,,,,\n",
		"null-first":  header + "\n,,,,,,\nBob,28,65000,Sales,2021-06-01,1,b\n",
	}
	feeds := map[string]string{"empty-stdin": ""}
	for name, csv := range inputs {
		p := filepath.Join(dir, name+".csv")
		os.WriteFile(p, []byte(csv), 0o644)
		out, err := exec.Command(bin, "from", "csv", p).Output()
		if err != nil {
			t.Fatalf("from %s: %v", name, err)
		}
		feeds[name] = string(out)
	}

	// Sources, sinks that write files or open servers, and meta commands
	// are not pipeline stages; join/union/merge need a second input.
	skip := map[string]bool{"from": true, "to": true, "generate": true, "serve": true, "codelab": true, "functions": true,
		"conventions": true, "version": true, "merge": true, "tee": true, "join": true, "union": true}

	type finding struct{ kind, cmd, detail string }
	var findings []finding
	runs := 0
	run := func(feed string, args []string, unknownField bool) {
		runs++
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdin = strings.NewReader(feeds[feed])
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		line := "[" + feed + "] ssql " + strings.Join(args, " ")
		first := strings.SplitN(strings.TrimSpace(stderr.String()), "\n", 2)[0]
		switch {
		case ctx.Err() != nil:
			findings = append(findings, finding{"HANG", line, "no exit within 15s"})
		case sweepRuntimeError(stderr.String()) || sweepRuntimeError(stdout.String()):
			findings = append(findings, finding{"PANIC", line, first})
		case err != nil && strings.TrimSpace(stderr.String()) == "":
			findings = append(findings, finding{"SILENT-FAIL", line, "non-zero exit with nothing on stderr"})
		case err == nil && unknownField && feed != "empty-stdin" && feed != "header-only" && !sweepCreatesFields[args[0]+" "+args[1]]:
			findings = append(findings, finding{"UNKNOWN-FIELD-ACCEPTED", line, "exit 0 for a field that does not exist"})
		case err == nil && !sweepWellFormed(stdout.String()):
			findings = append(findings, finding{"MALFORMED", line, strings.SplitN(stdout.String(), "\n", 2)[0]})
		}
	}

	var names []string
	for n := range spec.Subcommands {
		if !skip[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		c := spec.Subcommands[name]
		// The command bare, on every input.
		for feed := range feeds {
			run(feed, []string{name}, false)
		}
		for _, f := range c.Flags {
			flag := f.Names[0]
			if flag == "-generate" || flag == "-file" || flag == "-output" {
				continue
			}
			for _, variant := range sweepArgVariants(f) {
				args := []string{name}
				if strings.HasPrefix(flag, "-") || strings.HasPrefix(flag, "+") {
					args = append(args, flag)
				}
				args = append(args, variant.values...)
				for _, feed := range []string{"rows", "one-row", "all-null", "null-first", "header-only", "empty-stdin"} {
					run(feed, args, variant.unknownField)
				}
			}
		}
	}

	// Report: one line per distinct (kind, command-without-feed) so a flag
	// that misbehaves on every input is one finding, not six.
	seen := map[string]bool{}
	byKind := map[string]int{}
	for _, f := range findings {
		key := f.kind + "|" + f.cmd[strings.Index(f.cmd, "]")+2:]
		if seen[key] {
			continue
		}
		seen[key] = true
		byKind[f.kind]++
		t.Errorf("%s: %s\n    %s", f.kind, f.cmd, f.detail)
	}
	t.Logf("crash sweep: %d commands, %d runs, %d distinct findings %v", len(names), runs, len(seen), byKind)
}

// sweepCreatesFields: flags whose field argument NAMES A NEW FIELD by
// design, so a name that does not exist yet is the point, not a typo.
var sweepCreatesFields = map[string]bool{"update -set": true, "update -set-expr": true, "update -set-bucket": true}

type sweepSpec struct {
	Subcommands map[string]sweepCmd `json:"subcommands"`
}

type sweepCmd struct {
	Flags []sweepFlag `json:"flags"`
}

type sweepFlag struct {
	Names []string `json:"names"`
	Bool  bool     `json:"bool"`
	Args  []struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		Completer string `json:"completer"`
	} `json:"args"`
}

type sweepVariant struct {
	values       []string
	unknownField bool
}

// sweepArgVariants builds the flag's argument lists: a plausible one (a
// real field, a small number), then one degenerate value at a time per
// argument, the others held plausible.
func sweepArgVariants(f sweepFlag) []sweepVariant {
	if f.Bool || len(f.Args) == 0 {
		return []sweepVariant{{}}
	}
	plausible := make([]string, len(f.Args))
	for i, a := range f.Args {
		switch {
		case a.Completer == "fields":
			plausible[i] = "age"
		case a.Type == "integer":
			plausible[i] = "2"
		case a.Type == "float":
			plausible[i] = "0.5"
		default:
			plausible[i] = "x"
		}
	}
	out := []sweepVariant{{values: plausible}}
	for i, a := range f.Args {
		var bad []string
		switch {
		case a.Completer == "fields":
			bad = []string{"nosuchfield", ""}
		case a.Type == "integer":
			bad = []string{"0", "-1", "999999999999999999", "abc", ""}
		case a.Type == "float":
			bad = []string{"0", "-1", "1e400", "abc"}
		default:
			// (no NUL: the OS refuses it in an argument, so the command
			// never starts — that is exec's failure, not the CLI's)
			bad = []string{"", "'", "a\nb", "-", strings.Repeat("z", 5000)}
		}
		for _, b := range bad {
			v := append([]string(nil), plausible...)
			v[i] = b
			out = append(out, sweepVariant{values: v, unknownField: a.Completer == "fields" && b == "nosuchfield"})
		}
	}
	return out
}

// sweepRuntimeError recognises a Go runtime failure in what the user sees,
// recovered into an Error line or not.
func sweepRuntimeError(s string) bool {
	for _, tok := range []string{"runtime error", "panic:", "goroutine ", "nil pointer", "index out of range", "slice bounds out of range", "invalid memory address", "reflect:", "interface conversion"} {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}

// sweepWellFormed: a stage's stdout is JSON Lines (or empty, or the plain
// number `count` prints).
func sweepWellFormed(out string) bool {
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var v any
		if json.Unmarshal([]byte(ln), &v) != nil {
			return false
		}
	}
	return true
}

var _ = fmt.Sprintf
