package commands

import (
	"bufio"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// readAuxInput opens a side-input FILE (join/merge/union) with the same
// extension-inferred convenience `from FILE` provides: .csv and .tsv
// (delimiter auto-detected) read directly through the package readers,
// .json reads as JSON lines, and .jsonl / process substitutions carry
// the wire format (schema header REQUIRED for bare .jsonl — a headerless
// file silently loses field information, the bug the old always-JSONL
// rule guarded against). Parquet/arrow still route via process
// substitution. Returns records plus a schema (field order from the
// file header where there is one; types from the first record).
func readAuxInput(path string) (iter.Seq[ssql.Record], *lib.Schema, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if fi, err := os.Stat(path); err == nil && !fi.Mode().IsRegular() {
		ext = "" // process substitution / FIFO: wire format
	}

	switch ext {
	case ".csv", ".tsv":
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("opening %s: %w", path, err)
		}
		var headers []string
		var records iter.Seq[ssql.Record]
		if ext == ".csv" {
			headers, _ = readCSVHeadersFromReader(f)
			f.Seek(0, 0)
			records = ssql.ReadCSVFromReader(f)
		} else {
			line, _ := bufio.NewReader(f).ReadString('\n')
			headers = splitDelimHeader(strings.TrimRight(line, "\r\n"))
			f.Seek(0, 0)
			records = ssql.ReadTSVFromReader(f)
		}
		records = closeAfter(records, f)
		return withPeekedSchema(records, headers)

	case ".json":
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("opening %s: %w", path, err)
		}
		records := closeAfter(ssql.ReadJSONFromReader(f), f)
		return withPeekedSchema(records, nil)

	case "", ".jsonl":
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("opening %s: %w", path, err)
		}
		sr := lib.ReadJSONLWithSchema(f)
		if ext == ".jsonl" {
			// Bare .jsonl needs the schema header (e.g. `ssql tee`
			// output); a headerless file must fail LOUDLY.
			// Peek-free check isn't possible before iterating, so the
			// caller checks sr.Schema == nil — preserve that contract.
		}
		return closeAfter(sr.Records, f), sr.Schema, nil

	default:
		return nil, nil, fmt.Errorf("%s: %s files aren't read directly yet — use process substitution: <(ssql from %s)",
			path, ext, path)
	}
}

// splitDelimHeader parses a delimited header line with the shared
// delimiter detection (first non-identifier byte, default tab).
func splitDelimHeader(line string) []string {
	d := string(lib.DetectDelimInHeader(line))
	return strings.Split(line, d)
}

// closeAfter closes c when the sequence finishes (or is abandoned).
func closeAfter(records iter.Seq[ssql.Record], c interface{ Close() error }) iter.Seq[ssql.Record] {
	return func(yield func(ssql.Record) bool) {
		defer c.Close()
		for r := range records {
			if !yield(r) {
				return
			}
		}
	}
}

// withPeekedSchema builds a lib.Schema from the header field order (or,
// when nil, alphabetically from the first record) with types inferred
// from the first record, re-yielding it so the stream is unchanged.
func withPeekedSchema(records iter.Seq[ssql.Record], headers []string) (iter.Seq[ssql.Record], *lib.Schema, error) {
	next, stop := iter.Pull(records)
	first, ok := next()
	if !ok {
		stop()
		schema := lib.NewSchema()
		for _, h := range headers {
			schema.AddField(h, "string")
		}
		return func(yield func(ssql.Record) bool) {}, schema, nil
	}

	fields := headers
	if len(fields) == 0 {
		for k := range first.All() {
			fields = append(fields, k)
		}
		slices.Sort(fields) // alphabetical for determinism
	}
	schema := lib.NewSchema()
	for _, f := range fields {
		v, _ := ssql.Get[any](first, f)
		schema.AddField(f, auxTypeString(v))
	}

	seq := func(yield func(ssql.Record) bool) {
		defer stop()
		if !yield(first) {
			return
		}
		for {
			r, ok := next()
			if !ok {
				return
			}
			if !yield(r) {
				return
			}
		}
	}
	return seq, schema, nil
}

func auxTypeString(v any) string {
	switch v.(type) {
	case int64, int:
		return "int"
	case float64:
		return "float"
	case bool:
		return "bool"
	case time.Time:
		return "time"
	default:
		return "string"
	}
}
