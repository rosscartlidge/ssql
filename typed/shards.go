package typed

import (
	"iter"
	"reflect"
)

// The surface a format package outside typed builds its readers and
// writers on (DFC168: package parquet). Kept to what such a package
// cannot do without reaching into a Stream.

// FromShards makes a Stream of shards that are contiguous runs of the
// source, in source order (the order a parallel reader's sinks dump in;
// see Stream). No shards is an empty stream.
func FromShards[T any](shards []iter.Seq[T]) Stream[T] {
	return Stream[T]{shards: shards, n: len(shards)}
}

// ShardSeqs returns the stream's shards, in shard order.
func (s Stream[T]) ShardSeqs() []iter.Seq[T] { return s.shards }

// EachShard runs fn on every shard concurrently and waits for all of
// them. A panic in any (a reader's fail-fast *ReadError) is re-raised in
// the caller, as every Stream consumer that fans out does, so a
// generated program's main reports it instead of crashing.
func (s Stream[T]) EachShard(fn func(i int, shard iter.Seq[T])) {
	var g shardGroup
	for i, shard := range s.shards {
		g.Go(func() { fn(i, shard) })
	}
	g.Wait()
}

// ColumnName is the column a struct field reads and writes: its `ssql`
// tag's name, else its `csv` tag's, else the field name; skip is true
// for a field tagged "-". Every typed reader and writer names columns
// by this rule.
func ColumnName(f reflect.StructField) (name string, skip bool) { return columnName(f) }

// DecimalFieldScale is the scale an ssql.Decimal field's tag declares
// (`ssql:"price,decimal:2"`); a Decimal field without one is an error.
func DecimalFieldScale(f reflect.StructField) (int, error) { return decimalFieldScale(f) }
