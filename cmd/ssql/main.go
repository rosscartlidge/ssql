package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
		fmt.Println("Shell integration — add this one line to your ~/.bashrc:")
		fmt.Printf("  eval \"$(%s -shell-init)\"     # enables everything below in one eval\n", binaryName)
		fmt.Println()
		fmt.Println("…or enable pieces individually:")
		// Align the trailing comments. Driven by commands.ShellIntegrations so
		// new integrations appear here automatically.
		maxFlag := 0
		for _, si := range commands.ShellIntegrations {
			if len(si.Flag) > maxFlag {
				maxFlag = len(si.Flag)
			}
		}
		for _, si := range commands.ShellIntegrations {
			line := fmt.Sprintf("  eval \"$(%s %s)\"", binaryName, si.Flag)
			pad := maxFlag - len(si.Flag) + 2
			fmt.Printf("%s%*s# %s\n", line, pad, "", si.Desc)
		}
		return nil
	}).Build()
}

func main() {
	// Paren-aware cursor-context helpers for the Ctrl-O / Alt-h keybindings.
	// They take the line up to the cursor as the next arg and print a single
	// value; WriteString (not fmt.Print) because the text may contain %.
	// See cursor_context.go for why the bash split couldn't do this itself.
	if len(os.Args) >= 3 && os.Args[1] == "-complete-source" {
		os.Stdout.WriteString(completeSource(os.Args[2]))
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "-cursor-stage" {
		os.Stdout.WriteString(cursorTopLevelStage(os.Args[2]))
		return
	}
	// -help-at: autocli renders help for the flag/command under the cursor. We
	// intercept (rather than let it flow to autocli) only to ALSO append the
	// expression-function reference when the cursor is on an expression
	// argument — writing an expression is hard without knowing the functions.
	if len(os.Args) >= 3 && os.Args[1] == "-help-at" {
		pos, perr := strconv.Atoi(os.Args[2])
		args := os.Args[3:]
		if perr != nil {
			fmt.Fprintf(os.Stderr, "invalid position: %s\n", os.Args[2])
			os.Exit(1)
		}
		help, herr := buildRootCommand().HelpAt(args, pos)
		if herr != nil {
			fmt.Fprintf(os.Stderr, "%v\n", herr)
			os.Exit(1)
		}
		if exprArgAtCursor(args, pos) {
			help = strings.TrimRight(help, "\n") + "\n\n" + commands.FunctionsReference
		}
		os.Stdout.WriteString(help)
		return
	}

	// Shell-integration scripts (eval'd in ~/.bashrc). All driven by the
	// commands.ShellIntegrations table — the single source of truth — so a new
	// integration is wired everywhere at once. -completion-script is handled by
	// autocli, not here; the const-script ones (keybindings, -shell-helpers)
	// are intercepted before autocli. WriteString (not fmt.Print) because the
	// bash bodies contain literal %s that vet would flag as Printf directives.
	for _, a := range os.Args[1:] {
		// -shell-init: emit EVERYTHING in one eval (completion + every script).
		if a == "-shell-init" || a == "--shell-init" {
			os.Stdout.WriteString(buildRootCommand().GenerateCompletionScript())
			for _, si := range commands.ShellIntegrations {
				if si.Script != "" {
					os.Stdout.WriteString(si.Script)
				}
			}
			return
		}
		for _, si := range commands.ShellIntegrations {
			if si.Script != "" && (a == si.Flag || a == "-"+si.Flag) {
				os.Stdout.WriteString(si.Script)
				return
			}
		}
	}
	cmd := buildRootCommand()
	if err := cmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
