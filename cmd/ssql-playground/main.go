//go:build js && wasm

package main

import (
	"fmt"
	"os"
	"syscall/js"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/commands"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

func main() {
	js.Global().Set("ssqlExec", js.FuncOf(jsExec))
	js.Global().Set("ssqlWriteFile", js.FuncOf(jsWriteFile))
	js.Global().Set("ssqlReady", true)
	fmt.Fprintln(os.Stderr, "ssql playground ready ("+version.Version+")")
	select {} // block forever
}

// jsWriteFile: ssqlWriteFile(filename, contents) — write a virtual file
func jsWriteFile(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return js.ValueOf(map[string]any{"error": "ssqlWriteFile requires 2 arguments: filename, contents"})
	}
	filename := args[0].String()
	contents := args[1].String()
	if err := os.WriteFile(filename, []byte(contents), 0644); err != nil {
		return js.ValueOf(map[string]any{"error": err.Error()})
	}
	return js.ValueOf(map[string]any{"ok": true})
}

// jsExec: ssqlExec(argsArray, stdinString, envObject?) → {stdout, stderr, exitCode}
//
// envObject (optional) sets environment variables for THIS invocation only
// (restored afterwards). Needed because the wasm process env is frozen at
// go.run(); mode env vars like SSQL_MODE=schema (Ctrl-O field completion)
// must be settable per call. Safe: execCommand is synchronous and the
// playground is single-threaded.
func jsExec(this js.Value, jsArgs []js.Value) any {
	if len(jsArgs) < 1 {
		return js.ValueOf(map[string]any{
			"stdout":   "",
			"stderr":   "ssqlExec requires at least 1 argument: args array",
			"exitCode": 1,
		})
	}

	// Parse args from JS array
	jsArr := jsArgs[0]
	args := make([]string, jsArr.Length())
	for i := 0; i < jsArr.Length(); i++ {
		args[i] = jsArr.Index(i).String()
	}

	// Get stdin data
	stdinData := ""
	if len(jsArgs) > 1 && !jsArgs[1].IsUndefined() && !jsArgs[1].IsNull() {
		stdinData = jsArgs[1].String()
	}

	// Apply per-invocation env, restoring the previous state after.
	if len(jsArgs) > 2 && !jsArgs[2].IsUndefined() && !jsArgs[2].IsNull() {
		obj := jsArgs[2]
		keys := js.Global().Get("Object").Call("keys", obj)
		type saved struct {
			key, val string
			had      bool
		}
		var restore []saved
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			old, had := os.LookupEnv(k)
			restore = append(restore, saved{k, old, had})
			os.Setenv(k, obj.Get(k).String())
		}
		defer func() {
			for _, s := range restore {
				if s.had {
					os.Setenv(s.key, s.val)
				} else {
					os.Unsetenv(s.key)
				}
			}
		}()
	}

	stdout, stderr, exitCode := execCommand(args, stdinData)

	return js.ValueOf(map[string]any{
		"stdout":   stdout,
		"stderr":   stderr,
		"exitCode": exitCode,
	})
}

// execCounter provides unique temp file names across invocations.
var execCounter int

// execCommand runs an ssql command with captured I/O using temp files
// (os.Pipe is not implemented in Go WASM).
func execCommand(args []string, stdinData string) (string, string, int) {
	// Cursor-context protocol (-cursor-stage / -help-at / -complete-source):
	// pure argv→string, no I/O redirection needed. Drives the playground's
	// help-at-cursor button with the same code as the CLI's Alt-h binding.
	if out, errOut, code, ok := commands.HandleCursorProtocol(args, buildCommand); ok {
		return out, errOut, code
	}
	execCounter++
	prefix := fmt.Sprintf("/tmp/_exec_%d", execCounter)
	stdinFile := prefix + "_in"
	stdoutFile := prefix + "_out"
	stderrFile := prefix + "_err"

	// Set os.Args so getCommandString() produces correct Command fields in fragments
	os.Args = append([]string{"ssql"}, args...)

	// Write stdin to temp file
	if err := os.WriteFile(stdinFile, []byte(stdinData), 0644); err != nil {
		return "", fmt.Sprintf("writing stdin: %v", err), 1
	}

	// Open stdin file for reading
	inF, err := os.Open(stdinFile)
	if err != nil {
		return "", fmt.Sprintf("opening stdin: %v", err), 1
	}

	// Create stdout and stderr files
	outF, err := os.Create(stdoutFile)
	if err != nil {
		inF.Close()
		return "", fmt.Sprintf("creating stdout: %v", err), 1
	}
	errF, err := os.Create(stderrFile)
	if err != nil {
		inF.Close()
		outF.Close()
		return "", fmt.Sprintf("creating stderr: %v", err), 1
	}

	// Save and redirect
	origStdin := os.Stdin
	origStdout := os.Stdout
	origStderr := os.Stderr
	os.Stdin = inF
	os.Stdout = outF
	os.Stderr = errF

	// Build and execute command
	cmd := buildCommand()
	execErr := cmd.Execute(args)

	// Close redirected files
	inF.Close()
	outF.Close()
	errF.Close()

	// Restore
	os.Stdin = origStdin
	os.Stdout = origStdout
	os.Stderr = origStderr

	// Read captured output
	outBytes, _ := os.ReadFile(stdoutFile)
	errBytes, _ := os.ReadFile(stderrFile)

	// Clean up temp files
	os.Remove(stdinFile)
	os.Remove(stdoutFile)
	os.Remove(stderrFile)

	exitCode := 0
	if execErr != nil {
		exitCode = 1
		if len(errBytes) == 0 {
			errBytes = []byte(execErr.Error())
		}
	}

	return string(outBytes), string(errBytes), exitCode
}

func buildCommand() *cf.Command {
	cmd := cf.NewCommand("ssql").
		Version(version.Version).
		Description("Unix-style data processing tools").
		Flag("-verbose", "-v").
		Bool().
		Global().
		Help("Enable verbose output").
		Done()

	cmd = commands.RegisterVersion(cmd)
	cmd = commands.RegisterFunctions(cmd)
	cmd = commands.RegisterConventions(cmd)
	cmd = commands.RegisterFrom(cmd)
	cmd = commands.RegisterLimit(cmd)
	cmd = commands.RegisterSample(cmd)
	cmd = commands.RegisterOffset(cmd)
	cmd = commands.RegisterTee(cmd)
	cmd = commands.RegisterSort(cmd)
	cmd = commands.RegisterResample(cmd)
	cmd = commands.RegisterTop(cmd)
	cmd = commands.RegisterDistinct(cmd)
	cmd = commands.RegisterCount(cmd)
	cmd = commands.RegisterDescribe(cmd)
	cmd = commands.RegisterUnpivot(cmd)
	cmd = commands.RegisterWhere(cmd)
	cmd = commands.RegisterUpdate(cmd)
	cmd = commands.RegisterCast(cmd)
	cmd = commands.RegisterInclude(cmd)
	cmd = commands.RegisterExclude(cmd)
	cmd = commands.RegisterRename(cmd)
	cmd = commands.RegisterGroupBy(cmd)
	cmd = commands.RegisterPivot(cmd)
	cmd = commands.RegisterWindow(cmd)
	cmd = commands.RegisterJoin(cmd)
	cmd = commands.RegisterUnion(cmd)
	cmd = commands.RegisterMerge(cmd)
	cmd = commands.RegisterFFT(cmd)
	cmd = commands.RegisterIFFT(cmd)
	cmd = commands.RegisterConvolve(cmd)
	cmd = commands.RegisterCorrelate(cmd)
	cmd = commands.RegisterSpectrogram(cmd)
	cmd = commands.RegisterTo(cmd)
	cmd = commands.RegisterGenerate(cmd)
	cmd = commands.RegisterServe(cmd)

	return cmd.Handler(func(ctx *cf.Context) error {
		return fmt.Errorf("no command specified")
	}).Build()
}
