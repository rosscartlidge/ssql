package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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
		Flag("-run", "-r").
		Bool().
		Global().
		Default(false).
		Help("Compile and run the generated Go code (mutually exclusive with OUTPUT)").
		Done().
		Flag("OUTPUT").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.go"}).
		Global().
		Default("").
		Help("Output Go file (or stdout if not specified)").
		Done().
		Handler(func(ctx *cf.Context) error {
			var outputFile string
			var run bool

			if outVal, ok := ctx.GlobalFlags["OUTPUT"]; ok {
				outputFile = outVal.(string)
			}
			if runVal, ok := ctx.GlobalFlags["-run"]; ok {
				run = runVal.(bool)
			}
			if run && outputFile != "" {
				return fmt.Errorf("ssql generate go: -run and OUTPUT are mutually exclusive (use -run to compile and execute, or omit -run to write the source to a file)")
			}

			// Assemble code fragments from stdin
			code, err := lib.AssembleCodeFragments(os.Stdin)
			if err != nil {
				return fmt.Errorf("assembling code fragments: %w", err)
			}

			if run {
				return runGoSource(code)
			}

			// Write to output
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

// runGoSource compiles the generated Go code in a temporary module
// directory, then executes the resulting binary from the user's
// current working directory. The temp module declares a dependency
// on the same ssql version that built this binary (so the
// generated program's `import "github.com/rosscartlidge/ssql/v4/typed"`
// resolves consistently).
//
// Two-step (build then run) rather than `go run` because `go run`
// executes the binary in the same directory it compiles from — that
// would break relative file paths in the user's pipeline (e.g.
// `ssql from data.csv`). Building first, then exec'ing the binary
// from the user's cwd, keeps relative paths working AND lets Go
// resolve the temp module without conflict with whatever go.mod is
// (or isn't) in the user's cwd.
//
// Stdout / stderr from the compiled program are forwarded; the temp
// directory is cleaned up on return.
//
// Pre-conditions: a Go toolchain on $PATH, and the ssql module
// either in Go's module cache (typical after a `go install
// github.com/rosscartlidge/ssql/v4/cmd/ssql@vX.Y.Z`) or available
// to fetch from the proxy.
func runGoSource(code string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("ssql generate go -run: 'go' binary not found in PATH (a Go toolchain is required)")
	}

	dir, err := os.MkdirTemp("", "ssql-gen-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	goMod := fmt.Sprintf(`module ssqlgen

go 1.23

require github.com/rosscartlidge/ssql/v4 v%s
`, version.Version)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644); err != nil {
		return fmt.Errorf("writing main.go: %w", err)
	}

	binPath := filepath.Join(dir, "ssqlgen")

	// Build step: cwd = temp dir so Go uses the temp module's go.mod.
	// -mod=mod lets `go build` populate go.sum on demand from Go's
	// module cache (or fetch via GOPROXY) — avoids requiring the user
	// to run a separate `go mod tidy` step. The cost is one extra
	// cache lookup per shard; negligible compared to the actual
	// compile time.
	build := exec.Command("go", "build", "-mod=mod", "-o", binPath, ".")
	build.Dir = dir
	build.Stdout = os.Stderr // build messages go to stderr; only program output to stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("ssql generate go -run: compile failed: %w", err)
	}

	// Run step: do NOT set cmd.Dir — the binary inherits the user's
	// cwd, so relative file paths in the pipeline resolve as expected.
	run := exec.Command(binPath)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	return run.Run()
}
