package commands

import (
	"fmt"
	"os"
	"regexp"
	"strconv"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// rangeOpts is `from range N [-start S] [-step K] [-field NAME]` as the
// exec path, the code generators and the SQL translator all read it
// (DFC154): count rows of one int column holding start, start+step, …
type rangeOpts struct {
	count, start, step int64
	field              string
}

var rangeFieldRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validate is the one place the generator's grammar is checked: a
// negative count, a zero step or a column name no lane can carry are
// refused loudly here, before any lane runs.
func (o rangeOpts) validate() error {
	if o.count < 0 {
		return fmt.Errorf("from range: COUNT must be 0 or more, got %d", o.count)
	}
	if o.step == 0 {
		return fmt.Errorf("from range: -step must not be 0")
	}
	if !rangeFieldRe.MatchString(o.field) {
		return fmt.Errorf("from range: -field %q must be a plain identifier (letters, digits, _; not starting with a digit)", o.field)
	}
	return nil
}

// parseRangeArgs reads the range grammar from the raw stage arguments
// (after "range"), for the SQL translator which sees argv, not a parsed
// autocli context. The same defaults as the subcommand's flags.
func parseRangeArgs(args []string) (rangeOpts, error) {
	o := rangeOpts{start: 1, step: 1, field: "n"}
	seen := false
	intArg := func(i int, flag string) (int64, error) {
		if i+1 >= len(args) {
			return 0, fmt.Errorf("from range: %s needs a value", flag)
		}
		v, err := strconv.ParseInt(args[i+1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("from range: %s wants an integer, got %q", flag, args[i+1])
		}
		return v, nil
	}
	for i := 0; i < len(args); i++ {
		var err error
		switch args[i] {
		case "-start":
			o.start, err = intArg(i, "-start")
			i++
		case "-step":
			o.step, err = intArg(i, "-step")
			i++
		case "-field":
			if i+1 >= len(args) {
				return o, fmt.Errorf("from range: -field needs a value")
			}
			o.field = args[i+1]
			i++
		case "-tz":
			i++
		case "-generate", "-g", "-records":
		default:
			if seen {
				return o, fmt.Errorf("from range: unexpected argument %q", args[i])
			}
			o.count, err = strconv.ParseInt(args[i], 10, 64)
			if err != nil {
				return o, fmt.Errorf("from range: COUNT wants an integer, got %q", args[i])
			}
			seen = true
		}
		if err != nil {
			return o, err
		}
	}
	if !seen {
		return o, fmt.Errorf("from range: COUNT is required")
	}
	return o, o.validate()
}

func registerFromRange(cmd *cf.SubcommandBuilder) {
	zoneFlag(cmd.Subcommand("range")).
		Description("Generate COUNT rows of one int column n = start, start+step, … — a fixture, demo or benchmark source that needs no file; every other column is an expression on n").
		Example("ssql from range 10 | ssql update -set-expr sq 'n * n' | ssql to table", "Ten rows with no input file").
		Example("ssql from range 1000 -start 0 | ssql update -set-expr kind 'n % 3' -set-expr key 'md5(string(n))'", "Cycling categories and stable keys from the number").
		Example("ssql from range 365 -start 0 | ssql update -set-expr day 'date_add(date(\"2026-01-01\"), n, \"day\")'", "A date spine to join against").

		Flag("COUNT").
			Int().
			Required().
			Global().
			Help("Number of rows to generate").
			Done().

		Flag("-start").
			Int().
			Global().
			Default(1).
			Help("Value of the first row (default 1, like line numbers; -start 0 for DuckDB's convention)").
			Done().

		Flag("-step").
			Int().
			Global().
			Default(1).
			Help("Difference between rows (any non-zero integer)").
			Done().

		Flag("-field").
			String().
			Completer(cf.NoCompleter{Hint: "<column-name>"}).
			Global().
			Default("n").
			Help("Name of the generated column (default n)").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-records").
			Bool().
			Global().
			Default(false).
			Help("Print only the record count (COUNT) and exit").
			Done().

		Handler(func(ctx *cf.Context) error {
			if err := resolveFromZone(ctx); err != nil {
				return err
			}
			o := rangeOpts{start: 1, step: 1, field: "n"}
			if v, ok := ctx.GlobalFlags["COUNT"].(int); ok {
				o.count = int64(v)
			}
			if v, ok := ctx.GlobalFlags["-start"].(int); ok {
				o.start = int64(v)
			}
			if v, ok := ctx.GlobalFlags["-step"].(int); ok {
				o.step = int64(v)
			}
			if v, ok := ctx.GlobalFlags["-field"].(string); ok && v != "" {
				o.field = v
			}
			generate, _ := ctx.GlobalFlags["-generate"].(bool)
			records, _ := ctx.GlobalFlags["-records"].(bool)
			return executeFromRange(o, generate, records)
		}).
		Done()
}

// executeFromRange handles `from range`: the -records protocol answer is
// the count itself; schema mode types the column as int so Ctrl-O
// downstream sees it; then codegen or exec.
func executeFromRange(o rangeOpts, generate, records bool) error {
	if err := o.validate(); err != nil {
		return err
	}
	if records {
		fmt.Println(o.count)
		return nil
	}
	if schemaMode() {
		return writeSchemaModeOutputTyped(os.Stdout, []string{o.field}, map[string]string{o.field: lib.TypeInt})
	}
	if shouldGenerate(generate) {
		return generateFromRangeCode(o)
	}
	return writeWithInferredSchema(ssql.RangeRecords(o.start, o.step, o.count, o.field), writeWithInferredSchemaOptions{fieldOrder: []string{o.field}})
}

// generateFromRangeCode emits the source fragment. count, start and
// step become flags of the generated program, so the binary is a
// reusable generator (`./prog -count 1000000`). The typed form is a
// dual template: typed.RangeParallel (Stream[T]) with typed.Range
// (iter.Seq[T]) as the serial alternative; the row struct is rendered
// inline because -field names its one column.
func generateFromRangeCode(o rangeOpts) error {
	params := []lib.CodeParam{
		{Name: "count", Default: strconv.FormatInt(o.count, 10), Help: "rows to generate", VarName: "flagCount", Type: "int"},
		{Name: "start", Default: strconv.FormatInt(o.start, 10), Help: "value of the first row", VarName: "flagStart", Type: "int"},
		{Name: "step", Default: strconv.FormatInt(o.step, 10), Help: "difference between rows", VarName: "flagStep", Type: "int"},
	}
	const args = `int64(*flagStart), int64(*flagStep), int64(*flagCount)`
	if typedMode() {
		schema := &lib.TypedSchema{
			TypeName: "RangeRow",
			Fields: []lib.TypedSchemaField{
				{Name: o.field, GoName: lib.GoNameFromColumn(o.field), GoType: "int64"},
			},
		}
		goName := schema.Fields[0].GoName
		mk := fmt.Sprintf(`func(v int64) RangeRow { return RangeRow{%s: v} }`, goName)
		imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
		frag := fromInitFragment("records", fmt.Sprintf(`records := typed.RangeParallel(%s, 0, %s)`, args, mk), imports)
		frag.Params = params
		frag.OutputTypedSchema = schema
		frag.StructDefs = []string{lib.RenderStructDefWithComment(schema, "RangeRow is the generated row: one int64 column counted by `from range`.")}
		frag.IsStream = true
		frag.Capabilities = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeStream}
		frag.AltCodeIfSeq = fmt.Sprintf(`records := typed.Range(%s, %s)`, args, mk)
		frag.AltImportsIfSeq = append([]string{}, imports...)
		frag.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeNone, Produces: lib.ShapeSeqTyped}
		return lib.WriteCodeFragment(frag)
	}
	frag := fromInitFragment("records", fmt.Sprintf(`records := ssql.RangeRecords(%s, %q)`, args, o.field), nil)
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
