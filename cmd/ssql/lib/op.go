package lib

import "os"

// Op is the language-neutral operation descriptor on a CodeFragment
// (DFC123 slice 2, DFC099 §4a): what the stage MEANS, structurally,
// alongside the command-owned Go lowering in Code.
//
// AUTHORITY INVARIANT (DFC123 §5): Op describes; Code lowers. Op's
// consumers are the backends that cannot call Go functions anyway —
// today the `generate ssql` optimiser (which previously re-tokenized
// the shell-quoted Command string, a lossy round-trip: its tokenizer
// cannot even represent an embedded single quote). Backends MUST fall
// back to Command-string parsing when Op is absent: fragments can
// arrive from an older ssql across an SSH boundary, and version skew
// must degrade to the old behavior, never to a wrong parse.
//
// Argv is the stage's own argument vector (after the command name),
// taken verbatim from the emitting process's os.Args — lossless by
// construction, no quoting round-trip. Kind is the command name
// (os.Args[1]). Fields/Args carry per-command structured facts and
// are populated incrementally as backends grow consumers for them
// (slice 3: the SQL translator).
type Op struct {
	Kind   string         `json:"kind"`
	Argv   []string       `json:"argv,omitempty"`
	Fields []string       `json:"fields,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
}

// opFromProcessArgs builds the Op for the CURRENT process's stage from
// os.Args, mirroring getCommandString's -generate/-g filtering so Op
// and Command always describe the same invocation. Returns nil when
// the process has no subcommand (nothing to describe).
//
// Called only by the fragment constructors, and only for fragments
// carrying a non-empty Command: a command's continuation fragments
// (e.g. group-by's second fragment) pass command == "" and stay
// Op-less, exactly as they are Command-less — one stage, one Op.
func opFromProcessArgs() *Op {
	if len(os.Args) < 2 {
		return nil
	}
	op := &Op{Kind: os.Args[1]}
	for _, a := range os.Args[2:] {
		if a == "-generate" || a == "-g" {
			continue
		}
		op.Argv = append(op.Argv, a)
	}
	return op
}
