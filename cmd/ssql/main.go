package main

import (
	"fmt"
	"os"
	"path/filepath"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/commands"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

func buildRootCommand() *cf.Command {
	cmd := cf.NewCommand("ssql").
		Version(version.Version).
		Description("Unix-style data processing tools").

		// Root global flags
		Flag("-verbose", "-v").
		Bool().
		Global().
		Help("Enable verbose output").
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
	cmd = commands.RegisterFFT(cmd)
	cmd = commands.RegisterIFFT(cmd)
	cmd = commands.RegisterConvolve(cmd)
	cmd = commands.RegisterCorrelate(cmd)
	cmd = commands.RegisterSpectrogram(cmd)
	cmd = commands.RegisterTo(cmd)
	cmd = commands.RegisterGenerateGo(cmd)

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
	cmd := buildRootCommand()
	if err := cmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
