package typed

import "iter"

// FromRecords adapts a Record-mode sequence into a typed sequence by
// applying conv to each row lazily — no materialization, no per-row
// channel. This is the serial Record→typed re-entry boundary (DFC109):
// generated code passes an explicit per-field converter closure, so
// the conversion is readable and reflection-free.
//
// R is ssql.Record in practice; the adapter is generic so this package
// never imports the root ssql package (the root imports typed, and a
// direct dependency would be a cycle).
func FromRecords[R, T any](src iter.Seq[R], conv func(R) T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for r := range src {
			if !yield(conv(r)) {
				return
			}
		}
	}
}

// FromRecordsParallel adapts a Record-mode sequence into a parallel
// Stream[T]: it converts and materializes the rows into a slice in one
// pass, then shards via [ParallelFromSlice] (contiguous chunks, no
// channel transit — the house pattern for entering parallel
// execution).
//
// The materialization is deliberate: this boundary exists for sources
// like `from ssh`, whose output is post-pushdown and post-reduction —
// small enough that holding []T is the normal case, and the parallel
// downstream (GroupByParallel, Stream.Where, per-shard sinks) is where
// the speed lives. For a serial downstream use [FromRecords], which is
// lazy and allocation-free.
func FromRecordsParallel[R, T any](src iter.Seq[R], conv func(R) T, n int) Stream[T] {
	var data []T
	for r := range src {
		data = append(data, conv(r))
	}
	return ParallelFromSlice(data, n)
}
