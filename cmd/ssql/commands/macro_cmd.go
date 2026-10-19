package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterMacro registers `ssql macro`, the protocol surface of the macros
// file (DFC152 §7.4): `list` prints what is defined (what the completer
// offers), `check` compiles a file and reports every bad line. The file
// is the user's to edit; there is no add or remove.
func RegisterMacro(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	macroCmd := cmd.Subcommand("macro").
		Description("Expression macros: the named expressions of ~/.config/ssql/macros (or $SSQL_MACROS) that update -set-macro and where -if-macro call").
		Example("ssql macro list", "Show every macro with its parameters and body").
		Example("ssql macro check", "Compile the macros file and report any bad line").
		Example("ssql macro check ./macros", "Check a file before pointing SSQL_MACROS at it")

	macroCmd.Subcommand("list").
		Description("List the defined macros, one definition per line, in the file's canonical spelling").
		Example("ssql macro list", "Show every macro").
		Example("SSQL_MACROS=./macros ssql macro list", "Show the macros of another file").

		Handler(func(ctx *cf.Context) error {
			set, err := loadMacros()
			if err != nil {
				return err
			}
			if len(set) == 0 {
				fmt.Fprintf(os.Stderr, "no macros defined (%s)\n", macroFileLabel())
				return nil
			}
			for _, n := range macroNames(set) {
				fmt.Fprintln(ctx.Stdout(), set[n].String())
			}
			return nil
		}).
		Done()

	macroCmd.Subcommand("check").
		Description("Compile a macros file (default: the one in use) and report every bad line; exit 1 on any").
		Example("ssql macro check", "Check the file in use").
		Example("ssql macro check ./macros", "Check a file by path").

		Flag("FILE").
			String().
			Default("").
			Completer(&cf.FileCompleter{Hint: "<FILE>"}).
			Global().
			Help("The file to check (default: $SSQL_MACROS or ~/.config/ssql/macros)").
			Done().

		Handler(func(ctx *cf.Context) error {
			path, _ := ctx.GlobalFlags["FILE"].(string)
			if path == "" {
				path = macroFile()
			}
			set, err := parseMacroFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(ctx.Stdout(), "%s: %d macro(s) ok\n", path, len(set))
			return nil
		}).
		Done()

	macroCmd.Done()
	return cmd
}
