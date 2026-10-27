package main

// The rust lane of TestPipelineEquivalence (DFC157 §5): every case's
// pipeline rendered by `generate rust -func case_N`, all the functions in
// ONE crate with a main that dispatches on argv[1], built ONCE with cargo
// (DataFusion statically linked: one 170 MB binary, not 328), then each
// case runs its function and hands the JSONL to the standard
// normalisation. Opt-in on SSQL_RUST_LANE=1 and cargo being found, like
// the Postgres and DataFusion lanes are opt-in on their engines: the
// first build compiles DataFusion (minutes); the dependency build is
// shared with `generate rust -run` under ~/.cache/ssql/rust/target
// (SSQL_RUST_TARGET overrides). The lane inherits the datafusion lane's
// skips — same engine — and any value it disagrees with that lane on is
// a lowering bug.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// rustLaneCargo finds cargo the way generate rust does.
func rustLaneCargo() string {
	if c := os.Getenv("SSQL_CARGO"); c != "" {
		return c
	}
	if c, err := exec.LookPath("cargo"); err == nil {
		return c
	}
	if home, err := os.UserHomeDir(); err == nil {
		c := filepath.Join(home, ".cargo", "bin", "cargo")
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// rustLaneEnabled reports whether the lane runs.
func rustLaneEnabled() bool {
	return os.Getenv("SSQL_RUST_LANE") != "" && rustLaneCargo() != ""
}

// rustLaneState is the built binary and each case's generation outcome.
type rustLaneState struct {
	once   sync.Once
	bin    string
	err    error             // the build's failure, reported by every case
	skips  map[string]string // case → by-design refusal
	fails  map[string]string // case → generation failure (a lane bug)
	hasFn  map[string]bool
	target string
}

var rustLane rustLaneState

var rustCaseNameRe = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// rustCaseFunc is the function name a case renders as.
func rustCaseFunc(name string) string {
	return "case_" + rustCaseNameRe.ReplaceAllString(name, "_")
}

// rustLanePrepare renders every equivalence case and builds the one
// binary. Called once, before the cases run, with the fixture
// replacer TestPipelineEquivalence uses.
func rustLanePrepare(t *testing.T, bin string, repl *strings.Replacer) {
	t.Helper()
	rustLane.once.Do(func() {
		rustLane.skips = map[string]string{}
		rustLane.fails = map[string]string{}
		rustLane.hasFn = map[string]bool{}
		var funcs []string
		var names []string
		for _, c := range equivCases {
			var env []string
			for _, e := range c.Env {
				env = append(env, repl.Replace(e))
			}
			fn := rustCaseFunc(c.Name)
			cmd := exec.Command("bash", "-c", "export SSQL_MODE=record && "+repl.Replace(c.Pipeline)+" | "+bin+" to jsonl | "+bin+" generate rust -func "+fn)
			cmd.Env = append(os.Environ(), env...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				if strings.Contains(stderr.String(), "has no datafusion translation") {
					rustLane.skips[c.Name] = strings.TrimSpace(stderr.String())
				} else {
					rustLane.fails[c.Name] = fmt.Sprintf("%v\n%s", err, stderr.String())
				}
				continue
			}
			funcs = append(funcs, stdout.String())
			names = append(names, fn)
			rustLane.hasFn[c.Name] = true
		}
		rustLane.target = os.Getenv("SSQL_RUST_TARGET")
		if rustLane.target == "" {
			home, _ := os.UserHomeDir()
			rustLane.target = filepath.Join(home, ".cache", "ssql", "rust", "target")
		}
		crate := filepath.Join(os.TempDir(), fmt.Sprintf("ssql-rust-lane-%d", os.Getpid()))
		if err := os.MkdirAll(filepath.Join(crate, "src"), 0o755); err != nil {
			rustLane.err = err
			return
		}
		t.Cleanup(func() { os.RemoveAll(crate) })
		if err := os.WriteFile(filepath.Join(crate, "Cargo.toml"), []byte(rustLaneCargoToml), 0o644); err != nil {
			rustLane.err = err
			return
		}
		if err := os.WriteFile(filepath.Join(crate, "src", "main.rs"), []byte(rustLaneMain(funcs, names)), 0o644); err != nil {
			rustLane.err = err
			return
		}
		build := exec.Command(rustLaneCargo(), "build", "--release", "--quiet")
		build.Dir = crate
		build.Env = append(os.Environ(), "CARGO_TARGET_DIR="+rustLane.target)
		var out bytes.Buffer
		build.Stdout, build.Stderr = &out, &out
		t.Logf("rust lane: %d cases rendered (%d refused by design, %d failed to generate); building one crate under %s", len(funcs), len(rustLane.skips), len(rustLane.fails), rustLane.target)
		for name, msg := range rustLane.fails {
			first, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
			t.Logf("rust lane: %s did not generate: %s", name, first)
		}
		if err := build.Run(); err != nil {
			rustLane.err = fmt.Errorf("cargo build failed: %v\n%s", err, out.String())
			return
		}
		rustLane.bin = filepath.Join(rustLane.target, "release", "ssqllane")
	})
}

const rustLaneCargoToml = `[package]
name = "ssqllane"
version = "0.1.0"
edition = "2021"

[dependencies]
datafusion = "55"
tokio = { version = "1", features = ["rt-multi-thread", "macros"] }
futures = "0.3"

[profile.release]
opt-level = 3
`

// rustLaneMain is the crate's main.rs: every case's function and a main
// that runs the one named by argv[1] and prints its rows as JSONL.
func rustLaneMain(funcs, names []string) string {
	var sb strings.Builder
	sb.WriteString(`#![allow(unused_imports, dead_code)]
use datafusion::arrow::datatypes::DataType;
use datafusion::error::Result;
use datafusion::functions_aggregate::expr_fn as agg;
use datafusion::functions_window::expr_fn as win;
use datafusion::prelude::*;

`)
	for _, f := range funcs {
		sb.WriteString(f)
		sb.WriteString("\n")
	}
	sb.WriteString(`#[tokio::main]
async fn main() -> Result<()> {
    let name = std::env::args().nth(1).expect("case name");
    let config = SessionConfig::new().set_str("datafusion.catalog.has_header", "true");
    let ctx = SessionContext::new_with_config(config).enable_url_table();
    let df = match name.as_str() {
`)
	for _, n := range names {
		fmt.Fprintf(&sb, "        %q => %s(&ctx).await?,\n", n, n)
	}
	sb.WriteString(`        other => panic!("no case {}", other),
    };
    let mut w = datafusion::arrow::json::LineDelimitedWriter::new(std::io::BufWriter::new(std::io::stdout().lock()));
    let mut stream = df.execute_stream().await?;
    while let Some(batch) = futures::StreamExt::next(&mut stream).await {
        w.write(&batch?)?;
    }
    w.finish()?;
    Ok(())
}
`)
	return sb.String()
}

// rustLaneRun is the lane's run function: the case is named by the
// subtest (TestPipelineEquivalence/<case>).
func rustLaneRun(t *testing.T, bin, pipeline string, env []string) string {
	t.Helper()
	name := path.Base(t.Name())
	if rustLane.err != nil {
		t.Fatalf("lane rust: %v", rustLane.err)
	}
	if reason, ok := rustLane.skips[name]; ok {
		return equivSkipPrefix + reason
	}
	if msg, ok := rustLane.fails[name]; ok {
		t.Fatalf("lane rust: generate rust failed:\n  pipeline: %s\n  %s", pipeline, msg)
	}
	if !rustLane.hasFn[name] {
		return equivSkipPrefix + "rust lane: case not prebuilt (not a TestPipelineEquivalence case)"
	}
	cmd := exec.Command(rustLane.bin, rustCaseFunc(name))
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("lane rust: the program failed: %v\n  pipeline: %s\n  stderr:\n%s", err, pipeline, stderr.String())
	}
	return stdout.String()
}
