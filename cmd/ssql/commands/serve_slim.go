//go:build slim

package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterServe is the slim-build stub. The full implementation
// lives in serve.go behind the !slim build tag — slim binaries
// (WASM, WebVM, etc.) exclude crypto/ssh and chzyer/readline.
func RegisterServe(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("serve").
		Description("Run an SSH-accessible operator console (UNAVAILABLE in slim build)").
		Handler(func(ctx *cf.Context) error {
			return fmt.Errorf("ssql serve: requires a non-slim build (slim excludes crypto/ssh and chzyer/readline). Install the full ssql binary.")
		}).
		Done()
	return cmd
}
