package main

import (
	"bytes"

	"github.com/rosscartlidge/ssql/v4"
)

// pqRow is one fixture row: id, name, amount, and region when the file
// carries it (a hive-partitioned file has its partition column in the
// directory name instead).
type pqRow struct {
	id     int64
	name   string
	region string
	amount float64
}

// corpusParquet renders rows as Parquet bytes, for the corpus data map.
// Shuffled ids and distinct amounts across the files, so a file read
// twice, dropped, or read out of its own schema changes every aggregate.
func corpusParquet(withRegion bool, rows ...pqRow) string {
	seq := func(yield func(ssql.Record) bool) {
		for _, r := range rows {
			m := ssql.MakeMutableRecord().Int("id", r.id).String("name", r.name)
			if withRegion {
				m = m.String("region", r.region)
			}
			if !yield(m.Float("amount", r.amount).Freeze()) {
				return
			}
		}
	}
	var buf bytes.Buffer
	if err := ssql.WriteParquetToWriter(seq, &buf); err != nil {
		panic(err)
	}
	return buf.String()
}
