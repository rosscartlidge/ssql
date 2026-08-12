package commands

import (
	"slices"

	"github.com/rosscartlidge/ssql/v4"
)

// Schema-aware completion (SSQL_MODE=schema, slices 5 + Phase 2).
//
// Each data command can declare a schemaOp: given the session state
// (serve only), the field names flowing IN, and the command's own args,
// it returns the field names flowing OUT. The same per-command rules
// drive two runtimes:
//
//   - serve: serveSchemaWalk (serve_schema.go) threads an upstream
//     pipeline through the ops in-process for `<cmd> <TAB>` completion.
//   - bash: SSQL_MODE=schema runs each command as a subprocess that
//     transforms a schema header (schema_mode.go).
//
// Commands without a registered op are treated as identity (they don't
// change the set of field names) — correct for where/sort/limit/etc.
// Sources MUST register an explicit op since they produce a schema from
// state (from-loaded) or a file (from csv …) rather than their input.
//
// This file (untagged, so it compiles into slim builds too) holds the
// registry + the runtime-agnostic transform ops. The serve-only,
// serveState-dependent pieces live in serve_schema.go (!slim).
//
// See doc/research/schema-aware-completion.md §0.

// schemaOp computes the output field names of a command given the
// session state, the input field names, and the command's args (the
// stage tokens AFTER the command name). ok=false means the schema is
// undeterminable (e.g. pivot, whose columns depend on data values), in
// which case the walk reports failure and completion seeds no fields.
type schemaOp func(state any, in []string, args []string) (out []string, ok bool)

var schemaOps = map[string]schemaOp{}

// registerSchemaOp records op under a command name. Last registration
// wins; intended to be called from init() functions.
func registerSchemaOp(name string, op schemaOp) {
	schemaOps[name] = op
}

// lookupSchemaOp returns the op for a command, or identitySchemaOp when
// none is registered.
func lookupSchemaOp(name string) schemaOp {
	if op, ok := schemaOps[name]; ok {
		return op
	}
	return identitySchemaOp
}

// identitySchemaOp passes the input field names through unchanged.
func identitySchemaOp(_ any, in []string, _ []string) ([]string, bool) {
	return in, true
}

// --- argv decoding -------------------------------------------------------
//
// autocli's Command.Parse parses a leaf command's flags but does NOT
// descend into subcommands, so a schemaOp can't cheaply turn raw stage
// args into the handler's parsed Context (and thus can't reuse
// parseGroupBySpecs). The ops below hand-decode argv with walkStage and
// a per-command flag→arity map. The maps necessarily mirror each
// command's flag definitions; the schema-shadow corpus test (design §9,
// future) is what guards them against drift.

// isFlagTok reports whether tok begins a flag (-x or +x).
func isFlagTok(tok string) bool {
	return len(tok) > 0 && (tok[0] == '-' || tok[0] == '+')
}

// normFlag normalises a flag token: a leading '+' (negation form)
// becomes '-' so arity lookups match either spelling.
func normFlag(tok string) string {
	if len(tok) > 0 && tok[0] == '+' {
		return "-" + tok[1:]
	}
	return tok
}

type stageFlag struct {
	name string
	args []string
}

// walkStage splits raw stage args (tokens after the command name) into
// leading positionals and recognised flags, using a flag→arity map.
// Unknown dash-tokens are treated as 0-arity (bool). A flag short of
// its declared args (mid-typing) keeps whatever args are present.
func walkStage(args []string, arity map[string]int) (positionals []string, flags []stageFlag) {
	i := 0
	for i < len(args) && !isFlagTok(args[i]) {
		positionals = append(positionals, args[i])
		i++
	}
	for i < len(args) {
		if !isFlagTok(args[i]) {
			i++ // stray positional after a flag — ignore
			continue
		}
		name := normFlag(args[i])
		n := arity[name]
		var fa []string
		for j := 1; j <= n && i+j < len(args); j++ {
			fa = append(fa, args[i+j])
		}
		flags = append(flags, stageFlag{name: name, args: fa})
		i += 1 + n
	}
	return
}

// keepPresent filters names to those present in `in`, preserving the
// order of `names`. When `in` is nil (upstream schema unknown) all
// names pass through.
func keepPresent(in, names []string) []string {
	var out []string
	for _, f := range names {
		if in == nil || slices.Contains(in, f) {
			out = append(out, f)
		}
	}
	return out
}

func init() {
	// rename: `-as old new` (accumulated) → replace old with new in place.
	registerSchemaOp("rename", func(_ any, in []string, args []string) ([]string, bool) {
		out := slices.Clone(in)
		_, flags := walkStage(args, map[string]int{"-as": 2, "-generate": 0, "-g": 0})
		for _, f := range flags {
			if f.name == "-as" && len(f.args) == 2 {
				for i := range out {
					if out[i] == f.args[0] {
						out[i] = f.args[1]
					}
				}
			}
		}
		return out, true
	})

	// include: keep the listed fields, in listed order.
	registerSchemaOp("include", func(_ any, in []string, args []string) ([]string, bool) {
		pos, _ := walkStage(args, map[string]int{"-generate": 0, "-g": 0})
		return keepPresent(in, pos), true
	})

	// exclude: drop the listed fields.
	registerSchemaOp("exclude", func(_ any, in []string, args []string) ([]string, bool) {
		pos, _ := walkStage(args, map[string]int{"-generate": 0, "-g": 0})
		drop := make(map[string]bool, len(pos))
		for _, f := range pos {
			drop[f] = true
		}
		var out []string
		for _, f := range in {
			if !drop[f] {
				out = append(out, f)
			}
		}
		return out, true
	})

	// update: `-set`/`-set-expr FIELD …` → append FIELD if new.
	registerSchemaOp("update", func(_ any, in []string, args []string) ([]string, bool) {
		out := slices.Clone(in)
		_, flags := walkStage(args, map[string]int{
			"-set": 2, "-s": 2, "-set-expr": 2, "-e": 2,
			"-if": 3, "-i": 3, "-if-expr": 1, "-x": 1, "-generate": 0, "-g": 0,
		})
		for _, f := range flags {
			if (f.name == "-set" || f.name == "-set-expr") && len(f.args) >= 1 {
				if !slices.Contains(out, f.args[0]) {
					out = append(out, f.args[0])
				}
			}
		}
		return out, true
	})

	// group-by: output = group keys (positionals) + aggregation result
	// names (the last arg of each agg flag). -rollup/-cube enrich with
	// per-grouping-set prefixed result columns — fully determinable
	// from argv (unlike pivot, whose columns are data values): the same
	// computeGroupingSetsForSchema the exec path uses.
	registerSchemaOp("group-by", func(_ any, in []string, args []string) ([]string, bool) {
		pos, flags := walkStage(args, map[string]int{
			"-count": 1, "-sum": 2, "-avg": 2, "-min": 2, "-max": 2,
			"-collect": 2, "-expr": 2, "-stream-expr": 4,
			"-rollup": 0, "-cube": 0, "-presorted": 0, "-generate": 0, "-g": 0,
		})
		out := keepPresent(in, pos)
		var results []string
		rollupMode := ssql.RollupMode(-1)
		for _, f := range flags {
			switch f.name {
			case "-rollup":
				rollupMode = ssql.RollupHierarchical
			case "-cube":
				rollupMode = ssql.RollupCube
			case "-count", "-sum", "-avg", "-min", "-max", "-collect", "-expr", "-stream-expr":
				if len(f.args) > 0 {
					results = append(results, f.args[len(f.args)-1])
				}
			}
		}
		if rollupMode == ssql.RollupMode(-1) {
			return append(out, results...), true
		}
		// Rollup/cube: one prefixed copy of each result per grouping
		// set; the grand-total set has an empty prefix (plain names).
		// Mirrors the output-schema construction in the exec handler.
		for _, set := range computeGroupingSetsForSchema(pos, rollupMode) {
			prefix := groupingSetPrefixForSchema(set)
			for _, r := range results {
				out = append(out, prefix+r)
			}
		}
		return out, true
	})

	// window: appends one result field per result-producing flag (the
	// result name is always the flag's last arg). Spec flags
	// (-partition/-order/…) don't add fields.
	registerSchemaOp("window", func(_ any, in []string, args []string) ([]string, bool) {
		arity := map[string]int{
			"-partition": 1, "-order": 1, "-preceding": 1, "-following": 1,
			"-presorted": 0, "-desc": 0, "-generate": 0, "-g": 0,
			"-row-number": 1, "-rank": 1, "-dense-rank": 1, "-percent-rank": 1, "-count": 1,
			"-ntile": 2, "-first": 2, "-last": 2, "-sum": 2, "-avg": 2, "-min": 2, "-max": 2,
			"-lag": 3, "-lead": 3,
		}
		resultFlag := map[string]bool{
			"-row-number": true, "-rank": true, "-dense-rank": true, "-percent-rank": true,
			"-count": true, "-ntile": true, "-first": true, "-last": true,
			"-sum": true, "-avg": true, "-min": true, "-max": true, "-lag": true, "-lead": true,
		}
		out := slices.Clone(in)
		_, flags := walkStage(args, arity)
		for _, f := range flags {
			if resultFlag[f.name] && len(f.args) > 0 {
				out = append(out, f.args[len(f.args)-1])
			}
		}
		return out, true
	})

	// pivot: output columns are distinct data values of the pivot field
	// → undeterminable from argv alone.
	registerSchemaOp("pivot", func(_ any, _ []string, _ []string) ([]string, bool) {
		return nil, false
	})
}
