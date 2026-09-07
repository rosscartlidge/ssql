package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	codelabdata "github.com/rosscartlidge/ssql/v4/doc/codelab-data"
)

// RegisterCodelab adds `ssql codelab [DIR]`, which writes the CLI
// codelab's fixture files (embedded in the binary from
// doc/codelab-data) into a directory. The tutorial's setup is then
// `go install …` + `ssql codelab` + `cd ssql-codelab` — no clone of the
// repository for 44 KB of data.
func RegisterCodelab(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("codelab").
		Description("Write the CLI codelab's sample data files to a directory (default ssql-codelab)").
		Example("ssql codelab", "Create ./ssql-codelab with the tutorial's files, then `cd ssql-codelab`").
		Example("ssql codelab ~/play", "Write the files into an existing directory of your choice").
		Example("ssql codelab -force", "Restore the original files after editing them during the tutorial").

		Flag("DIR").
			String().
			Default("ssql-codelab").
			Completer(&cf.FileCompleter{DirsOnly: true, Hint: "<DIR>"}).
			Global().
			Help("Directory to write into (created if missing)").
			Done().

		Flag("-force", "-f").
			Bool().
			Global().
			Help("Overwrite files that already exist in DIR").
			Done().

		Handler(func(ctx *cf.Context) error {
			dir := "ssql-codelab"
			if v, ok := ctx.GlobalFlags["DIR"].(string); ok && v != "" {
				dir = v
			}
			force, _ := ctx.GlobalFlags["-force"].(bool)

			written, err := codelabdata.Write(dir, force)
			if err != nil {
				return fmt.Errorf("codelab: %w", err)
			}
			for _, path := range written {
				fmt.Fprintln(ctx.Stdout(), path)
			}
			fmt.Fprintf(ctx.Stdout(), "\n%d files written. Next:\n  cd %s\n  eval \"$(ssql -shell-init)\"     # Tab / Ctrl-O / Alt-h completion\n  ssql from employees.csv | ssql to table\nFollow along: https://github.com/rosscartlidge/ssql/blob/main/doc/cli-codelab.md\nSelf-test: ./codelab-run.sh runs every block of the codelab against this ssql.\n", len(written), dir)
			return nil
		}).
		Done()
	return cmd
}
