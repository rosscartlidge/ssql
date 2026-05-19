//go:build !slim

package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
)

// buildServeCLI assembles the small autocli Command tree shown at the
// SSH prompt. Each handler reads from ctx.State (a *serveState) and
// writes to ctx.Stdout(). Handlers are intentionally minimal in v1 —
// just enough to demonstrate the operator-console UX.
//
// Future additions: query (run a pipeline against the loaded data),
// reload (re-read the file), :var (named intermediates), etc.
func buildServeCLI() *cf.Command {
	return cf.NewCommand("serve").
		Subcommand("status").
		Description("show uptime, dataset path, row count").
		Handler(serveStatusHandler).
		Done().

		Subcommand("schema").
		Description("show the dataset schema (field names + inferred types)").
		Handler(serveSchemaHandler).
		Done().

		Subcommand("count").
		Description("print total row count").
		Handler(serveCountHandler).
		Done().

		Subcommand("head").
		Description("show the first N rows (default tunable via `:set head-default-rows`)").
		Flag("-n").
			Int().
			Global().
			Default(int64(-1)).
			Help("rows to print (default: see `:set head-default-rows`)").
			Done().
		Flag("-t").
			Bool().
			Global().
			Default(false).
			Help("render as a table (default: JSONL)").
			Done().
		Handler(serveHeadHandler).
		Done().

		// Sink-style counterpart to `head`: render the (optionally limited)
		// dataset as a fixed-width text table, the same shape ssql's
		// `to table` produces. Default prints ALL rows; -n caps.
		Subcommand("to").
		Subcommand("table").
		Description("render the dataset as a fixed-width text table").
		Flag("-n").
			Int().
			Global().
			Default(int64(0)).
			Help("limit to first N rows (default 0 = all)").
			Done().
		Handler(serveToTableHandler).
		Done().
		Done().

		Build()
}

func serveStatusHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)
	w := ctx.Stdout()
	fmt.Fprintf(w, "uptime:  %v\n", time.Since(srv.started).Round(time.Second))
	fmt.Fprintf(w, "loaded:  %v ago in %v\n", time.Since(srv.started).Round(time.Second), srv.loadTime)
	fmt.Fprintf(w, "path:    %s\n", srv.path)
	fmt.Fprintf(w, "rows:    %d\n", len(srv.records))
	fmt.Fprintf(w, "fields:  %d\n", len(srv.schema))
	return nil
}

func serveSchemaHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)
	w := ctx.Stdout()
	if len(srv.records) == 0 {
		fmt.Fprintln(w, "(empty dataset — no schema)")
		return nil
	}
	r := srv.records[0]
	for _, name := range srv.schema {
		v := ssql.GetOr[any](r, name, nil)
		fmt.Fprintf(w, "  %-20s  %s\n", name, inferTypeForServe(v))
	}
	return nil
}

func serveCountHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)
	fmt.Fprintln(ctx.Stdout(), len(srv.records))
	return nil
}

// serveToTableHandler renders the dataset as a fixed-width table —
// symmetric to ssql's `to table` sink in normal pipelines. Default is
// to print everything (the dataset is already in memory); -n caps.
func serveToTableHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)

	n := 0
	switch v := ctx.GlobalFlags["-n"].(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	}
	if n <= 0 || n > len(srv.records) {
		n = len(srv.records)
	}

	return renderTableTo(ctx.Stdout(), srv.records[:n], srv.schema)
}

func serveHeadHandler(ctx *cf.Context) error {
	srv := ctx.State.(*serveState)
	w := ctx.Stdout()

	// autocli Int() flags arrive as `int`; the Default we set is int64
	// to satisfy the builder signature. Handle both. -1 is the
	// sentinel meaning "use the head-default-rows Setting".
	n := -1
	switch v := ctx.GlobalFlags["-n"].(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	}
	if n < 0 {
		n = int(srv.headDefault.Load())
	}
	if n <= 0 || n > len(srv.records) {
		n = len(srv.records)
	}

	asTable, _ := ctx.GlobalFlags["-t"].(bool)
	if asTable {
		return renderTableTo(w, srv.records[:n], srv.schema)
	}

	// JSONL default — terse, easy to pipe / parse on the operator's end.
	enc := json.NewEncoder(w)
	for i := 0; i < n; i++ {
		obj := make(map[string]any, len(srv.schema))
		for _, k := range srv.schema {
			obj[k] = ssql.GetOr[any](srv.records[i], k, nil)
		}
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}
	return nil
}

// inferTypeForServe returns a short, readable type tag for a value.
// Aligned with how ssql infers types in CSV (int64/float64/string).
func inferTypeForServe(v any) string {
	switch v.(type) {
	case int, int32, int64:
		return "int"
	case float32, float64:
		return "float"
	case bool:
		return "bool"
	case time.Time:
		return "time"
	case nil:
		return "null"
	default:
		return "string"
	}
}

// renderTableTo writes rows + schema as a fixed-width text table to
// the writer. Goes through an io.Writer (the SSH channel) rather than
// ctx.Stdout() — ssql.DisplayTable writes to stdout directly which would
// not reach the operator's session. Refactoring DisplayTable to
// accept an io.Writer is on the list; for now keep it inline.
//
// Column widths are computed from the data with a 50-rune cap.
func renderTableTo(w io.Writer, rows []ssql.Record, schema []string) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no rows)")
		return err
	}
	widths := make(map[string]int, len(schema))
	for _, col := range schema {
		widths[col] = len(col)
	}
	cellAt := func(r ssql.Record, col string) string {
		v := ssql.GetOr[any](r, col, nil)
		s := fmt.Sprintf("%v", v)
		if len(s) > 50 {
			s = s[:47] + "..."
		}
		return s
	}
	for _, r := range rows {
		for _, col := range schema {
			if n := len(cellAt(r, col)); n > widths[col] {
				widths[col] = n
			}
		}
	}
	var sb strings.Builder
	for i, col := range schema {
		if i > 0 {
			sb.WriteString("  ")
		}
		sb.WriteString(padRight(col, widths[col]))
	}
	sb.WriteByte('\n')
	sb.WriteString(strings.Repeat("-", sb.Len()-1))
	sb.WriteByte('\n')
	for _, r := range rows {
		for i, col := range schema {
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(padRight(cellAt(r, col), widths[col]))
		}
		sb.WriteByte('\n')
	}
	_, err := w.Write([]byte(sb.String()))
	return err
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}
