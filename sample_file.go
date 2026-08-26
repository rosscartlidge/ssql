package ssql

// Byte-offset file sampling (DFC110 amendment, Ross's algorithm):
// draw N random byte offsets, seek, take the line containing each
// offset. Cost is N seeks + N line parses instead of a full-file
// parse — 0.2s vs 21s on a 1.2GB CSV — at the price of APPROXIMATE
// uniformity: a line's selection probability is proportional to its
// byte length (what databases call system/block sampling, vs the
// `sample` stage's exact reservoir). Deterministic under a seed for
// fixed file bytes: offsets are pure functions of (seed, draw index)
// via the same spec-stable RNG the sample stage uses.

import (
	"bytes"
	"fmt"
	"io"
	"iter"
	"os"
	"sort"
	"strings"
	"sync"
)

// sampleFileMaxLine bounds the backward/forward scans around an
// offset, defending against pathological files without newlines.
const sampleFileMaxLine = 4 << 20

// SampleCSVFile returns n lines drawn by byte-offset sampling from a
// header-bearing delimited file, parsed with the header's schema and
// emitted in FILE order. If the file has fewer than n data lines the
// whole file is returned (delegating to a full read). Selection is
// approximately uniform — probability proportional to line length.
func SampleCSVFile(filename string, n int, seed int64, config ...CSVConfig) (iter.Seq[Record], error) {
	cfg := DefaultCSVConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := st.Size()

	// Read the header line (schema) and note where data starts.
	head := make([]byte, 64*1024)
	m, _ := io.ReadFull(f, head)
	head = head[:m]
	nl := bytes.IndexByte(head, '\n')
	if nl < 0 {
		// Single-line (or header-only) file: nothing to sample.
		f.Close()
		return ReadCSV(filename, cfg)
	}
	headerLine := strings.TrimRight(string(head[:nl]), "\r")
	dataStart := int64(nl + 1)
	if dataStart >= size {
		f.Close()
		return ReadCSV(filename, cfg)
	}

	// Cheap smallness guard: if the data region can't hold clearly
	// more than n lines of the average size seen so far, fall back to
	// the exact reservoir over a full read — byte sampling only pays
	// on big files, and collision-redraw degenerates near n≈lines.
	if size-dataStart < int64(n)*8 {
		f.Close()
		seq, err := ReadCSV(filename, cfg)
		if err != nil {
			return nil, err
		}
		return SampleN[Record](n, seed)(seq), nil
	}

	ordered, ok := sampleLineStarts(f, size, dataStart, n, seed)
	f.Close()
	if !ok {
		// Couldn't find n distinct lines by drawing (file has few,
		// long, or strange lines) — exact fallback.
		seq, err := ReadCSV(filename, cfg)
		if err != nil {
			return nil, err
		}
		return SampleN[Record](n, seed)(seq), nil
	}

	// Parse each sampled line under the header schema by replaying
	// header+line through the standard CSV reader — one parser, one
	// set of type-inference rules (never a second CSV dialect).
	return func(yield func(Record) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		lineBuf := make([]byte, 0, 4096)
		for _, s := range ordered {
			line, ok := readLineAt(f, s, size)
			if !ok {
				continue
			}
			lineBuf = lineBuf[:0]
			lineBuf = append(lineBuf, headerLine...)
			lineBuf = append(lineBuf, '\n')
			lineBuf = append(lineBuf, line...)
			for r := range ReadCSVFromReader(bytes.NewReader(lineBuf), cfg) {
				if !yield(r) {
					return
				}
			}
		}
	}, nil
}

// lineStartAt returns the byte offset of the start of the line
// containing off (scanning backward for the preceding newline, never
// before dataStart).
func lineStartAt(f *os.File, off, dataStart int64, buf []byte) (int64, bool) {
	pos := off
	for scanned := 0; scanned < sampleFileMaxLine; {
		lo := pos - int64(len(buf))
		if lo < dataStart {
			lo = dataStart
		}
		if lo == pos {
			return dataStart, true
		}
		chunk := buf[:pos-lo]
		if _, err := f.ReadAt(chunk, lo); err != nil && err != io.EOF {
			return 0, false
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			return lo + int64(i) + 1, true
		}
		scanned += len(chunk)
		pos = lo
		if pos == dataStart {
			return dataStart, true
		}
	}
	return 0, false
}

// readLineAt reads the line beginning at start (without trailing
// newline), bounded by sampleFileMaxLine.
// Reads in small chunks, growing only when the line doesn't fit —
// the first version read the 4MB cap per line, turning a 1000-line
// sample into ~4GB of page-cache traffic (the entire 1.1s wall).
func readLineAt(f *os.File, start, size int64) ([]byte, bool) {
	chunk := int64(4096)
	for {
		remain := size - start
		if remain <= 0 {
			return nil, false
		}
		if remain > chunk {
			remain = chunk
		}
		buf := make([]byte, remain)
		m, err := f.ReadAt(buf, start)
		if err != nil && err != io.EOF {
			return nil, false
		}
		buf = buf[:m]
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			return bytes.TrimRight(buf[:i], "\r"), true
		}
		if int64(m) == size-start || chunk >= sampleFileMaxLine {
			return bytes.TrimRight(buf, "\r"), true // last line (or cap)
		}
		chunk *= 8
	}
}

// sampleLineStarts draws n distinct line-start offsets in
// [dataStart, size) — offsets via sampleHash, backward scan to each
// containing line's start, dedupe, redraw collisions (bounded) — and
// returns them in file order. ok=false when n distinct lines couldn't
// be found (caller falls back to an exact full read).
func sampleLineStarts(f *os.File, size, dataStart int64, n int, seed int64) ([]int64, bool) {
	span := size - dataStart
	if span <= 0 {
		return nil, false
	}
	// Draws run in PARALLEL batches: each draw is a pure function of
	// (seed, draw index) and lineStartAt uses ReadAt (offset-explicit,
	// safe on a shared *os.File), so concurrency cannot change WHICH
	// lines a seed selects — only how fast the seeks complete. On
	// local page cache this is noise; on high-latency storage (FUSE
	// cloud mounts, future https sources) it is the difference between
	// N×RTT and N×RTT/workers (DFC112). Dedup and ordering happen
	// after, exactly as before.
	const workers = 32
	starts := make(map[int64]bool, n)
	maxDraws := int64(n)*20 + 100
	batch := make([]int64, workers)
	for base := int64(0); int64(len(starts)) < int64(n) && base < maxDraws; base += workers {
		var wg sync.WaitGroup
		for w := 0; w < workers && base+int64(w) < maxDraws; w++ {
			w := w
			wg.Add(1)
			go func() {
				defer wg.Done()
				buf := make([]byte, 4096)
				off := dataStart + int64(sampleHash(seed, base+int64(w))%uint64(span))
				start, ok := lineStartAt(f, off, dataStart, buf)
				if ok {
					batch[w] = start
				} else {
					batch[w] = -1
				}
			}()
		}
		wg.Wait()
		// ADMISSION stays sequential by draw index and stops at n —
		// this reproduces the serial loop's selected set exactly
		// (draws beyond the batch's stopping point are wasted seeks,
		// never extra selections).
		for w := 0; w < workers && base+int64(w) < maxDraws; w++ {
			if int64(len(starts)) >= int64(n) {
				break
			}
			if batch[w] >= 0 {
				starts[batch[w]] = true
			}
		}
	}
	if int64(len(starts)) < int64(n) {
		return nil, false
	}
	ordered := make([]int64, 0, len(starts))
	for s := range starts {
		ordered = append(ordered, s)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered, true
}

// SampleTSVFile is [SampleCSVFile] for TSV files: the delimiter is
// auto-detected from the header line (first non-identifier byte,
// default tab) — the same single rule every TSV path uses.
func SampleTSVFile(filename string, n int, seed int64) (iter.Seq[Record], error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := st.Size()

	head := make([]byte, 64*1024)
	m, _ := io.ReadFull(f, head)
	head = head[:m]
	nl := bytes.IndexByte(head, '\n')
	if nl < 0 || int64(nl+1) >= size || size-int64(nl+1) < int64(n)*8 {
		f.Close()
		fh, ferr := os.Open(filename)
		if ferr != nil {
			return nil, ferr
		}
		return SampleN[Record](n, seed)(ReadTSVFromReader(fh)), nil
	}
	headerLine := strings.TrimRight(string(head[:nl]), "\r")
	dataStart := int64(nl + 1)

	ordered, ok := sampleLineStarts(f, size, dataStart, n, seed)
	f.Close()
	if !ok {
		fh, ferr := os.Open(filename)
		if ferr != nil {
			return nil, ferr
		}
		return SampleN[Record](n, seed)(ReadTSVFromReader(fh)), nil
	}

	// Replay header+line through the standard TSV reader — its own
	// delimiter auto-detection applies, one rule everywhere.
	return func(yield func(Record) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		lineBuf := make([]byte, 0, 4096)
		for _, st := range ordered {
			line, ok := readLineAt(f, st, size)
			if !ok {
				continue
			}
			lineBuf = lineBuf[:0]
			lineBuf = append(lineBuf, headerLine...)
			lineBuf = append(lineBuf, '\n')
			lineBuf = append(lineBuf, line...)
			for r := range ReadTSVFromReader(bytes.NewReader(lineBuf)) {
				if !yield(r) {
					return
				}
			}
		}
	}, nil
}

// SampleJSONLFile is byte-offset sampling for JSONL files. A leading
// {"_schema":…} header line is honoured (sampled lines parse with its
// types and never include it); headerless JSONL samples from byte 0
// and infers per line. JSON ARRAY files are refused loudly — they are
// not line-oriented, so offset sampling cannot apply.
func SampleJSONLFile(filename string, n int, seed int64) (iter.Seq[Record], error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := st.Size()

	head := make([]byte, 64*1024)
	m, _ := io.ReadFull(f, head)
	head = head[:m]
	trimmed := bytes.TrimLeft(head, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		f.Close()
		return nil, fmt.Errorf("%s is a JSON array — byte-offset sampling needs line-oriented JSONL (use the `sample` stage instead)", filename)
	}

	var schema *Schema
	dataStart := int64(0)
	if nl := bytes.IndexByte(head, '\n'); nl >= 0 {
		if sc, isHeader := parseSchemaHeaderLine(head[:nl+1]); isHeader {
			schema = sc
			dataStart = int64(nl + 1)
		}
	}

	fullFallback := func() (iter.Seq[Record], error) {
		f.Close()
		fh, ferr := os.Open(filename)
		if ferr != nil {
			return nil, ferr
		}
		return SampleN[Record](n, seed)(ReadJSONLFromReader(fh)), nil
	}
	if size-dataStart < int64(n)*8 {
		return fullFallback()
	}
	ordered, ok := sampleLineStarts(f, size, dataStart, n, seed)
	f.Close()
	if !ok {
		return fullFallback()
	}

	return func(yield func(Record) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		for _, st := range ordered {
			line, ok := readLineAt(f, st, size)
			if !ok || len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var rec Record
			if schema != nil {
				r, perr := ParseJSONLineWithSchema(line, schema)
				if perr != nil {
					continue
				}
				rec = r
			} else {
				mr, perr := ParseJSONLine(line)
				if perr != nil {
					continue
				}
				rec = mr.Freeze()
			}
			if !yield(rec) {
				return
			}
		}
	}, nil
}

// CountFileLines returns the number of newline-terminated lines in
// path — a pure bytes.Count scan at memory bandwidth (~0.15s/GB).
// The cheap-count backbone of `from -records` for line formats.
func CountFileLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	var n int64
	for {
		m, err := f.Read(buf)
		n += int64(bytes.Count(buf[:m], []byte{'\n'}))
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return 0, err
		}
	}
}
