package typed

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"runtime"
	"sync"
	"unsafe"
)

// ReadJSON streams rows of T from a JSON ARRAY file (`[ {...}, {...} ]`,
// the shape an API export or `to json` writes) — the array twin of
// [ReadJSONL]. Each element is decoded with the same positional plan
// the JSONL readers use (a type the plan cannot cover falls back to
// encoding/json), so a nested value lands in a string field as its raw
// text, as the CLI's `json` wire type does.
//
// Like [ReadJSONL] it fails fast: an unreadable file, a document that is
// not an array, an element that is not an object, or a value that does
// not fit its field panics with a *[ReadError] whose Row is the 1-based
// element index. Elements are scanned with encoding/json's Decoder, so
// the file is streamed, not loaded; whitespace and layout do not matter.
func ReadJSON[T any](filename string) iter.Seq[T] {
	return func(yield func(T) bool) {
		f, err := os.Open(filename)
		if err != nil {
			failRead("typed.ReadJSON", filename, err)
		}
		defer f.Close()
		readJSONArrayLoud[T]("typed.ReadJSON", filename, f, yield)
	}
}

// ReadJSONFromReader is the [io.Reader] variant of [ReadJSON].
func ReadJSONFromReader[T any](rd io.Reader) iter.Seq[T] {
	return func(yield func(T) bool) {
		readJSONArrayLoud[T]("typed.ReadJSONFromReader", "", rd, yield)
	}
}

// readJSONArrayLoud is the shared body of the lossy array readers.
func readJSONArrayLoud[T any](op, source string, rd io.Reader, yield func(T) bool) {
	pl, perr := buildJSONLPlan[T]()
	var row T
	for raw, idx := range jsonArrayElements(op, source, rd) {
		row = *new(T)
		var err error
		if perr == nil {
			err = pl.decode(raw, unsafe.Pointer(&row))
		} else {
			err = json.Unmarshal(raw, &row)
		}
		if err != nil {
			failRow(op, source, idx, err)
		}
		if !yield(row) {
			return
		}
	}
}

// jsonArrayElements yields each element of a JSON array document as its
// raw bytes with its 1-based index. A document that does not open with
// `[` or does not parse is a *ReadError (Row 0 for the document, the
// element's index for a bad element).
func jsonArrayElements(op, source string, rd io.Reader) iter.Seq2[[]byte, int64] {
	return func(yield func([]byte, int64) bool) {
		dec := json.NewDecoder(bufio.NewReaderSize(rd, 1<<20))
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return // an empty file: no rows, as the record reader
			}
			failRead(op, source, fmt.Errorf("reading JSON array: %w", err))
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			failRead(op, source, errors.New("not a JSON array (expected '[' — a JSON Lines file reads with ReadJSONL)"))
		}
		var idx int64
		for dec.More() {
			idx++
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				failRow(op, source, idx, err)
			}
			if !yield(raw, idx) {
				return
			}
		}
		if _, err := dec.Token(); err != nil { // the closing ']'
			failRead(op, source, fmt.Errorf("reading JSON array: %w", err))
		}
	}
}

// rawBatch is a run of array elements handed from the scanner to a
// shard: the elements' raw bytes and the 1-based index of the first.
type rawBatch struct {
	first int64
	raws  [][]byte
}

// ReadJSONParallel reads a JSON array file as a Stream[T] with n shards
// (n <= 0 → GOMAXPROCS) — the array twin of [ReadJSONLParallel]. An
// array has no newline index to split on, so one goroutine scans the
// document into element boundaries (encoding/json's Decoder, a fast
// token walk that copies each element's bytes) and hands the elements
// to the shards in batches of parallelBatchSize over a channel; the
// shards do the decoding — the expensive part — in parallel, with the
// same positional plan as [ReadJSON]. The shape is [ParallelBatched]
// applied before the decode rather than after it.
//
// Errors are the serial reader's (*[ReadError], Row = element index),
// raised in whichever shard meets them, or re-raised from the scanner
// by every shard when the document itself is bad. A consumer that stops
// early releases the scanner.
func ReadJSONParallel[T any](filename string, n int) Stream[T] {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	const op = "typed.ReadJSONParallel"
	work := make(chan rawBatch, n*2)
	stop := make(chan struct{})
	var stopOnce sync.Once
	var scanner shardGroup
	scanner.Go(func() {
		defer close(work)
		f, err := os.Open(filename)
		if err != nil {
			failRead(op, filename, err)
		}
		defer f.Close()
		batch := rawBatch{first: 1, raws: make([][]byte, 0, parallelBatchSize)}
		flush := func() bool {
			if len(batch.raws) == 0 {
				return true
			}
			select {
			case work <- batch:
				batch = rawBatch{first: batch.first + int64(len(batch.raws)), raws: make([][]byte, 0, parallelBatchSize)}
				return true
			case <-stop:
				return false
			}
		}
		for raw := range jsonArrayElements(op, filename, f) {
			batch.raws = append(batch.raws, raw)
			if len(batch.raws) == parallelBatchSize && !flush() {
				return
			}
		}
		flush()
	})
	pl, perr := buildJSONLPlan[T]()
	shards := make([]iter.Seq[T], n)
	for i := 0; i < n; i++ {
		shards[i] = func(yield func(T) bool) {
			for batch := range work {
				for k, raw := range batch.raws {
					var row T
					var err error
					if perr == nil {
						err = pl.decode(raw, unsafe.Pointer(&row))
					} else {
						err = json.Unmarshal(raw, &row)
					}
					if err != nil {
						failRow(op, filename, batch.first+int64(k), err)
					}
					if !yield(row) {
						stopOnce.Do(func() { close(stop) })
						return
					}
				}
			}
			scanner.rethrow()
		}
	}
	return Stream[T]{shards: shards, n: n}
}
