package commands

// `-empty zero` on the delimited readers: an empty numeric or boolean
// cell is read as the type's zero (0, false) instead of as a missing
// value. The default stays DFC124's: an empty cell is absent, and the
// typed lane refuses it loudly because a struct cannot hold absence. The
// flag is the user's explicit word that a blank means nothing-to-add (a
// sales sheet with blank cells), and every lane reads it so: the record
// reader through ssql.CSVConfig.EmptyAsZero, a typed program through
// typed.EmptyAsZero / DelimEmptyAsZero, generate sql through COALESCE.
// It belongs on `from` by the source-only rule: `fill -default` can do
// this after the read in the record lanes, but the typed reader has
// already failed before fill runs — only the source can choose the
// decoder, and it is one pass instead of two.

import (
	"fmt"

	cf "github.com/rosscartlidge/autocli/v4"
)

// fromEmptyZero is set by the source handler for this process's one
// read and read by the config builders — the fromTimeFormats shape.
var fromEmptyZero bool

const emptyFlagHelp = "An empty numeric or boolean cell: missing (default; the typed lane refuses it) or zero (0, false — the blank means nothing to add)"

// emptyFlag adds -empty missing|zero to a delimited reader.
func emptyFlag(b *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return b.
		Flag("-empty").
		String().
		Global().
		Default("missing").
		Completer(&cf.StaticCompleter{Options: []string{"missing", "zero"}}).
		Help(emptyFlagHelp).
		Done()
}

// applyEmptyFlag reads -empty and records it for the config builders.
func applyEmptyFlag(ctx *cf.Context) error {
	v, _ := ctx.GlobalFlags["-empty"].(string)
	switch v {
	case "", "missing":
		fromEmptyZero = false
	case "zero":
		fromEmptyZero = true
	default:
		return fmt.Errorf("-empty %s: choose missing or zero", v)
	}
	return nil
}

// emptyZeroFromArgs reads `-empty zero` from a stage's argv (the SQL
// translator's view of the source).
func emptyZeroFromArgs(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-empty" {
			return args[i+1] == "zero"
		}
	}
	return false
}

// emptyOptionCode renders the typed reader option for the flag: `,
// typed.EmptyAsZero()` (fn: EmptyAsZero or DelimEmptyAsZero) or "".
func emptyOptionCode(fn string) string {
	if !fromEmptyZero {
		return ""
	}
	return ", typed." + fn + "()"
}
