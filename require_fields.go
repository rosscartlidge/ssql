package ssql

import (
	"fmt"
	"iter"
	"sort"
	"strings"
)

// RequireFieldsSample is how many records RequireFields looks at before
// deciding a field is missing: a JSON source's first record may lack a
// key a later one carries, as exec's schema sampling allows (DFC128 D3).
// A CSV, TSV, Parquet or Arrow source gives every record the header's
// schema, so the first record settles it.
const RequireFieldsSample = 100

// RequireFields is the check exec makes against the stream's schema, for
// generated record-mode programs, which have no schema: every field must
// appear in one of the first RequireFieldsSample records (a dotted path
// by its own name, then by its head segment), or the pipeline stops with
// the message exec prints. The typed lane checks at generate time; the
// record lane checked nothing until 2026-10-14 — `where -if nosuch eq 1`
// filtered everything out and `group-by nosuch` grouped on an empty
// column, both exit 0. The error is a panic, the library's error channel
// (DFC142), which ssql.Run turns into the program's exit status. An
// empty stream passes: there is no schema to disagree with. Codegen
// applies it only where the stream's field set is the source's (after
// schema-preserving stages); after a join or a conditional update a
// field may legitimately be absent from early records.
func RequireFields(stage string, fields ...string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		if len(fields) == 0 {
			return input
		}
		return func(yield func(Record) bool) {
			held := make([]Record, 0, 8)
			missing := append([]string(nil), fields...)
			var available []string
			decided := false
			flush := func() bool {
				for _, r := range held {
					if !yield(r) {
						return false
					}
				}
				held = held[:0]
				return true
			}
			for r := range input {
				if decided {
					if !yield(r) {
						return
					}
					continue
				}
				missing = MissingFields(r, missing)
				available = mergeFieldNames(available, r.Schema().Fields())
				held = append(held, r)
				if len(missing) == 0 || len(held) >= RequireFieldsSample {
					if len(missing) > 0 {
						panic(UnknownFieldsError(stage, missing, available))
					}
					decided = true
					if !flush() {
						return
					}
				}
			}
			if !decided && len(missing) > 0 && len(held) > 0 {
				panic(UnknownFieldsError(stage, missing, available))
			}
			flush()
		}
	}
}

// MissingFields returns, in order and without repeats, the fields the
// record's schema lacks. A dotted name is a column when the schema has it
// as written (a flattened `items.sku`), else a path checked by its head.
func MissingFields(r Record, fields []string) []string {
	var missing []string
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f] {
			continue
		}
		seen[f] = true
		if r.Has(f) {
			continue
		}
		if i := strings.IndexByte(f, '.'); i > 0 && r.Has(f[:i]) {
			continue
		}
		missing = append(missing, f)
	}
	return missing
}

func mergeFieldNames(into, names []string) []string {
	for _, n := range names {
		found := false
		for _, have := range into {
			if have == n {
				found = true
				break
			}
		}
		if !found {
			into = append(into, n)
		}
	}
	return into
}

// UnknownFieldsError is the one wording of "a stage names a field the
// records do not have", shared by exec and the generated programs.
func UnknownFieldsError(stage string, missing, available []string) error {
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)
	return fmt.Errorf("%s references unknown field(s): %s (available: %s)",
		stage, strings.Join(missing, ", "), strings.Join(sorted, ", "))
}
