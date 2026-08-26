package commands

// autocli host hooks for field-name and value completion (autocli
// v4.14.0). autocli's built-in extractors read local csv/tsv/json(l)
// headers itself; these hooks answer the sources it can't — parquet
// footers and http(s) URLs — by asking the command (DFC115: `from` is
// the authority on its own files), never by growing format knowledge
// in a second place. Anything the built-ins already handle returns
// errCompletionBuiltin so the faster in-autocli path keeps serving it.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	ssql "github.com/rosscartlidge/ssql/v4"
)

func init() {
	cf.FieldSource = completionFieldSource
	cf.FieldValueSource = completionFieldValueSource
}

var errCompletionBuiltin = fmt.Errorf("autocli built-in handles this source")

// completionFieldSource answers field names for parquet files and
// http(s) URLs by exec'ing `SSQL_MODE=schema ssql from SOURCE` and
// parsing the `_schema` header — the same protocol Ctrl-O and serve
// use, so every format `from` can route (including future ones) works
// here with zero grammar duplication. (.arrow is left to the hint:
// `from arrow` has no schema mode yet — see TODO.)
func completionFieldSource(source string) ([]string, error) {
	fi, known := formatForPath(source)
	// .arrow stays at the hint until `from arrow` grows schema mode.
	answerable := known && fi.Binary && fi.Name == "parquet"
	if !ssql.IsHTTPURL(source) && !answerable {
		return nil, errCompletionBuiltin
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "from", source)
	cmd.Env = append(os.Environ(), "SSQL_MODE=schema")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("schema mode for %s: %w", source, err)
	}
	line, _, _ := strings.Cut(string(out), "\n")
	fields, ok := ssql.ParseSchemaHeaderFields([]byte(line))
	if !ok {
		return nil, fmt.Errorf("no schema header from %s", source)
	}
	return fields, nil
}

// completionFieldValueSource samples values for the sources autocli's
// samplers can't read: parquet (local or URL — ReadParquetColumns
// prunes to the one column, and over http fetches only its byte
// ranges) and URL line formats (streamed, first maxRecords records).
func completionFieldValueSource(source, field string, maxSamples, maxRecords int) ([]string, error) {
	fi, known := formatForPath(source)
	isURL := ssql.IsHTTPURL(source)

	var records func(yield func(ssql.Record) bool)
	var closer io.Closer
	switch {
	case known && fi.Name == "parquet":
		seq, err := ssql.ReadParquetColumns(source, []string{field})
		if err != nil {
			return nil, err
		}
		records = seq
	case isURL && known && !fi.Binary:
		body, err := ssql.OpenHTTPStream(source)
		if err != nil {
			return nil, err
		}
		closer = body
		r := bufio.NewReader(body)
		switch fi.Name {
		case "csv":
			records = ssql.ReadCSVFromReader(r, ssql.DefaultCSVConfig())
		case "tsv":
			records = ssql.ReadTSVFromReader(r)
		default:
			records = ssql.ReadJSONFromReader(r)
		}
	default:
		return nil, errCompletionBuiltin
	}
	if closer != nil {
		defer closer.Close()
	}

	seen := make(map[string]bool)
	n := 0
	for r := range records {
		if n++; n > maxRecords {
			break
		}
		if v, ok := ssql.Get[any](r, field); ok {
			seen[fmt.Sprint(v)] = true
			if len(seen) >= maxSamples {
				break
			}
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("no values for %s in %s", field, source)
	}
	values := make([]string, 0, len(seen))
	for v := range seen {
		values = append(values, v)
	}
	sort.Strings(values)
	return values, nil
}
