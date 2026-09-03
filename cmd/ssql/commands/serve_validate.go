//go:build !slim

package commands

import (
	"fmt"
	"strings"
)

// serveSourceCommands are the console commands that make sense as a
// pipeline's FIRST stage: the dataset source and the state shortcuts.
// Everything else reads its stdin — which, for stage 0, is empty by the
// shell's design — so a pipeline starting with a transform would run
// and print nothing. Refuse loudly instead (nothing implicit).
var serveSourceCommands = map[string]bool{
	"from-loaded": true,
	"status":      true,
	"schema":      true,
}

// serveValidatePipeline is the shell's pre-execution hook: the first
// stage must be a source (or a help request). Returns a hint that
// names the fix rather than letting `where … | to table` succeed
// silently with zero rows.
func serveValidatePipeline(stages [][]string) error {
	if len(stages) == 0 || len(stages[0]) == 0 {
		return nil
	}
	first := stages[0][0]
	if serveSourceCommands[first] || strings.HasPrefix(first, "-") || first == "help" {
		return nil
	}
	// A transform's own -help is fine as a single command.
	if len(stages) == 1 {
		for _, a := range stages[0][1:] {
			if a == "-help" || a == "-h" || a == "-man" {
				return nil
			}
		}
	}
	return fmt.Errorf("pipeline has no source: %q reads its input, and there is none — start with from-loaded (the in-memory dataset), e.g.\n  from-loaded | %s", first, strings.Join(stages[0], " "))
}
