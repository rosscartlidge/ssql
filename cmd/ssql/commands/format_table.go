package commands

// The format authority table (DFC116 F1+F2). Extension→format
// knowledge lived in seven files — from.go, aux_input.go, union.go,
// from_records.go, serve_http.go, cursor_context.go,
// completion_sources.go — and serve additionally knew which formats
// support `-sample` (from's flag grammar restated elsewhere: the
// drift shape that took three bugs in one week before `-records`).
// This table is the ONE place extension grammar and per-format
// capability facts live, colocated with `from` (the commands that own
// the formats). Consumers look up facts here and keep only their own
// REACTIONS (route, refuse, suggest, generate) at the call site.
//
// Adding a format or alias: add the extension row here; every
// consumer inherits it.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ssql "github.com/rosscartlidge/ssql/v4"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

type formatInfo struct {
	// Name is the canonical `from` subcommand ("csv", "jsonl",
	// "parquet", …) — consumers that build pipelines or dispatch
	// per-format switch on this, never on raw extensions.
	Name string

	// Sampleable: `from NAME FILE -sample N` exists (byte-offset
	// line sampling — csv/tsv/jsonl).
	Sampleable bool

	// DirectAux: join/merge/union read the file directly
	// (readAuxInput) instead of requiring a process substitution.
	DirectAux bool

	// Binary: not line-parseable — completion's built-in extractors
	// can't read it (the host FieldSource hook answers instead), and
	// remote line counts are impossible without a download.
	Binary bool

	// CheapRecords: `from -records` has a cheap local count
	// (parquet footer, newline count).
	CheapRecords bool
}

var formatByExt = map[string]formatInfo{
	".csv":     {Name: "csv", Sampleable: true, DirectAux: true, CheapRecords: true},
	".tsv":     {Name: "tsv", Sampleable: true, DirectAux: true, CheapRecords: true},
	".json":    {Name: "json", DirectAux: true, CheapRecords: true},
	".jsonl":   {Name: "jsonl", Sampleable: true, DirectAux: true, CheapRecords: true},
	".ndjson":  {Name: "jsonl", Sampleable: true, DirectAux: true, CheapRecords: true},
	".parquet": {Name: "parquet", Binary: true, CheapRecords: true},
	".arrow":   {Name: "arrow", Binary: true},
	".wav":     {Name: "wav", Binary: true},
	".xlsx":    {Name: "xlsx", Binary: true},
	".log":     {Name: "lines"}, // raw text: one record per line (from lines)
	".txt":     {Name: "lines"},
}

// pathExt is the lowercase extension of a local path or http(s) URL
// (URL query/fragment ignored — presigned URLs).
func pathExt(path string) string {
	if ssql.IsHTTPURL(path) {
		return ssql.HTTPURLExt(path)
	}
	return strings.ToLower(filepath.Ext(path))
}

// formatForPath resolves a local path or URL to its format facts.
// ok=false means the extension names no known format — the caller
// decides its own reaction (from's bare form defaults to JSON for
// local files and refuses for URLs; others refuse or fall back).
func formatForPath(path string) (formatInfo, bool) {
	fi, ok := formatByExt[pathExt(path)]
	return fi, ok
}

// readRecordsFile materializes a whole csv/json/jsonl file as records
// — the shared input path for commands that need the full dataset in
// memory (the DSP stages: fft, ifft, convolve, correlate,
// spectrogram). Was five identical extension switches (DFC116 F1).
func readRecordsFile(path string) ([]ssql.Record, error) {
	fi, _ := formatForPath(path)
	switch fi.Name {
	case "csv":
		recs, err := ssql.ReadCSV(path)
		if err != nil {
			return nil, fmt.Errorf("reading CSV: %w", err)
		}
		return slices.Collect(recs), nil
	case "json":
		recs, err := ssql.ReadJSON(path)
		if err != nil {
			return nil, fmt.Errorf("reading JSON: %w", err)
		}
		return slices.Collect(recs), nil
	case "jsonl":
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening JSONL file: %w", err)
		}
		defer f.Close()
		return slices.Collect(lib.ReadJSONL(f)), nil
	default:
		return nil, fmt.Errorf("unsupported file format: %s", path)
	}
}
