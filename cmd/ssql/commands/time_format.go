package commands

// Time patterns on the readers and cast (DFC147 §7.2): `-format COL
// PATTERN` reads a time column whose cells are not RFC 3339 — strftime
// (%d/%m/%Y, the language DuckDB, Python and DataFusion share) or a Go
// layout. A column with a pattern is a time; `-type COL time` is implied.
// A schema sidecar's date format (DFC146) feeds the same map, under the
// user's own -format.

import (
	"fmt"
	"sort"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
)

// fromTimeFormats is the current source invocation's column → pattern
// map (user -format, then the sidecar's). `from` is one read per process,
// so it is set once by the handler and read by the config builders — the
// same shape as fromJSONSkipInvalid.
var fromTimeFormats map[string]string

// formatFlag adds -format COL PATTERN to a reader or to cast.
func formatFlag(b *cf.SubcommandBuilder, help string) *cf.SubcommandBuilder {
	return b.
		Flag("-format").
		Arg("field").
		FieldsFromFlag("").
		Done().
		Arg("pattern").
		Completer(&cf.StaticCompleter{Options: []string{"%Y-%m-%d", "%d/%m/%Y", "%m/%d/%Y", "%d/%m/%Y %H:%M", "%Y%m%d", "%d-%b-%Y", "%s"}}).
		Done().
		Accumulate().
		Global().
		Help(help).
		Done()
}

const readerFormatHelp = "Read a time column whose cells follow a pattern: -format when '%d/%m/%Y' (strftime, as in DuckDB and Python; or a Go layout). The column is a time; -type is implied"
const castFormatHelp = "Parse a text field as a time with a pattern: -format when '%d/%m/%Y' (strftime or a Go layout); -type when time is implied"

// parseFormatFlags reads the accumulated -format pairs and validates each
// pattern once (a pattern that cannot be parsed is refused here, in every
// mode, not per cell).
func parseFormatFlags(ctx *cf.Context) (map[string]string, error) {
	raw, ok := ctx.GlobalFlags["-format"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, _ := raw.([]any)
	out := map[string]string{}
	for _, item := range list {
		m, _ := item.(map[string]any)
		field, _ := m["field"].(string)
		pattern, _ := m["pattern"].(string)
		if field == "" || pattern == "" {
			continue
		}
		if _, err := ssql.TimeLayout(pattern); err != nil {
			return nil, fmt.Errorf("-format %s: %v", field, err)
		}
		out[field] = pattern
	}
	return out, nil
}

// applyTimeFormats records the formats for the source's config builders
// and implies `time` for each formatted column that has no explicit type.
// The user's -format wins over a sidecar's (already merged by
// resolveSidecarTypes).
func applyTimeFormats(overrides map[string]string, formats map[string]string) map[string]string {
	fromTimeFormats = formats
	for col := range formats {
		if overrides == nil {
			overrides = map[string]string{}
		}
		if t, ok := overrides[col]; !ok || t == "" || t == "auto" {
			overrides[col] = "time"
		} else if t != "time" {
			// A -type that says otherwise is a contradiction the user wrote.
			fromTimeFormats = nil
		}
	}
	return overrides
}

// timeFormatsFromArgs reads `-format COL PATTERN` pairs from a stage's
// argv (the SQL translator's view of the source or cast).
func timeFormatsFromArgs(args []string) map[string]string {
	var out map[string]string
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "-format" {
			if out == nil {
				out = map[string]string{}
			}
			out[args[i+1]] = args[i+2]
			i += 2
		}
	}
	return out
}

// timeFormatOptionsCode renders the typed reader options for the
// formats: `, typed.TimeFormat("when", "%d/%m/%Y")` … (fn names the
// option constructor: TimeFormat or DelimTimeFormat).
func timeFormatOptionsCode(formats map[string]string, fn string) string {
	if len(formats) == 0 {
		return ""
	}
	cols := make([]string, 0, len(formats))
	for c := range formats {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	var b strings.Builder
	for _, c := range cols {
		fmt.Fprintf(&b, ", typed.%s(%q, %q)", fn, c, formats[c])
	}
	return b.String()
}

// timeFormatsLiteral renders the CSVConfig.TimeFormats field for a
// generated record program.
func timeFormatsLiteral(formats map[string]string) string {
	if len(formats) == 0 {
		return ""
	}
	cols := make([]string, 0, len(formats))
	for c := range formats {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	var b strings.Builder
	b.WriteString("\t\tTimeFormats: map[string]string{")
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q: %q", c, formats[c])
	}
	b.WriteString("},")
	return b.String()
}

// timeFormatFlag adds -time-format PATTERN to a sink (DFC147 §7.3).
func timeFormatFlag(b *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return b.
		Flag("-time-format").
		String().
		Global().
		Default("").
		Completer(&cf.StaticCompleter{Options: []string{"%Y-%m-%d", "%Y-%m-%d %H:%M", "%d/%m/%Y", "%H:%M:%S", "%b %-d, %Y", "2006-01-02"}}).
		Help("Render time columns with a pattern on the pipeline zone's wall clock: -time-format '%Y-%m-%d %H:%M' (strftime, or a Go layout). Default: RFC 3339. The column stays a time").
		Done()
}

// sinkTimeFormat reads and validates a sink's -time-format.
func sinkTimeFormat(ctx *cf.Context) (string, error) {
	pattern, _ := ctx.GlobalFlags["-time-format"].(string)
	if pattern == "" {
		return "", nil
	}
	if err := ssql.ValidateTimePattern(pattern); err != nil {
		return "", fmt.Errorf("-time-format: %w", err)
	}
	return pattern, nil
}
