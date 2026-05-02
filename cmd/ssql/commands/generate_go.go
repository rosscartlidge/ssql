package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// registerGenerateGo registers the "generate go" subcommand
func registerGenerateGo(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("go").
		Description("Generate Go code from ssql CLI pipeline").
		Example("ssql from -g data.csv | ssql where -g -if age gt 18 | ssql generate go", "Generate Go code from pipeline").
		Example("(export SSQLGO=1 && ssql from data.csv | ssql limit 10 | ssql generate go) > prog.go", "Generate using environment variable").
		Example("(export SSQLGO=parallel; ssql from data.csv | ssql to table) | ssql generate go -run", "Generate, compile, and execute in one shot").
		Example("(export SSQLGO=parallel; ssql from data.csv | ssql to table) | ssql generate go -build query", "Compile to a binary named 'query' and exit").
		Example("(export SSQLGO=parallel; ssql from parquet x.parquet | ssql group-by k -count n | ssql to table) | ssql generate go -optimise -run", "Apply pipeline optimiser (column projection etc.), then compile and execute").
		Flag("-run", "-r").
		Bool().
		Global().
		Default(false).
		Help("Compile and run the generated Go code (mutually exclusive with OUTPUT and -build)").
		Done().
		Flag("-build", "-b").
		String().
		Completer(&cf.FileCompleter{Pattern: "*"}).
		Global().
		Default("").
		Help("Compile to the named binary and exit (mutually exclusive with OUTPUT and -run)").
		Done().
		Flag("-optimise", "-O").
		Bool().
		Global().
		Default(false).
		Help("Apply the pipeline optimiser (same rewrites as 'generate ssql') before generating code — auto-injects column projection, predicate pushdown, etc.").
		Done().
		Flag("-explain", "-e").
		Bool().
		Global().
		Default(false).
		Help("With -optimise: print applied optimisation rules to stderr").
		Done().
		Flag("OUTPUT").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.go"}).
		Global().
		Default("").
		Help("Output Go source file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var run bool
			var buildOut string
			var optimise bool
			var explain bool

			if outVal, ok := ctx.GlobalFlags["OUTPUT"]; ok {
				outputFile = outVal.(string)
			}
			if runVal, ok := ctx.GlobalFlags["-run"]; ok {
				run = runVal.(bool)
			}
			if buildVal, ok := ctx.GlobalFlags["-build"]; ok {
				buildOut = buildVal.(string)
			}
			if v, ok := ctx.GlobalFlags["-optimise"]; ok {
				optimise = v.(bool)
			}
			if v, ok := ctx.GlobalFlags["-explain"]; ok {
				explain = v.(bool)
			}

			// At most one of {-run, -build, OUTPUT} may be set — they
			// represent three mutually exclusive output forms.
			modes := 0
			if run {
				modes++
			}
			if buildOut != "" {
				modes++
			}
			if outputFile != "" {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("ssql generate go: -run, -build, and OUTPUT are mutually exclusive (pick one: -run to compile+execute, -build PATH to compile to a binary, OUTPUT to write Go source)")
			}

			if explain {
				// The typed-mode planner reads this and emits its
				// per-stage decisions (source parallel/serial,
				// inserted Serial() boundaries) to stderr. The
				// optimiser's own -explain output is handled below
				// when -optimise is set.
				os.Setenv("SSQL_EXPLAIN_PLAN", "1")
			}
			if optimise {
				return runOptimiseThenGo(os.Stdin, run, buildOut, outputFile, explain)
			}

			// Assemble code fragments from stdin
			code, err := lib.AssembleCodeFragments(os.Stdin)
			if err != nil {
				return fmt.Errorf("assembling code fragments: %w", err)
			}

			if run {
				return runGoSource(code)
			}
			if buildOut != "" {
				return buildGoSource(code, buildOut)
			}

			// Write source to output
			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(code), 0644); err != nil {
					return fmt.Errorf("writing output file: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Generated Go code written to %s\n", outputFile)
			} else {
				fmt.Print(code)
			}

			return nil
		}).
		Done()
}

// runOptimiseThenGo implements `generate go -optimise`. The fragment
// stream from stdin is fed to optimizePipeline (the same rewriter
// `generate ssql` uses), which returns a rewritten pipeline string.
// We re-execute that string under bash with SSQLGO inferred from the
// incoming fragment metadata (parallel/typed/Record), then pipe the
// new fragments through `ssql generate go` (without -optimise to
// avoid recursion). Output mode flags (-run, -build, OUTPUT) are
// forwarded to the inner generate-go.
//
// Inferring SSQLGO from incoming fragment metadata (rather than the
// parent's environment) handles the user's own subshell-export
// patterns naturally — see doc/research/generate-go-flags-proposal.md
// §2b for the discussion.
func runOptimiseThenGo(in io.Reader, run bool, buildOut, outputFile string, explain bool) error {
	// Buffer stdin once — we feed it to optimizePipeline AND parse it
	// again to detect the target SSQLGO mode for re-execution.
	buf, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("reading fragments: %w", err)
	}

	pipeline, rules, err := optimizePipeline(bytes.NewReader(buf))
	if err == errEmptyResult {
		if explain {
			for _, r := range rules {
				fmt.Fprintf(os.Stderr, "[%s] %s → %s\n", r.Rule, r.Before, r.After)
			}
		}
		fmt.Fprintln(os.Stderr, "pipeline produces no results (contradictory predicates)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("optimising pipeline: %w", err)
	}

	if explain {
		if len(rules) == 0 {
			fmt.Fprintln(os.Stderr, "(no optimisation rules applied)")
		}
		for _, r := range rules {
			fmt.Fprintf(os.Stderr, "[%s] %s → %s\n", r.Rule, r.Before, r.After)
		}
	}

	targetMode := detectFragmentMode(buf)

	// Build the inner command. The inner generate-go inherits whichever
	// output flag was passed to the outer one.
	inner := pipeline + " | ssql generate go"
	switch {
	case run:
		inner += " -run"
	case buildOut != "":
		inner += " -build " + shellQuote(buildOut)
	case outputFile != "":
		inner += " " + shellQuote(outputFile)
	}

	cmd := exec.Command("bash", "-c", inner)
	cmd.Env = setEnvVar(os.Environ(), "SSQLGO", targetMode)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// detectFragmentMode peeks the buffered fragment stream to figure
// out which SSQLGO scope to set when re-executing the optimised
// pipeline:
//
//   - any fragment with IsStream=true   → parallel-mode (the
//     pipeline contains a Stream[T] producer like
//     ReadCSVParallel / ReadParquetParallel)
//   - else any fragment with OutputTypedSchema set → typed-mode
//     (the pipeline emitted struct-typed fragments)
//   - otherwise → "1" (Record-mode)
//
// Errors during decoding fall through to the most permissive mode
// ("1") — the inner generate-go will surface a clearer error.
func detectFragmentMode(buf []byte) string {
	hasTyped := false
	dec := json.NewDecoder(bytes.NewReader(buf))
	for {
		var frag lib.CodeFragment
		if err := dec.Decode(&frag); err != nil {
			break
		}
		if frag.IsStream {
			return "parallel"
		}
		if frag.OutputTypedSchema != nil {
			hasTyped = true
		}
	}
	if hasTyped {
		return "typed"
	}
	return "1"
}

// setEnvVar returns env with KEY=value, replacing an existing entry
// if present.
func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// shellQuote wraps s in single quotes if it contains characters bash
// would otherwise interpret. Used for output paths passed to the
// inner `ssql generate go` invocation.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runGoSource compiles the generated Go code, then executes the
// resulting binary from the user's current working directory. The
// temp module is removed on return.
//
// Two-step (build then run) rather than `go run` because `go run`
// executes the binary in the same directory it compiles from — that
// would break relative file paths in the user's pipeline (e.g.
// `ssql from data.csv`). Building first, then exec'ing the binary
// from the user's cwd, keeps relative paths working AND lets Go
// resolve the temp module without conflict with whatever go.mod is
// (or isn't) in the user's cwd.
func runGoSource(code string) error {
	dir, binPath, err := compileGoSource(code, "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// Run step: do NOT set cmd.Dir — the binary inherits the user's
	// cwd, so relative file paths in the pipeline resolve as expected.
	run := exec.Command(binPath)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	return run.Run()
}

// buildGoSource compiles the generated Go code and writes the
// resulting binary to outPath, then exits without running it. The
// temp source directory is cleaned up on return; only the binary
// remains.
//
// Useful when the user wants to ship the compiled pipeline (e.g. on
// a CI runner that doesn't have ssql installed) or run it many
// times without paying the compile cost each invocation.
func buildGoSource(code, outPath string) error {
	abs, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("ssql generate go -build: resolving output path: %w", err)
	}
	dir, _, err := compileGoSource(code, abs)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	fmt.Fprintf(os.Stderr, "Compiled binary written to %s\n", outPath)
	return nil
}

// compileGoSource is the shared implementation behind [runGoSource]
// and [buildGoSource]. It writes a temp module containing the user's
// generated code, runs `go build`, and returns (tempDir, binaryPath).
//
// outPath: where the compiled binary should land. Pass "" to put the
// binary inside the temp dir (caller will exec it then nuke
// everything); pass an absolute path to keep the binary after the
// temp dir is cleaned up.
//
// The temp module declares a dependency on the same ssql version
// that built this binary, so the generated program's
// `import "github.com/rosscartlidge/ssql/v4/typed"` resolves
// consistently.
//
// Pre-conditions: a Go toolchain on $PATH, and the ssql module
// either in Go's module cache (typical after `go install
// github.com/rosscartlidge/ssql/v4/cmd/ssql@vX.Y.Z`) or available
// to fetch from the proxy.
func compileGoSource(code, outPath string) (tempDir, binPath string, err error) {
	if _, err := exec.LookPath("go"); err != nil {
		return "", "", fmt.Errorf("ssql generate go: 'go' binary not found in PATH (a Go toolchain is required)")
	}

	dir, err := os.MkdirTemp("", "ssql-gen-*")
	if err != nil {
		return "", "", fmt.Errorf("creating temp dir: %w", err)
	}

	goMod := fmt.Sprintf(`module ssqlgen

go 1.23

require github.com/rosscartlidge/ssql/v4 v%s
`, version.Version)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("writing go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("writing main.go: %w", err)
	}

	if outPath == "" {
		outPath = filepath.Join(dir, "ssqlgen")
	}

	// Build step: cwd = temp dir so Go uses the temp module's go.mod.
	// -mod=mod lets `go build` populate go.sum on demand from Go's
	// module cache (or fetch via GOPROXY) — avoids requiring the user
	// to run a separate `go mod tidy` step.
	build := exec.Command("go", "build", "-mod=mod", "-o", outPath, ".")
	build.Dir = dir
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("ssql generate go: compile failed: %w", err)
	}

	return dir, outPath, nil
}
