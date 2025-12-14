package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// RegisterVersion registers the version subcommand
func RegisterVersion(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("version").
		Description("Show version information").
		Example("ssql version", "Display the current ssql version").
		Handler(func(ctx *cf.Context) error {
			fmt.Printf("ssql v%s\n", version.Version)
			return nil
		}).
		Done()
	return cmd
}
