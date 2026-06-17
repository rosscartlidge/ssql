//go:build !slim

package commands

// Serve-only pieces of schema-aware completion: the source op that
// reads the in-memory dataset schema from the session serveState, and
// the SchemaWalkFunc the serve session hands to autocli/shell. Both
// depend on serveState, so they live in this !slim file; the registry
// and the runtime-agnostic transform ops are in schema_ops.go.

// serveSchemaWalk is the shell.SchemaWalkFunc the serve session installs.
// It walks the upstream stages left-to-right through their schemaOps,
// returning the field names entering the stage under the cursor. A
// single ok=false anywhere poisons the result (no fields seeded).
func serveSchemaWalk(state any, upstream [][]string) (fields []string, ok bool) {
	for _, stage := range upstream {
		if len(stage) == 0 {
			continue
		}
		out, ok := lookupSchemaOp(stage[0])(state, fields, stage[1:])
		if !ok {
			return nil, false
		}
		fields = out
	}
	return fields, true
}

func init() {
	// from-loaded is a source: it ignores its input and emits the
	// in-memory dataset's schema, read from the session serveState.
	registerSchemaOp("from-loaded", func(state any, _ []string, _ []string) ([]string, bool) {
		srv, ok := state.(*serveState)
		if !ok || srv == nil {
			return nil, false
		}
		return srv.schema, true
	})
}
