package commands

import (
	"fmt"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// Out-of-core sort (DFC137 §1): `sort -spill DIR [-memory SIZE]` sorts
// runs of at most SIZE in memory, writes them under DIR and merges them;
// `group-by -spill DIR` is that sort on the group fields followed by the
// presorted (streaming) aggregation. Opt-in: without -spill nothing
// changes. Exec and generated record programs call the same
// ssql.NewSpillDir / ssql.SortRecordsSpill; typed codegen falls back to
// record for the stage (the planner inserts the adapter) with a plan
// note; generate sql drops the flags, since spilling is the engine's.

// spillSpec is the -spill / -memory pair as parsed.
type spillSpec struct {
	dir    string
	memory string
}

func (s spillSpec) active() bool { return s.dir != "" }

// parseSpillSpec reads the flags; -memory without -spill is refused.
func parseSpillSpec(ctx *cf.Context) (spillSpec, error) {
	var s spillSpec
	s.dir, _ = ctx.GlobalFlags["-spill"].(string)
	s.memory, _ = ctx.GlobalFlags["-memory"].(string)
	if s.memory != "" && s.dir == "" {
		return s, fmt.Errorf("-memory needs -spill DIR (it is the size of each run written there)")
	}
	if s.memory != "" {
		if _, err := ssql.ParseMemorySize(s.memory); err != nil {
			return s, fmt.Errorf("-memory: %w", err)
		}
	}
	return s, nil
}

// memoryBytes is the parsed budget, 0 for the library default.
func (s spillSpec) memoryBytes() int64 {
	if s.memory == "" {
		return 0
	}
	n, _ := ssql.ParseMemorySize(s.memory)
	return n
}

// spillSort is the out-of-core sort for exec: -spill DIR must exist;
// the library makes and removes its own run directory under it.
func spillSort(orderBy []ssql.OrderField, s spillSpec) (ssql.Filter[ssql.Record, ssql.Record], error) {
	if st, err := os.Stat(s.dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("-spill %s: not an existing directory", s.dir)
	}
	return ssql.SortRecordsSpill(orderBy, ssql.SpillConfig{Dir: s.dir, MemoryBytes: s.memoryBytes()}), nil
}

// spillSortFragment is the record-mode stmt fragment for the sort: the
// generated program takes the directory as its own -spill flag and
// makes the same one call exec does. command "" makes it a continuation
// of the stage that follows (group-by's), so the stage is translated
// once by the other generators.
func spillSortFragment(outputVar, inputVar string, orderBy []ssql.OrderField, s spillSpec, command string) *lib.CodeFragment {
	var fields []string
	for _, of := range orderBy {
		fields = append(fields, fmt.Sprintf(`{Field: %q, Desc: %v}`, of.Field, of.Desc))
	}
	code := fmt.Sprintf(`%s := ssql.SortRecordsSpill([]ssql.OrderField{%s}, ssql.SpillConfig{Dir: *flagSpill, MemoryBytes: %d})(%s)`,
		outputVar, strings.Join(fields, ", "), s.memoryBytes(), inputVar)
	frag := lib.NewStmtFragment(outputVar, inputVar, code, nil, command)
	frag.Params = []lib.CodeParam{{Name: "spill", Default: s.dir, Help: "directory for sort runs (out-of-core sort)", VarName: "flagSpill"}}
	return frag
}
