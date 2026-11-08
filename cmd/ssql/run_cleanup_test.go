//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunLeavesNoTrace: `generate go -run` and `generate rust -run` remove
// the program they built, after a normal run and after an interrupt. Ctrl-C
// killed ssql before its cleanup until 2026-11-08, leaving a ~47 MB Go build
// in /tmp per interrupted run (nine were found), and every Rust run left its
// ~170 MB binary in the shared target directory (eleven were). An interrupt
// must also end ssql by the same signal, so a shell loop around it stops.
// The interrupt is sent to ssql alone, as `kill -INT` does: ssql has to pass
// it on to the program.
func TestRunLeavesNoTrace(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles programs")
	}
	bin := corpusBin(t)

	t.Run("go", func(t *testing.T) {
		tmp := t.TempDir()
		env := append(os.Environ(), "TMPDIR="+tmp, "SSQL_MODULE_DIR="+mustRepoRoot(t))
		builds := func() []string {
			m, _ := filepath.Glob(filepath.Join(tmp, "ssql-gen-*"))
			return m
		}
		run := exec.Command(bin, "generate", "go", "-run", "-pipeline", "ssql from range 3 | ssql to csv")
		run.Env = env
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		if left := builds(); len(left) > 0 {
			t.Errorf("a finished run left %v", left)
		}
		// the program exists once its binary does: interrupt then.
		interruptRun(t, bin, env, "go", func() bool {
			for _, d := range builds() {
				if _, err := os.Stat(filepath.Join(d, "ssqlgen")); err == nil {
					return true
				}
			}
			return false
		})
		if left := builds(); len(left) > 0 {
			t.Errorf("an interrupted run left %v", left)
		}
		concurrentRuns(t, bin, env, "go")
		if left := builds(); len(left) > 0 {
			t.Errorf("two runs at once left %v", left)
		}
	})

	t.Run("rust", func(t *testing.T) {
		if os.Getenv("SSQL_RUST_LANE") == "" {
			t.Skip("set SSQL_RUST_LANE=1 to compile the generated Rust (minutes on the first build)")
		}
		cache := filepath.Join(os.Getenv("HOME"), ".cache", "ssql", "rust")
		if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
			cache = filepath.Join(x, "ssql", "rust")
		}
		// this program's traces: its crate, and anything cargo keeps
		// under a name starting ssqlgen_ (the package is named per build).
		traces := func() []string {
			var all []string
			for _, pat := range []string{
				filepath.Join(cache, "crate-*"),
				filepath.Join(cache, "target", "release", "ssqlgen_*"),
				filepath.Join(cache, "target", "release", "*", "ssqlgen_*"),
			} {
				m, _ := filepath.Glob(pat)
				all = append(all, m...)
			}
			sort.Strings(all)
			return all
		}
		before := strings.Join(traces(), "\n")
		run := exec.Command(bin, "generate", "rust", "-run", "-pipeline", "ssql from range 3 | ssql to csv")
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		if after := strings.Join(traces(), "\n"); after != before {
			t.Errorf("a finished run left\n%s\n(before:\n%s)", after, before)
		}
		interruptRun(t, bin, os.Environ(), "rust", func() bool {
			m, _ := filepath.Glob(filepath.Join(cache, "target", "release", "ssqlgen_*"))
			for _, f := range m {
				if !strings.HasSuffix(f, ".d") {
					return true
				}
			}
			return false
		})
		if after := strings.Join(traces(), "\n"); after != before {
			t.Errorf("an interrupted run left\n%s\n(before:\n%s)", after, before)
		}
		concurrentRuns(t, bin, os.Environ(), "rust")
		if after := strings.Join(traces(), "\n"); after != before {
			t.Errorf("two runs at once left\n%s\n(before:\n%s)", after, before)
		}
	})
}

// concurrentRuns starts two different programs at the same moment and
// requires each to print its own pipeline's rows, as exec prints them.
// Until 2026-11-08 every Rust build wrote target/release/ssqlgen, so one
// run could execute the other's program, and with per-run cleanup one
// could delete the other's binary.
func concurrentRuns(t *testing.T, bin string, env []string, lang string) {
	t.Helper()
	counts := []string{"3", "7"}
	type result struct {
		out []byte
		err error
	}
	results := make([]chan result, len(counts))
	for i, n := range counts {
		results[i] = make(chan result, 1)
		go func(i int, n string) {
			cmd := exec.Command(bin, "generate", lang, "-run", "-pipeline", "ssql from range "+n+" | ssql to csv")
			cmd.Env = env
			out, err := cmd.Output()
			results[i] <- result{out, err}
		}(i, n)
	}
	for i, n := range counts {
		r := <-results[i]
		want, err := exec.Command("bash", "-c", bin+" from range "+n+" | "+bin+" to csv").Output()
		if err != nil {
			t.Fatalf("exec from range %s: %v", n, err)
		}
		if r.err != nil {
			if ee, ok := r.err.(*exec.ExitError); ok {
				t.Errorf("%s run of range %s failed: %v\n%s", lang, n, r.err, ee.Stderr)
				continue
			}
			t.Errorf("%s run of range %s: %v", lang, n, r.err)
			continue
		}
		if string(r.out) != string(want) {
			t.Errorf("%s run of range %s printed\n%s\nwant\n%s", lang, n, r.out, want)
		}
	}
}

// interruptRun starts a program that runs for minutes, waits until built
// says its binary exists, sends SIGINT to ssql alone, and requires ssql to
// die of SIGINT within the time it takes to clean up.
func interruptRun(t *testing.T, bin string, env []string, lang string, built func() bool) {
	t.Helper()
	cmd := exec.Command(bin, "generate", lang, "-run", "-pipeline", "ssql from range 100000000000 | ssql to csv")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Minute)
	for !built() {
		select {
		case err := <-done:
			t.Fatalf("ssql ended before its program was built: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatal("the program was never built")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // past the exec, into the run
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("ssql exited %v after SIGINT, want death by SIGINT", err)
		}
		ws, _ := ee.Sys().(syscall.WaitStatus)
		if !ws.Signaled() || ws.Signal() != syscall.SIGINT {
			t.Errorf("ssql ended %v after SIGINT, want death by SIGINT", ee)
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("ssql did not end within 30 s of SIGINT")
	}
}
