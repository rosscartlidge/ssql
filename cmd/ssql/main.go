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
		Done()

	// Register all subcommands
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
	// Paren-aware cursor-context protocol (-complete-source, -cursor-stage,
	// -help-at) for the Ctrl-O / Alt-h keybindings — shared with the browser
	// playground via commands.HandleCursorProtocol. WriteString (not
	// fmt.Print) because the text may contain %. See commands/cursor_context.go
	// for why the bash split couldn't do this itself.
	if out, errOut, code, ok := commands.HandleCursorProtocol(os.Args[1:], buildRootCommand); ok {
		os.Stdout.WriteString(out)
		os.Stderr.WriteString(errOut)
		if code != 0 {
			os.Exit(code)
		}
		return
	}

	// Shell-integration scripts (eval'd in ~/.bashrc). All driven by the
	// commands.ShellIntegrations table — the single source of truth — so a new
	// integration is wired everywhere at once. -completion-script is handled by
	// autocli, not here; the const-script ones (keybindings)
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
