package commands

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// RegisterVersion registers the version subcommand
func RegisterVersion(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("version").
		Description("Show version information").
		Example("ssql version", "Display the current ssql version").
		Handler(func(ctx *cf.Context) error {
			gpuStatus := "no"
			if ssql.GPUAvailable() {
				gpuStatus = "yes"
			}
			fmt.Printf("ssql v%s (build: %s, gpu: %s)\n", version.Version, version.Commit, gpuStatus)
			return nil
		}).
		Done()
	return cmd
}
