package commands

// The -records protocol (DFC115 direction, Ross's design): `from …
// -records` prints ONE integer — the number of records this exact
// invocation would produce — computed the cheapest way the format
// allows, and nothing else. Like completion, help and schema mode,
// the answer lives IN the from command, which owns the flag grammar
// and format knowledge; external consumers (serve's throughput
// display) exec it instead of re-parsing from-args with a drifting
// grammar copy (three drift bugs in one week motivated this).
//
// Cost classes: parquet = O(footer); csv/tsv/jsonl = O(bytes) newline
// scan at memory bandwidth; stdin and JSON arrays have no cheap count
// and refuse loudly (use `| ssql count`).

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// runFromRecords implements -records for a from invocation. format is
// the explicit subcommand ("" for the bare form — inferred per file
// by extension); sampleN < 0 means no -sample flag.
func runFromRecords(format string, files []string, sampleN int64) error {
	if len(files) == 0 {
		return fmt.Errorf("from -records: no cheap count for stdin (counting would consume it) — use `| ssql count`")
	}
	var total int64
	for _, f := range files {
		n, err := fromRecordsCountOne(format, f)
		if err != nil {
			return fmt.Errorf("from -records: %w", err)
		}
		total += n
	}
	if sampleN > 0 && sampleN < total {
		total = sampleN
	}
	fmt.Println(total)
	return nil
}

func fromRecordsCountOne(format, file string) (int64, error) {
	if ssql.IsHTTPURL(file) {
		f := format
		if f == "" {
			switch ssql.HTTPURLExt(file) {
			case ".parquet":
				f = "parquet"
			}
		}
		if f == "parquet" {
			return ssql.ParquetRowCount(file) // footer via Range: cheap
		}
		return 0, fmt.Errorf("no cheap record count over http for %s — a remote line count would download the file; use `| ssql count`", file)
	}
	f := format
	if f == "" {
		switch strings.ToLower(filepath.Ext(file)) {
		case ".csv":
			f = "csv"
		case ".tsv":
			f = "tsv"
		case ".jsonl", ".ndjson":
			f = "jsonl"
		case ".json":
			f = "json"
		case ".parquet":
			f = "parquet"
		default:
			return 0, fmt.Errorf("no cheap record count for %s (unsupported format)", file)
		}
	}
	switch f {
	case "parquet":
		return ssql.ParquetRowCount(file)
	case "csv", "tsv":
		lines, err := ssql.CountFileLines(file)
		if err != nil {
			return 0, err
		}
		if lines > 0 {
			lines-- // header
		}
		return lines, nil
	case "jsonl", "json":
		return jsonlRecordCount(file)
	default:
		return 0, fmt.Errorf("no cheap record count for format %q", f)
	}
}

// jsonlRecordCount counts JSONL records: newline count, minus a
// leading {"_schema":…} header line when present. JSON ARRAY files
// have no cheap count (counting is parsing) and refuse loudly.
func jsonlRecordCount(file string) (int64, error) {
	fh, err := os.Open(file)
	if err != nil {
		return 0, err
	}
	br := bufio.NewReader(fh)
	head, _ := br.Peek(64 * 1024)
	trimmed := strings.TrimLeft(string(head), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		fh.Close()
		return 0, fmt.Errorf("%s is a JSON array — counting requires parsing it; use `| ssql count`", file)
	}
	var headerLines int64
	if nl := strings.IndexByte(string(head), '\n'); nl >= 0 {
		if _, ok := lib.ParseSchemaHeaderFromBytes([]byte(head[:nl])); ok {
			headerLines = 1
		}
	}
	fh.Close()
	lines, err := ssql.CountFileLines(file)
	if err != nil {
		return 0, err
	}
	return lines - headerLines, nil
}
