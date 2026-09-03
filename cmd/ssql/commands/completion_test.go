package commands

import (
	"slices"
	"strings"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

// TestFieldCompletionConfiguration verifies that all commands that accept field names
// have proper field completion configured (FieldsFromFlag) instead of NoCompleter.
// This test prevents regression where field completion is accidentally removed.
func TestFieldCompletionConfiguration(t *testing.T) {
	// Build the command tree
	cmd := cf.NewCommand("ssql")

	// Register all commands
	RegisterFrom(cmd)
	RegisterWhere(cmd)
	RegisterUpdate(cmd)
	RegisterCast(cmd)
	RegisterJoin(cmd)
	RegisterUnion(cmd)
	RegisterInclude(cmd)
	RegisterExclude(cmd)
	RegisterRename(cmd)
	RegisterSort(cmd)
	RegisterResample(cmd)
	RegisterDescribe(cmd)
	RegisterGroupBy(cmd)
	RegisterTo(cmd)
	RegisterConvolve(cmd)
	RegisterCorrelate(cmd)
	RegisterFFT(cmd)
	RegisterIFFT(cmd)
	RegisterMerge(cmd)

	// Define expected field completion for each command
	// Format: command -> flag -> arg index that should have field completion
	expectedFieldCompletion := map[string]map[string][]int{
		"where": {
			"-if": {0}, // field is arg 0
		},
		"update": {
			"-if":       {0}, // field is arg 0
			"-set":      {0}, // field is arg 0
			"-set-expr": {0}, // field is arg 0
		},
		"cast": {
			"-type": {0}, // field is arg 0
		},
		"join": {
			"-using": {0}, // same field name both sides
			"-on":    {0}, // left-field is arg 0 (right-field is arg 1, not completed from fields)
		},
		"include": {
			"FIELDS": {0}, // variadic fields
		},
		"exclude": {
			"FIELDS": {0}, // variadic fields
		},
		"merge": {
			"-if": {0}, // catalog metadata field is arg 0 (from -catalog CSV header)
		},
		"rename": {
			"-as": {0}, // old-field is arg 0
		},
		"sort": {
			"FIELDS": {0}, // sort fields (variadic)
		},
		"resample": {
			"-time":  {0}, // timestamp field
			"-value": {0}, // value field (accumulate)
		},
		"describe": {
			"FIELDS": {0}, // fields to profile (variadic)
		},
		"group-by": {
			"FIELDS":   {0}, // group fields (variadic)
			"-sum":     {0}, // field is arg 0
			"-avg":     {0}, // field is arg 0
			"-min":     {0}, // field is arg 0
			"-max":     {0}, // field is arg 0
			"-collect": {0}, // field is arg 0
			// Note: -count only takes result-name, not a field to count
		},
		// Nested subcommands use "parent/child" notation
		"to/table": {
			"FIELDS": {0}, // fields to display (variadic)
		},
		"to/chart": {
			"-x": {0}, // x-axis field
			"-y": {0}, // y-axis field
		},
		"from/catalog": {
			"-if": {0}, // catalog field is arg 0
		},
		"convolve": {
			"-field": {0}, // signal field
		},
		"correlate": {
			"-field": {0}, // primary signal field
			"-with":  {0}, // second signal field
		},
		"fft": {
			"-field": {0}, // signal field
		},
		"ifft": {
			"-magnitude": {0}, // magnitude field
			"-phase":     {0}, // phase field
		},
	}

	// Define expected value completion (FieldValuesFrom)
	expectedValueCompletion := map[string]map[string][]int{
		"where": {
			"-if": {2}, // value is arg 2 (field, operator, value)
		},
		"update": {
			"-if":  {2}, // value is arg 2
			"-set": {1}, // value is arg 1 (field, value)
		},
	}

	built := cmd.Build()

	for subcmdName, flags := range expectedFieldCompletion {
		t.Run(subcmdName+"_field_completion", func(t *testing.T) {
			subcmd := findSubcommand(built, subcmdName)
			if subcmd == nil {
				t.Fatalf("subcommand %q not found", subcmdName)
			}

			for flagName, argIndices := range flags {
				flag := findFlag(subcmd, flagName)
				if flag == nil {
					t.Errorf("flag %q not found in %q", flagName, subcmdName)
					continue
				}

				for _, argIdx := range argIndices {
					if argIdx >= len(flag.ArgCompleters) {
						t.Errorf("%s %s: arg index %d out of range (have %d completers)",
							subcmdName, flagName, argIdx, len(flag.ArgCompleters))
						continue
					}

					completer := flag.ArgCompleters[argIdx]
					if completer == nil {
						t.Errorf("%s %s arg[%d]: completer is nil, expected FieldCompleter",
							subcmdName, flagName, argIdx)
						continue
					}

					// Check it's a FieldCompleter (not NoCompleter)
					if _, ok := completer.(*cf.FieldCompleter); !ok {
						if _, isNone := completer.(cf.NoCompleter); isNone {
							t.Errorf("%s %s arg[%d]: using NoCompleter, should use FieldsFromFlag(\"\") for field completion",
								subcmdName, flagName, argIdx)
						}
					}
				}
			}
		})
	}

	for subcmdName, flags := range expectedValueCompletion {
		t.Run(subcmdName+"_value_completion", func(t *testing.T) {
			subcmd := findSubcommand(built, subcmdName)
			if subcmd == nil {
				t.Fatalf("subcommand %q not found", subcmdName)
			}

			for flagName, argIndices := range flags {
				flag := findFlag(subcmd, flagName)
				if flag == nil {
					t.Errorf("flag %q not found in %q", flagName, subcmdName)
					continue
				}

				for _, argIdx := range argIndices {
					if argIdx >= len(flag.ArgCompleters) {
						t.Errorf("%s %s: arg index %d out of range (have %d completers)",
							subcmdName, flagName, argIdx, len(flag.ArgCompleters))
						continue
					}

					completer := flag.ArgCompleters[argIdx]
					if completer == nil {
						t.Errorf("%s %s arg[%d]: completer is nil, expected FieldValueCompleter",
							subcmdName, flagName, argIdx)
						continue
					}

					// Check it's a FieldValueCompleter (not NoCompleter)
					if _, ok := completer.(*cf.FieldValueCompleter); !ok {
						if _, isNone := completer.(cf.NoCompleter); isNone {
							t.Errorf("%s %s arg[%d]: using NoCompleter, should use FieldValuesFrom(\"\", \"field\") for value completion",
								subcmdName, flagName, argIdx)
						}
					}
				}
			}
		})
	}
}

// findSubcommand finds a subcommand by name in the command tree
// Supports nested paths like "to/table" for nested subcommands
func findSubcommand(cmd *cf.Command, name string) *cf.Subcommand {
	parts := strings.Split(name, "/")
	if len(parts) == 1 {
		return cmd.GetSubcommand(name)
	}

	// Handle nested subcommands
	parent := cmd.GetSubcommand(parts[0])
	if parent == nil {
		return nil
	}
	for _, part := range parts[1:] {
		if parent.Subcommands == nil {
			return nil
		}
		child, ok := parent.Subcommands[part]
		if !ok {
			return nil
		}
		parent = child
	}
	return parent
}

// findFlag finds a flag by name in a subcommand
func findFlag(subcmd *cf.Subcommand, name string) *cf.FlagSpec {
	for _, flag := range subcmd.Flags {
		if slices.Contains(flag.Names, name) {
			return flag
		}
	}
	return nil
}
