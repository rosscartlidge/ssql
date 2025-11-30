package commands

import (
	"fmt"
	"os"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v3"
	"github.com/rosscartlidge/ssql/v3/cmd/ssql/lib"
)

// RegisterExec registers the exec subcommand
func RegisterExec(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("exec").
		Description("Execute command and parse output as records").
		Example("ssql exec -- ps aux | ssql where -match USER eq root", "Parse ps output and filter for root processes").
		Example("ssql exec -- ls -la | ssql include FILE SIZE", "Parse ls output and select specific fields").
		Handler(func(ctx *cf.Context) error {
			// Everything after -- is in ctx.RemainingArgs
			if len(ctx.RemainingArgs) == 0 {
				return fmt.Errorf("exec requires command after '--' separator (usage: ssql exec -- command args...)")
			}

			command := ctx.RemainingArgs[0]
			args := ctx.RemainingArgs[1:]

			// Execute command and parse output
			records, err := ssql.ExecCommand(command, args)
			if err != nil {
				return fmt.Errorf("executing command: %w", err)
			}

			// Write as JSONL to stdout
			if err := lib.WriteJSONL(os.Stdout, records); err != nil {
				return fmt.Errorf("writing JSONL: %w", err)
			}

			return nil
		}).
		Done()
	return cmd
}
