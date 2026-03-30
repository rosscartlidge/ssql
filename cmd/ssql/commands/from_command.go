package commands

import (
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

func registerFromCommand(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("command").
		Description("Execute a command and read its output").
		Example("ssql from command -- ps aux | ssql where -if USER eq root", "Parse ps output").
		Example("ssql from command -- docker ps | ssql to table", "Parse docker output").

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			var generate bool
			if genVal, ok := ctx.GlobalFlags["-generate"]; ok {
				generate = genVal.(bool)
			}

			if len(ctx.RemainingArgs) == 0 {
				return fmt.Errorf("usage: ssql from command -- <command> [args...]")
			}

			command := ctx.RemainingArgs[0]
			args := ctx.RemainingArgs[1:]

			if shouldGenerate(generate) {
				return generateFromExecCode(command, args)
			}

			records, err := ssql.ExecCommand(command, args)
			if err != nil {
				return fmt.Errorf("executing command: %w", err)
			}

			return writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
		}).
		Done()
}

// generateFromExecCode generates Go code for from command with command execution
func generateFromExecCode(command string, args []string) error {
	// Build the args slice literal
	var argsCode string
	if len(args) == 0 {
		argsCode = "nil"
	} else {
		quotedArgs := make([]string, len(args))
		for i, arg := range args {
			quotedArgs[i] = fmt.Sprintf("%q", arg)
		}
		argsCode = fmt.Sprintf("[]string{%s}", strings.Join(quotedArgs, ", "))
	}

	code := fmt.Sprintf(`records, err := ssql.ExecCommand(%q, %s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", fmt.Errorf("executing command: %%w", err))
		os.Exit(1)
	}`, command, argsCode)

	imports := []string{"fmt", "os"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	return lib.WriteCodeFragment(frag)
}
