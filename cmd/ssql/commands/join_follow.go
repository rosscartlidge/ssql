package commands

import (
	"fmt"
	"iter"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Graph traversal (DFC161), a mode on `join`: the seeds are the stream,
// the edges the right-side file, and the join runs to a fixpoint — each
// round joins the routes the last round found against the edges again.
//
//	printf 'city\nSYD\n' | ssql from csv - | ssql join flights.csv -on city src -follow dst -depth hops
//	… | ssql join flights.csv -on city src -follow dst -lowest-sum mins total
//	… | ssql join flights.csv -on city src -follow dst -all-paths -path route
//
// Exec and record codegen call ssql.Follow; typed emits
// typed.Follow[Parallel]; generate sql emits a recursive CTE (the keyed
// modes on DuckDB only).

// followSpec is the -follow family of flags, parsed once by the handler.
type followSpec struct {
	dst         string
	mode        ssql.FollowMode
	lowest      ssql.FollowSum
	sums        []ssql.FollowSum
	depth, path string
	maxDepth    int
	both        bool
}

func (f followSpec) active() bool { return f.dst != "" }

// followPairs reads an accumulated two-argument flag (-sum FIELD NAME).
func followPairs(raw any) []ssql.FollowSum {
	var out []ssql.FollowSum
	add := func(v any) {
		if m, ok := v.(map[string]any); ok {
			f, _ := m["field"].(string)
			n, _ := m["result"].(string)
			out = append(out, ssql.FollowSum{Field: f, Name: n})
		}
	}
	if arr, ok := raw.([]any); ok {
		for _, v := range arr {
			add(v)
		}
	} else {
		add(raw)
	}
	return out
}

// parseFollowSpec reads the -follow flags from the parsed context.
func parseFollowSpec(ctx *cf.Context) (followSpec, error) {
	var f followSpec
	g := ctx.GlobalFlags
	f.dst, _ = g["-follow"].(string)
	f.depth, _ = g["-depth"].(string)
	f.path, _ = g["-path"].(string)
	f.maxDepth, _ = g["-max-depth"].(int)
	f.both, _ = g["-both"].(bool)
	f.sums = followPairs(g["-sum"])
	fewest, _ := g["-fewest-hops"].(bool)
	all, _ := g["-all-paths"].(bool)
	lowest := followPairs(g["-lowest-sum"])
	modes := 0
	for _, on := range []bool{fewest, all, len(lowest) > 0} {
		if on {
			modes++
		}
	}
	if !f.active() {
		if modes > 0 || f.depth != "" || f.path != "" || f.maxDepth != 0 || f.both || len(f.sums) > 0 {
			return f, fmt.Errorf("join: -fewest-hops, -lowest-sum, -all-paths, -depth, -path, -sum, -max-depth and -both need -follow FIELD")
		}
		return f, nil
	}
	if modes > 1 {
		return f, fmt.Errorf("join -follow: -fewest-hops, -lowest-sum and -all-paths are alternatives — give one (-fewest-hops is the default)")
	}
	switch {
	case all:
		f.mode = ssql.FollowAllPaths
	case len(lowest) > 0:
		f.mode, f.lowest = ssql.FollowLowestSum, lowest[0]
	}
	for _, s := range append(append([]ssql.FollowSum{}, f.sums...), lowest...) {
		if s.Field == "" || s.Name == "" {
			return f, fmt.Errorf("join -follow: -sum and -lowest-sum need FIELD NAME")
		}
	}
	if f.maxDepth < 0 {
		return f, fmt.Errorf("join -follow: -max-depth %d must be positive", f.maxDepth)
	}
	if f.maxDepth > 0 && f.mode == ssql.FollowLowestSum {
		return f, fmt.Errorf("join -follow: -max-depth is not defined with -lowest-sum (the cheapest route within N hops is not what the search finds; DFC161 §11)")
	}
	return f, nil
}

// followAccNames are the result columns in output order (DFC161 §10).
func (f followSpec) accNames() []string {
	var names []string
	if f.depth != "" {
		names = append(names, f.depth)
	}
	for _, s := range f.sums {
		names = append(names, s.Name)
	}
	if f.mode == ssql.FollowLowestSum {
		names = append(names, f.lowest.Name)
	}
	if f.path != "" {
		names = append(names, f.path)
	}
	return names
}

// weights are the edge fields the accumulators read.
func (f followSpec) weights() []ssql.FollowSum {
	w := append([]ssql.FollowSum{}, f.sums...)
	if f.mode == ssql.FollowLowestSum {
		w = append(w, f.lowest)
	}
	return w
}

// followCheck validates the traversal against what the field lists
// allow: one node/source pair, no merge options, the fields present, the
// weights numeric, and no result column colliding with a seed column or
// another result. nil field lists are unknown and skipped.
func followCheck(leftFields, rightFields []string, rightType func(string) string, clauses []ssql.LookupClause, f followSpec, joinType string, opts joinOptions, asof asofSpec) (ssql.FollowConfig, error) {
	var cfg ssql.FollowConfig
	if len(clauses) != 1 || len(clauses[0].FieldRenames) > 0 {
		return cfg, fmt.Errorf("join -follow needs exactly one -on NODE SRC (or -using FIELD) and no -as: the seed's node field and the edge's source field")
	}
	if joinType != "" && joinType != "inner" {
		return cfg, fmt.Errorf("join -follow: -type %s has no traversal meaning (every seed is its own depth-0 row)", joinType)
	}
	if opts.any() {
		return cfg, fmt.Errorf("join -follow: -suffix, -exclude-left and -exclude-right are not supported with -follow (the output is the seed's columns plus the results)")
	}
	if asof.active() {
		return cfg, fmt.Errorf("join: -follow and -asof are different joins — give one")
	}
	c := clauses[0]
	if leftFields != nil && !fieldListHasOrPath(leftFields, c.LeftField) {
		return cfg, fmt.Errorf("join -follow: seed field %q not found (available: %s)", c.LeftField, strings.Join(leftFields, ", "))
	}
	if rightFields != nil {
		for _, rf := range append([]string{c.RightField, f.dst}, followFields(f.weights())...) {
			if !fieldListHasOrPath(rightFields, rf) {
				return cfg, fmt.Errorf("join -follow: edge field %q not found (available: %s)", rf, strings.Join(rightFields, ", "))
			}
		}
		for _, w := range f.weights() {
			switch base, _ := lib.ParseWireType(rightType(w.Field)); base {
			case "", lib.TypeInt, lib.TypeInt128, lib.TypeFloat, lib.TypeDecimal:
			default:
				return cfg, fmt.Errorf("join -follow: edge field %q is %s; a sum needs a number", w.Field, base)
			}
		}
	}
	seen := map[string]bool{}
	for _, n := range f.accNames() {
		if seen[n] || (leftFields != nil && fieldListHasOrPath(leftFields, n)) || n == c.LeftField {
			return cfg, fmt.Errorf("join -follow: result column %q is already a column (seed: %s)", n, strings.Join(leftFields, ", "))
		}
		seen[n] = true
	}
	return ssql.FollowConfig{
		NodeField: c.LeftField, SrcField: c.RightField, DstField: f.dst,
		Mode: f.mode, Lowest: f.lowest, Sums: f.sums,
		DepthField: f.depth, PathField: f.path, MaxDepth: f.maxDepth, Both: f.both,
	}, nil
}

func followFields(sums []ssql.FollowSum) []string {
	var fs []string
	for _, s := range sums {
		fs = append(fs, s.Field)
	}
	return fs
}

// followSumType is the wire type of a total over an edge field: an
// integer sum is an int128 (DFC155 C), a decimal keeps its scale.
func followSumType(edgeType string) string {
	switch base, _ := lib.ParseWireType(edgeType); base {
	case lib.TypeInt, lib.TypeInt128:
		return lib.TypeInt128
	case lib.TypeDecimal:
		return edgeType
	}
	return lib.TypeFloat
}

// execFollowJoin runs the traversal in the interpreter.
func execFollowJoin(ctx *cf.Context, leftRecords iter.Seq[ssql.Record], leftSchema *lib.Schema, rightSeq iter.Seq[ssql.Record], rightSchema *lib.Schema, clauses []ssql.LookupClause, f followSpec, joinType string, opts joinOptions, asof asofSpec) error {
	var leftFields []string
	if leftSchema != nil {
		leftFields = leftSchema.Fields
	}
	cfg, err := followCheck(leftFields, rightSchema.Fields, rightSchema.TypeOf, clauses, f, joinType, opts, asof)
	if err != nil {
		return err
	}
	routes := ssql.Follow(rightSeq, cfg)(leftRecords)

	var outputSchema *lib.Schema
	if leftSchema != nil {
		outputSchema = lib.NewSchema()
		for _, fld := range leftSchema.Fields {
			outputSchema.AddField(fld, leftSchema.TypeOf(fld))
		}
		if !outputSchema.HasField(cfg.NodeField) {
			outputSchema.AddField(cfg.NodeField, rightSchema.TypeOf(cfg.DstField))
		}
		if f.depth != "" {
			outputSchema.AddField(f.depth, lib.TypeInt)
		}
		for _, w := range f.weights() {
			outputSchema.AddField(w.Name, followSumType(rightSchema.TypeOf(w.Field)))
		}
		if f.path != "" {
			outputSchema.AddField(f.path, lib.TypeJSON)
		}
	}
	if err := lib.WriteJSONLWithSchema(ctx.Stdout(), outputSchema, routes); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

// followConfigGo renders the config as Go source for record codegen.
func followConfigGo(cfg ssql.FollowConfig) string {
	parts := []string{fmt.Sprintf("NodeField: %q, SrcField: %q, DstField: %q", cfg.NodeField, cfg.SrcField, cfg.DstField)}
	switch cfg.Mode {
	case ssql.FollowLowestSum:
		parts = append(parts, "Mode: ssql.FollowLowestSum", fmt.Sprintf("Lowest: ssql.FollowSum{Field: %q, Name: %q}", cfg.Lowest.Field, cfg.Lowest.Name))
	case ssql.FollowAllPaths:
		parts = append(parts, "Mode: ssql.FollowAllPaths")
	}
	if len(cfg.Sums) > 0 {
		var sums []string
		for _, s := range cfg.Sums {
			sums = append(sums, fmt.Sprintf("{Field: %q, Name: %q}", s.Field, s.Name))
		}
		parts = append(parts, "Sums: []ssql.FollowSum{"+strings.Join(sums, ", ")+"}")
	}
	if cfg.DepthField != "" {
		parts = append(parts, fmt.Sprintf("DepthField: %q", cfg.DepthField))
	}
	if cfg.PathField != "" {
		parts = append(parts, fmt.Sprintf("PathField: %q", cfg.PathField))
	}
	if cfg.MaxDepth > 0 {
		parts = append(parts, fmt.Sprintf("MaxDepth: %d", cfg.MaxDepth))
	}
	if cfg.Both {
		parts = append(parts, "Both: true")
	}
	return "ssql.FollowConfig{" + strings.Join(parts, ", ") + "}"
}

// generateFollowStmt writes the record-mode stmt fragment.
func generateFollowStmt(inputVar, funcName string, cfg ssql.FollowConfig) error {
	code := fmt.Sprintf("joined := ssql.Follow(%s(), %s)(%s)", funcName, followConfigGo(cfg), inputVar)
	return lib.WriteCodeFragment(lib.NewStmtFragment("joined", inputVar, code, nil, getCommandString()))
}
