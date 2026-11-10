//go:build slim

package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterLsp is the slim-build stub: the language server runs pipelines
// on the editor's machine (an upstream's schema, a value sample, a
// check), which a browser or WebVM build has no shell for.
func RegisterLsp(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("lsp").
		Description("Serve the Language Server Protocol on stdio (UNAVAILABLE in slim build)").
		Handler(func(ctx *cf.Context) error {
			return fmt.Errorf("ssql lsp: requires a non-slim build (the language server runs pipelines on this machine). Install the full ssql binary")
		}).
		Done()
	return cmd
}
