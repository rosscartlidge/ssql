package main

import (
	"fmt"
	"os"
	"path/filepath"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/commands"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// Flags that support +flag negation with arguments.
// Boolean flags all support + negation automatically.
var negatableArgFlags = map[string]bool{
	"-if":      true,
	"-if-expr": true,
}

// ssqlPrefixHandler interprets +flag as negation of -flag.
// Boolean flags: +desc → false (instead of true).
// Arg flags -if, -if-expr: +if → adds _negated: true to the arg map.
// Other arg flags with +: error (unsupported negation).
func ssqlPrefixHandler(flagName string, hasPlus bool, value interface{}) interface{} {
	if !hasPlus {
		return value
	}
	// Boolean flags: negate
	if b, ok := value.(bool); ok {
		return !b
	}
	// Multi-arg flags: only -if and -if-expr support negation
	if m, ok := value.(map[string]any); ok {
		if !negatableArgFlags[flagName] {
			fmt.Fprintf(os.Stderr, "Error: +%s is not supported — only +if and +if-expr support negation\n", flagName[1:])
			os.Exit(1)
		}
		m["_negated"] = true
		return m
	}
	return value
}

func buildRootCommand() *cf.Command {
	cmd := cf.NewCommand("ssql").
		Version(version.Version).
		Description("Unix-style data processing tools").
		PrefixHandler(ssqlPrefixHandler).

		// Root global flags
		Flag("-verbose", "-v").
		Bool().
		Global().
		Help("Enable verbose output").
		Done().

		// Registering -shell-helpers as a global Bool flag makes it
		// show up in `ssql -help` and (more importantly) gets picked
		// up by the bash-completion script — `ssql -she<TAB>` then
		// expands to `-shell-helpers`. The actual handler is in
		// main(), intercepting before autocli dispatches: we print
		// the helper script and exit, same as autocli does for
		// -completion-script.
		Flag("-shell-helpers").
		Bool().
		Global().
		Help("Print bash helper functions (eval \"$(ssql -shell-helpers)\" in ~/.bashrc to install)").
		Done()

	// Register all subcommands
	cmd = commands.RegisterVersion(cmd)
	cmd = commands.RegisterFunctions(cmd)
	cmd = commands.RegisterFrom(cmd)
	cmd = commands.RegisterLimit(cmd)
	cmd = commands.RegisterOffset(cmd)
	cmd = commands.RegisterSort(cmd)
	cmd = commands.RegisterTop(cmd)
	cmd = commands.RegisterDistinct(cmd)
	cmd = commands.RegisterCount(cmd)
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

	// Root handler (when no subcommand specified)
	return cmd.Handler(func(ctx *cf.Context) error {
		fmt.Println("ssql - Unix-style data processing tools")
		fmt.Println()
		fmt.Println("Use -help to see available subcommands")
		fmt.Println()
		binaryName := filepath.Base(os.Args[0])
		fmt.Println("To enable tab completion, add to your ~/.bashrc:")
		fmt.Printf("  eval \"$(%s -completion-script)\"\n", binaryName)
		return nil
	}).Build()
}

func main() {
	// Intercept -shell-helpers before autocli sees it. Same pattern
	// as -completion-script (handled by autocli internally), but the
	// helper-functions output isn't autocli's concern. Users source
	// it with `eval "$(ssql -shell-helpers)"` in ~/.bashrc.
	for _, a := range os.Args[1:] {
		if a == "-shell-helpers" || a == "--shell-helpers" {
			fmt.Print(commands.ShellHelpersScript)
			return
		}
		// Field-completion keybinding (bind -x). Sourced like
		// -shell-helpers: eval "$(ssql -field-keybinding)". WriteString
		// (not fmt.Print) because the bash body contains literal %s that
		// vet would otherwise flag as a stray Printf directive.
		if a == "-field-keybinding" || a == "--field-keybinding" {
			os.Stdout.WriteString(commands.FieldKeybindingScript)
			return
		}
	}
	cmd := buildRootCommand()
	if err := cmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
