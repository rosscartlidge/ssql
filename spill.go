package ssql

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"io"
	"iter"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SpillConfig is the out-of-core setting for [SortRecordsSpill] (DFC137
// §1): sort runs of at most MemoryBytes (estimated) in memory, write
// each under Dir, and k-way merge the runs. Zero MemoryBytes means
// 1 GiB; an empty Dir means the system temp directory.
type SpillConfig struct {
	Dir         string
	MemoryBytes int64
}

// DefaultSpillMemory is the run size when SpillConfig.MemoryBytes is 0.
const DefaultSpillMemory = 1 << 30

// SortRecordsSpill is [SortRecords] with bounded memory: records are read
// into a run until it reaches the budget, the run is sorted (stably) and
// written to a file in cfg.Dir, and when the input ends the runs are
// merged with [MergeSorted], each file deleted as it drains. An input
// that fits in one run is sorted in memory and nothing is written. The
// cost is one write and one read of the data beyond the sort itself.
//
// Runs are gob-encoded, which keeps every value's type (a time stays a
// time; JSON Lines would return it as text). A record holding a
// sequence or nested record cannot be spilled and panics with a clear
// message. The runs live in a fresh directory under cfg.Dir that is
// removed when the merge ends, when the consumer stops early, and on
// interrupt (SIGINT/SIGTERM: the handler removes every live run
// directory and exits 130 / 143), so a killed sort leaves nothing
// behind. Exec and generated record programs make the same one call.
func SortRecordsSpill(orderBy []OrderField, cfg SpillConfig) Filter[Record, Record] {
	budget := cfg.MemoryBytes
	if budget <= 0 {
		budget = DefaultSpillMemory
	}
	cmp := func(a, b Record) int { return CompareRecordFields(a, b, orderBy) }
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var run []Record
			var size int64
			var files []string
			var dir string
			var cleanup func()
			flush := func() {
				if dir == "" {
					var err error
					if dir, cleanup, err = newSpillDir(cfg.Dir); err != nil {
						panic(fmt.Errorf("sort -spill: %w", err))
					}
				}
				slices.SortStableFunc(run, cmp)
				path := filepath.Join(dir, fmt.Sprintf("run-%04d.gob", len(files)))
				if err := writeSpillRun(path, run); err != nil {
					panic(fmt.Errorf("sort -spill: %w", err))
				}
				files = append(files, path)
				run, size = run[:0], 0
			}
			for r := range input {
				run = append(run, r)
				size += recordSizeEstimate(r)
				if size >= budget {
					flush()
				}
			}
			if len(files) == 0 {
				// everything fit: the in-memory sort, stable as the runs are
				slices.SortStableFunc(run, cmp)
				for _, r := range run {
					if !yield(r) {
						return
					}
				}
				return
			}
			if len(run) > 0 {
				flush()
			}
			run = nil
			sources := make([]iter.Seq[Record], len(files))
			for i, path := range files {
				sources[i] = readSpillRun(path)
			}
			defer cleanup() // the directory and whatever runs remain
			for r := range MergeSorted(orderBy, sources...) {
				if !yield(r) {
					return
				}
			}
		}
	}
}

// recordSizeEstimate is a cheap, honest-enough byte count for the run
// budget, calibrated against the resident size a run actually costs
// (a Record is a schema pointer and a []any of boxed values, the run
// slice grows by doubling, and the GC keeps headroom): on the 3M-row
// scale fixture a 16 MB budget runs in ~100 MB RSS and 256 MB in
// ~700 MB before this calibration; per-field overhead is set so the
// budget is within a factor of two of RSS growth.
func recordSizeEstimate(r Record) int64 {
	var n int64 = 96
	for k, v := range r.All() {
		n += int64(len(k)) + 56
		switch x := v.(type) {
		case string:
			n += int64(len(x))
		case JSONString:
			n += int64(len(x))
		default:
			n += 8
		}
	}
	return n
}

// spillRecord is a record on the wire: parallel key and value slices,
// which gob encodes with the values' concrete types.
type spillRecord struct {
	Keys   []string
	Values []any
}

func init() {
	// the value types a record field may hold, for gob's interface slots
	gob.Register(time.Time{})
	gob.Register(JSONString(""))
	gob.Register(int64(0))
	gob.Register(float64(0))
	gob.Register(false)
	gob.Register("")
}

func writeSpillRun(path string, run []Record) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	enc := gob.NewEncoder(w)
	for _, r := range run {
		sr := spillRecord{Keys: make([]string, 0, r.Len()), Values: make([]any, 0, r.Len())}
		for k, v := range r.All() {
			switch v.(type) {
			case nil, string, int64, float64, bool, time.Time, JSONString, int:
			default:
				f.Close()
				os.Remove(path)
				return fmt.Errorf("field %q holds a %T, which cannot be spilled to disk (sequences and nested records stay in memory: drop -spill or exclude the field)", k, v)
			}
			sr.Keys = append(sr.Keys, k)
			sr.Values = append(sr.Values, v)
		}
		if err := enc.Encode(sr); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readSpillRun streams a run file's records back, in the field order
// they were written; the file is closed when the sequence ends.
func readSpillRun(path string) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		f, err := os.Open(path)
		if err != nil {
			panic(fmt.Errorf("sort -spill: %w", err))
		}
		defer f.Close()
		dec := gob.NewDecoder(bufio.NewReaderSize(f, 1<<20))
		var schema *Schema
		for {
			var sr spillRecord
			if err := dec.Decode(&sr); err != nil {
				if err == io.EOF {
					return
				}
				panic(fmt.Errorf("sort -spill: reading %s: %w", path, err))
			}
			// runs from one sort mostly share a schema: reuse it while the
			// keys match (the #1 performance rule)
			if schema == nil || !slices.Equal(schema.fields, sr.Keys) {
				schema = NewSchema(sr.Keys)
			}
			if !yield(NewRecordFromSchema(schema, sr.Values)) {
				return
			}
		}
	}
}

// newSpillDir creates a fresh run directory under parent for one
// SortRecordsSpill and returns it with the function that removes it.
// The directory also goes on interrupt (SIGINT/SIGTERM): the handler
// removes every live run directory and exits with the conventional
// 130 / 143, since a killed sort must not leave gigabytes of runs
// behind.
func newSpillDir(parent string) (dir string, cleanup func(), err error) {
	if parent == "" {
		parent = os.TempDir()
	}
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		return "", nil, fmt.Errorf("spill directory %s: not an existing directory", parent)
	}
	dir, err = os.MkdirTemp(parent, "ssql-spill-")
	if err != nil {
		return "", nil, err
	}
	spillDirsMu.Lock()
	spillDirs = append(spillDirs, dir)
	if !spillSignalArmed {
		spillSignalArmed = true
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		go func() {
			sig := <-ch
			spillDirsMu.Lock()
			for _, d := range spillDirs {
				os.RemoveAll(d)
			}
			spillDirsMu.Unlock()
			code := 130
			if sig == syscall.SIGTERM {
				code = 143
			}
			os.Exit(code)
		}()
	}
	spillDirsMu.Unlock()
	cleanup = func() {
		os.RemoveAll(dir)
		spillDirsMu.Lock()
		spillDirs = slices.DeleteFunc(spillDirs, func(d string) bool { return d == dir })
		spillDirsMu.Unlock()
	}
	return dir, cleanup, nil
}

var (
	spillDirsMu      sync.Mutex
	spillDirs        []string
	spillSignalArmed bool
)

// ParseMemorySize reads a run budget: bytes, or a number with a K, M or
// G suffix (binary units): 512M, 4G, 1G.
func ParseMemorySize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K', 'k':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M', 'm':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G', 'g':
		mult, s = 1<<30, s[:len(s)-1]
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("size %q: want a positive number with an optional K, M or G suffix (512M, 4G)", s)
	}
	return int64(n * float64(mult)), nil
}
