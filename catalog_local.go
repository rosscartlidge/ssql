package ssql

// The local fast path (DFC149 §4.4). A catalog shard that is a Parquet
// file on this machine, with no pushdown stages to run, is read in this
// process by the Parquet reader — not by a child ssql writing JSONL
// that this process parses back. Seven local partition files took
// 3.0 s through the subprocess path and 0.7 s in-process; the generated
// program and the exec path both take it, per shard, so a catalog that
// mixes hosts still reads its local Parquet shards directly. Remote
// hosts, other formats and `-- stage` pushdown keep the subprocess path,
// where the shard's own ssql is the authority on its format.

// catalogLocalParquet reports whether a shard takes the fast path.
func catalogLocalParquet(e CatalogEntry, pushdown [][]string) bool {
	return len(pushdown) == 0 && IsLocalHost(e.Host) && CatalogEntryFormat(e) == "parquet"
}

// readLocalParquetShard reads one shard in-process, checks the hive
// clash on its first record, attaches the shard's metadata and yields.
// stopped reports that yield returned false.
func readLocalParquetShard(entry CatalogEntry, shardField string, yield func(Record) bool) (stopped bool, err error) {
	seq, err := ReadParquet(entry.Path)
	if err != nil {
		return false, err
	}
	enrich := newCatalogEnricher(entry, shardField)
	checked := false
	for r := range seq {
		if !checked {
			checked = true
			if err := hiveClash(r, entry); err != nil {
				return false, err
			}
		}
		if !yield(enrich.enrich(r)) {
			return true, nil
		}
	}
	return false, nil
}

// catalogEnricher attaches a shard's metadata (and -shard-field) to its
// records with one output schema per source schema: a Parquet file's
// records share one, so a three-million-row shard costs three million
// value copies, not three million schemas (the #1 performance rule;
// enrichCatalogRecord's ToMutable per record is what the subprocess
// path pays). A metadata column that is also a column of the file takes
// the metadata value in place, as enrichCatalogRecord does.
type catalogEnricher struct {
	keys  []string // the columns added, in catalogMetaColumns order
	extra []any    // their values, parallel to keys
	plans map[*Schema]enrichPlan
}

type enrichPlan struct {
	out     *Schema
	inPlace []int // for each key: the source index it overwrites, or -1 to append
}

func newCatalogEnricher(entry CatalogEntry, shardField string) *catalogEnricher {
	e := &catalogEnricher{plans: map[*Schema]enrichPlan{}}
	names, values := CatalogRecordMetadata(entry, shardField)
	for i, k := range names {
		e.keys = append(e.keys, k)
		e.extra = append(e.extra, values[i])
	}
	return e
}

func (e *catalogEnricher) enrich(r Record) Record {
	if len(e.keys) == 0 || r.schema == nil {
		return r
	}
	plan, ok := e.plans[r.schema]
	if !ok {
		fields := r.schema.Fields()
		plan.inPlace = make([]int, len(e.keys))
		for i, k := range e.keys {
			plan.inPlace[i] = r.schema.Index(k)
			if plan.inPlace[i] < 0 {
				fields = append(fields, k)
			}
		}
		plan.out = NewSchema(fields)
		e.plans[r.schema] = plan
	}
	values := make([]any, len(r.values), len(plan.out.fields))
	copy(values, r.values)
	for i, idx := range plan.inPlace {
		if idx >= 0 {
			values[idx] = e.extra[i]
		} else {
			values = append(values, e.extra[i])
		}
	}
	return NewRecordFromSchema(plan.out, values)
}
