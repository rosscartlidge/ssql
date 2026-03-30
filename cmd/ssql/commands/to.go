package commands

import (
	"strconv"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterTo registers the to subcommand with nested format subcommands
func RegisterTo(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	toCmd := cmd.Subcommand("to").
		Description("Write output in various formats (table, csv, tsv, json, jsonl, xlsx, chart, animate, explore, wav)")

	// Register nested subcommands
	registerToTable(toCmd)
	registerToCSV(toCmd)
	registerToTSV(toCmd)
	registerToJSON(toCmd)
	registerToJSONL(toCmd)
	registerToArrow(toCmd)
	registerToParquet(toCmd)
	registerToWAV(toCmd)
	registerToXLSX(toCmd)
	registerToChart(toCmd)
	registerToAnimate(toCmd)
	registerToExplore(toCmd)

	toCmd.Done()
	return cmd
}

// joinStrings joins strings with a separator (avoiding strings import for simple case)
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString(strs[0])
	for _, s := range strs[1:] {
		result.WriteString(sep + s)
	}
	return result.String()
}

// parseFloat parses a string as float64
func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
