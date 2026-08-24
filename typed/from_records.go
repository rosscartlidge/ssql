package typed

import (
	"iter"
	"sync"
)

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

// DistinctParallel dedupes a parallel Stream by key: each shard
// dedupes locally in parallel (the expensive part — hashing every
// row), then a serial merge dedupes across shards (cheap — at most
// nShards × distinct-count rows). Output order is shard order of
// first occurrence; like the serial [Distinct], callers needing a
// specific order sort downstream.
//
// Motivating case (found by Ross): `group-by relationship` with no
// aggregations — DISTINCT semantics — was SerialOnly, funnelling
// 14.6M parquet rows through one core (6.7s) while the -count form
// ran GroupByParallel (1.5s).
func DistinctParallel[T any, K comparable](in Stream[T], key func(T) K) iter.Seq[T] {
	nShards := len(in.shards)
	if nShards == 0 {
		return func(yield func(T) bool) {}
	}
	locals := make([][]T, nShards)
	var wg sync.WaitGroup
	for i, shard := range in.shards {
		i, shard := i, shard
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen := make(map[K]struct{})
			var out []T
			for v := range shard {
				k := key(v)
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				out = append(out, v)
			}
			locals[i] = out
		}()
	}
	return func(yield func(T) bool) {
		wg.Wait()
		seen := make(map[K]struct{})
		for _, local := range locals {
			for _, v := range local {
				k := key(v)
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				if !yield(v) {
					return
				}
			}
		}
	}
}
