package commands

import (
	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterGenerate registers the "generate" parent subcommand with go, sql, and ssql children.
func RegisterGenerate(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	genCmd := cmd.Subcommand("generate").
		Description("Generate code, SQL, or optimized pipelines from ssql fragments").
		Example("(export SSQLGO=1; ssql from data.csv | ssql where -if age gt 25 | ssql to table) | ssql generate go", "Generate Go code").
		Example("... | ssql generate sql", "Generate DuckDB SQL").
		Example("... | ssql generate ssql", "Optimize pipeline")

	registerGenerateGo(genCmd)
	registerGenerateSQL(genCmd)
	registerGenerateSSQL(genCmd)

	genCmd.Done()
	return cmd
}
