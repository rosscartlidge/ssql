package commands

import (
	"fmt"
	"iter"
	"os"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// except and intersect (DFC137 §3): the two set operations union lacks.
// Both keep LEFT rows (stdin) by membership of their key on the right
// (-file): the whole row when no key is given (SQL EXCEPT / INTERSECT),
// or the named fields (an anti-join / semi-join: the left row comes out
// unchanged, nothing from the right is added). Distinct by default;
// -all keeps duplicate left rows, and in the whole-row form is the
// multiset EXCEPT ALL / INTERSECT ALL.

// RegisterExcept registers the except subcommand.
func RegisterExcept(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	return registerSetOp(cmd, setOpExcept)
}

// RegisterIntersect registers the intersect subcommand.
func RegisterIntersect(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	return registerSetOp(cmd, setOpIntersect)
}

type setOpKind struct {
	name     string // command name and library function name (lower/upper)
	verb     string // for help text
	outVar   string // generated code's output variable
	keep     bool   // membership keeps (intersect) or drops (except)
	sqlOp    string
	exampleA string
	exampleB string
}

var (
	setOpExcept = setOpKind{
		name: "except", verb: "not in", outVar: "excepted", keep: false, sqlOp: "EXCEPT",
		exampleA: "Rows of today's file not in yesterday's (SQL EXCEPT)",
		exampleB: "Customers with no order (anti-join): the left row, unchanged",
	}
	setOpIntersect = setOpKind{
		name: "intersect", verb: "also in", outVar: "intersected", keep: true, sqlOp: "INTERSECT",
		exampleA: "Rows present in both files (SQL INTERSECT)",
		exampleB: "Customers with at least one order (semi-join): the left row, unchanged",
	}
)

func (k setOpKind) goFunc() string { return strings.ToUpper(k.name[:1]) + k.name[1:] }

// setOpKey is the membership key: empty = whole row.
type setOpKey struct {
	left, right []string
}

func (k setOpKey) wholeRow() bool { return len(k.left) == 0 }

func registerSetOp(cmd *cf.CommandBuilder, kind setOpKind) *cf.CommandBuilder {
	cmd.Subcommand(kind.name).
		Description(fmt.Sprintf("Keep the rows on stdin that are %s the -file side (SQL %s); with -using/-on, by key", kind.verb, kind.sqlOp)).
		Example(fmt.Sprintf("ssql from today.csv | ssql %s -file yesterday.csv", kind.name), kind.exampleA).
		Example(fmt.Sprintf("ssql from customers.csv | ssql %s -file orders.csv -using customer_id", kind.name), kind.exampleB).
		Example(fmt.Sprintf("ssql from a.csv | ssql %s -all -file <(ssql from csv b.csv | ssql where -if ok eq true)", kind.name), fmt.Sprintf("%s ALL against a pipeline", kind.sqlOp)).

		Flag("-generate", "-g").
			Bool().
			Global().
			Help("Generate Go code instead of executing").
			Done().

		Flag("-file", "-f").
			String().
			Completer(&cf.FileCompleter{Pattern: "*.{jsonl,csv,tsv,json}"}).
			Accumulate().
			Local().
			Help("Right-side file (csv/tsv/json/schema-headed jsonl, or <(pipeline)); repeat to apply each in turn").
			Done().

		Flag("-all", "-a").
			Bool().
			Global().
			Help(fmt.Sprintf("Keep duplicate rows (%s ALL); default is distinct", kind.sqlOp)).
			Done().

		Flag("-using").
			String().
			FieldsFromFlag("").
			Accumulate().
			Local().
			Help("Key field with the same name on both sides (repeat for a composite key)").
			Done().

		Flag("-on").
			Arg("left-field").
				FieldsFromFlag("").
				Done().
			Arg("right-field").
				Completer(&cf.NoCompleter{Hint: FieldHintToken}).
				Done().
			Accumulate().
			Local().
			Help("Key fields with different names: -on <left> <right>").
			Done().

		Handler(func(ctx *cf.Context) error {
			all, _ := ctx.GlobalFlags["-all"].(bool)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)

			var files []string
			key := setOpKey{}
			if len(ctx.Clauses) > 0 {
				clause := ctx.Clauses[0]
				files = clauseStrings(clause, "-file")
				for _, f := range clauseStrings(clause, "-using") {
					key.left = append(key.left, f)
					key.right = append(key.right, f)
				}
				if onRaw, ok := clause.Flags["-on"].([]any); ok {
					for _, v := range onRaw {
						if m, ok := v.(map[string]any); ok {
							l, _ := m["left-field"].(string)
							r, _ := m["right-field"].(string)
							if l == "" || r == "" {
								return fmt.Errorf("%s -on needs <left> <right>", kind.name)
							}
							key.left = append(key.left, l)
							key.right = append(key.right, r)
						}
					}
				}
			}
			if len(files) == 0 {
				return fmt.Errorf("%s: at least one -file required", kind.name)
			}

			if shouldGenerate(generate) {
				return generateSetOpCode(kind, files, key, all)
			}

			in := lib.ReadJSONLWithSchema(ctx.Stdin())
			result := in.Records
			for _, file := range files {
				right, rightSchema, err := readAuxInput(file)
				if err != nil {
					return err
				}
				if rightSchema == nil {
					return fmt.Errorf("file %s has no schema header — pipe through ssql first: <(ssql from jsonl %s)", file, file)
				}
				if err := key.validate(kind.name, in.Schema, rightSchema); err != nil {
					return err
				}
				result = setOpFilter(kind, right, key, all)(result)
			}
			if err := lib.WriteJSONLWithSchema(ctx.Stdout(), in.Schema, result); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			return nil
		}).
		Done()
	return cmd
}

// clauseStrings returns an accumulated string flag's values.
func clauseStrings(clause cf.Clause, flag string) []string {
	var out []string
	if raw, ok := clause.Flags[flag].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// validate fails loudly on a key field neither schema has. A nil schema
// (headerless stdin) cannot be checked; the absent-key rule then applies
// per row.
func (k setOpKey) validate(cmdName string, left, right *lib.Schema) error {
	for _, f := range k.left {
		if left != nil && !left.HasFieldOrPath(f) {
			return fmt.Errorf("%s: left field %q not found (available: %s)", cmdName, f, strings.Join(left.Fields, ", "))
		}
	}
	for _, f := range k.right {
		if right != nil && !right.HasFieldOrPath(f) {
			return fmt.Errorf("%s: right field %q not found (available: %s)", cmdName, f, strings.Join(right.Fields, ", "))
		}
	}
	return nil
}

func setOpFilter(kind setOpKind, right iter.Seq[ssql.Record], key setOpKey, all bool) ssql.Filter[ssql.Record, ssql.Record] {
	lk, rk := ssql.WholeRow, ssql.WholeRow
	if !key.wholeRow() {
		lk, rk = ssql.FieldsKey(key.left...), ssql.FieldsKey(key.right...)
	}
	if kind.keep {
		return ssql.Intersect(right, lk, rk, all)
	}
	return ssql.Except(right, lk, rk, all)
}

// generateSetOpCode emits the record or typed fragments. Each -file is a
// side source: a process substitution's fragments become a func
// fragment (as join and union do), a named file an init fragment that
// reads it; the stage applies one library call per source in order.
func generateSetOpCode(kind setOpKind, files []string, key setOpKey, all bool) error {
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}
	inputVar := "records"
	var leftSchema *lib.TypedSchema
	if len(fragments) > 0 {
		inputVar = fragments[len(fragments)-1].Var
		leftSchema = fragments[len(fragments)-1].OutputTypedSchema
	}
	if typedMode() && leftSchema != nil {
		return emitTypedSetOp(kind, inputVar, leftSchema, files, key, all)
	}

	var keyArgs string
	if key.wholeRow() {
		keyArgs = "ssql.WholeRow, ssql.WholeRow"
	} else {
		keyArgs = fmt.Sprintf("ssql.FieldsKey(%s), ssql.FieldsKey(%s)", quotedList(key.left), quotedList(key.right))
	}
	expr := inputVar
	needsLib := false
	for i, file := range files {
		src, usesLib, err := sideSourceFragment(kind.name, i+1, file)
		if err != nil {
			return err
		}
		needsLib = needsLib || usesLib
		expr = fmt.Sprintf("ssql.%s(%s, %s, %t)(%s)", kind.goFunc(), src, keyArgs, all, expr)
	}
	code := fmt.Sprintf("%s := %s", kind.outVar, expr)
	var imports []string
	if needsLib {
		imports = []string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"}
	}
	return lib.WriteCodeFragment(lib.NewStmtFragment(kind.outVar, inputVar, code, imports, getCommandString()))
}

// sideSourceFragment writes the fragment that reads one side file for
// a record-mode stage and returns the Go expression that yields its
// records: a func call for a process substitution, a variable for a
// named file. needsLib reports whether the reading code imports lib.
func sideSourceFragment(stage string, n int, file string) (expr string, needsLib bool, err error) {
	if fi, statErr := os.Stat(file); statErr == nil && !fi.Mode().IsRegular() {
		if inner, err := lib.ReadCodeFragmentsFromFile(file); err == nil && len(inner) > 0 {
			funcName := fmt.Sprintf("%sSource%d", stage, n)
			if err := lib.WriteCodeFragment(lib.NewFuncFragment(funcName, inner, getCommandString())); err != nil {
				return "", false, fmt.Errorf("writing func fragment from %s: %w", file, err)
			}
			return funcName + "()", false, nil
		}
	}
	varName := fmt.Sprintf("%sFile%d", stage, n)
	code, imports, needsLib := sideFileReadCode(varName, file)
	if err := lib.WriteCodeFragment(lib.NewInitFragment(varName, code, imports, "")); err != nil {
		return "", false, fmt.Errorf("writing file read fragment: %w", err)
	}
	return varName, needsLib, nil
}

// sideFileReadCode is the record-mode read of a named side file into
// varName, extension-inferred as exec's readAuxInput: csv and tsv
// through the package readers, anything else through the schema-aware
// JSONL reader (lib). Any error is reported and fatal.
func sideFileReadCode(varName, file string) (code string, imports []string, needsLib bool) {
	format := ""
	if fi, ok := formatForPath(file); ok {
		format = fi.Name
	}
	switch format {
	case "csv", "tsv":
		reader := "ReadCSV"
		if format == "tsv" {
			reader = "ReadTSV"
		}
		return fmt.Sprintf(`%s, err := ssql.%s(%q)
	if err != nil {
		return fmt.Errorf("opening %s: %%w", err)
	}`, varName, reader, file, file), []string{"fmt", "os"}, false
	default:
		return fmt.Sprintf(`%sHandle, err := os.Open(%q)
	if err != nil {
		return fmt.Errorf("opening %s: %%w", err)
	}
	defer %sHandle.Close()
	%s := lib.ReadJSONLWithSchema(%sHandle).Records`, varName, file, file, varName, varName, varName),
			[]string{"fmt", "os", "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"}, true
	}
}

func quotedList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// emitTypedSetOp: each -file becomes a func fragment yielding the right
// row type (a process substitution's own type; a named CSV/TSV sampled
// here, and read AS THE LEFT TYPE in the whole-row form, whose schemas
// must match as union's must). Keys are Go field accessors, numbers
// widened to float64 across int/float as join does. Dual templates for
// the membership-only forms (a per-shard probe of the shared set); the
// distinct forms compose DistinctParallel on top; the multiset forms
// (EXCEPT ALL / INTERSECT ALL) and chains over several files are
// serial.
func emitTypedSetOp(kind setOpKind, inputVar string, leftSchema *lib.TypedSchema, files []string, key setOpKey, all bool) error {
	fail := func(err error) error {
		return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("ssql generate go -typed: %s: %w", kind.name, err))
	}
	type side struct {
		call   string
		schema *lib.TypedSchema
	}
	var sides []side
	for i, file := range files {
		funcName := fmt.Sprintf("%sSource%d", kind.name, i+1)
		var body []*lib.CodeFragment
		var rightSchema *lib.TypedSchema
		if fi, statErr := os.Stat(file); statErr == nil && !fi.Mode().IsRegular() {
			inner, err := lib.ReadCodeFragmentsFromFile(file)
			if err != nil {
				return fail(fmt.Errorf("reading subprocess fragments: %w", err))
			}
			rightSchema = findOutputSchema(inner)
			if rightSchema == nil {
				return fail(fmt.Errorf("the -file pipeline did not produce a typed schema (it must use typed-mode commands)"))
			}
			body = inner
		} else {
			typeName := ""
			if key.wholeRow() {
				typeName = leftSchema.TypeName // read as the left type; schemas are checked below
			}
			format := ""
			if fi, ok := formatForPath(file); ok {
				format = fi.Name
			}
			var structDef, readCode string
			var err error
			switch format {
			case "csv":
				rightSchema, structDef, err = lib.SampleCSVSchema(file, typeName, 0)
				if err == nil {
					readCode = fmt.Sprintf("%sFile%d := typed.ReadCSV[%s](%q)", kind.name, i+1, rightSchema.TypeName, file)
				}
			case "tsv":
				var delim byte
				rightSchema, structDef, delim, err = lib.SampleTSVSchema(file, typeName, 0)
				if err == nil {
					readCode = fmt.Sprintf("%sFile%d := typed.ReadDelim[%s](%q%s)", kind.name, i+1, rightSchema.TypeName, file, typedDelimArg(delim))
				}
			default:
				return fail(fmt.Errorf("-file %s: only csv/tsv files are read directly in typed mode — use <(ssql from %s)", file, file))
			}
			if err != nil {
				return fail(err)
			}
			init := lib.NewInitFragment(fmt.Sprintf("%sFile%d", kind.name, i+1), readCode,
				[]string{"github.com/rosscartlidge/ssql/v4/typed"}, fmt.Sprintf("ssql from %s", file))
			init.OutputTypedSchema = rightSchema
			if !key.wholeRow() {
				init.StructDefs = []string{structDef}
			}
			body = []*lib.CodeFragment{init}
		}
		call := funcName + "()"
		if key.wholeRow() {
			if err := assertCompatibleSchemas(leftSchema, rightSchema); err != nil {
				return fail(err)
			}
			call = typedConvertCall(call, rightSchema, leftSchema)
			rightSchema = leftSchema
		}
		if err := lib.WriteCodeFragment(lib.NewFuncFragment(funcName, body, fmt.Sprintf("ssql from %s", file))); err != nil {
			return fmt.Errorf("writing func fragment: %w", err)
		}
		sides = append(sides, side{call: call, schema: rightSchema})
	}

	L := leftSchema.TypeName
	identity := fmt.Sprintf("func(r %s) %s { return r }", L, L)
	imports := []string{"github.com/rosscartlidge/ssql/v4/typed"}
	frag := func(code string) *lib.CodeFragment {
		f := lib.NewStmtFragment(kind.outVar, inputVar, code, imports, getCommandString())
		f.InputTypedSchema = leftSchema
		f.OutputTypedSchema = leftSchema
		return f
	}

	// Multiset forms: serial, whole row, one type.
	if all && key.wholeRow() {
		expr := inputVar
		for _, s := range sides {
			expr = fmt.Sprintf("typed.%sAll(%s)(%s)", kind.goFunc(), s.call, expr)
		}
		f := frag(fmt.Sprintf("%s := %s", kind.outVar, expr))
		f.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
		return lib.WriteCodeFragment(f)
	}

	// Key accessors per side.
	type keyed struct {
		call, leftKey, rightKey string
	}
	var ks []keyed
	for _, s := range sides {
		if key.wholeRow() {
			ks = append(ks, keyed{s.call, identity, identity})
			continue
		}
		lk, rk, err := typedSetOpKeys(leftSchema, s.schema, key)
		if err != nil {
			return fail(err)
		}
		ks = append(ks, keyed{s.call, lk, rk})
	}

	// One side: dual templates. Stream in → membership filter per shard;
	// the distinct form dedupes across shards with DistinctParallel and
	// yields a Seq, as `distinct` does.
	if len(ks) == 1 {
		k := ks[0]
		parallel := fmt.Sprintf("typed.%sParallel(%s, %s, %s, %s)", kind.goFunc(), inputVar, k.call, k.leftKey, k.rightKey)
		serial := fmt.Sprintf("typed.%s(%s, %s, %s, %t)(%s)", kind.goFunc(), k.call, k.leftKey, k.rightKey, all, inputVar)
		produces := lib.ShapeStream
		if !all {
			parallel = fmt.Sprintf("typed.DistinctParallel(%s, %s)", parallel, identity)
			produces = lib.ShapeSeqTyped
		}
		f := frag(fmt.Sprintf("%s := %s", kind.outVar, parallel))
		f.IsStream = all
		f.Capabilities = &lib.Capabilities{Accepts: lib.ShapeStream, Produces: produces}
		f.AltCodeIfSeq = fmt.Sprintf("%s := %s", kind.outVar, serial)
		f.AltImportsIfSeq = imports
		f.AltCapabilitiesIfSeq = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped}
		return lib.WriteCodeFragment(f)
	}

	// Several sides: a serial chain.
	expr := inputVar
	for _, k := range ks {
		expr = fmt.Sprintf("typed.%s(%s, %s, %s, %t)(%s)", kind.goFunc(), k.call, k.leftKey, k.rightKey, all, expr)
	}
	f := frag(fmt.Sprintf("%s := %s", kind.outVar, expr))
	f.Capabilities = &lib.Capabilities{Accepts: lib.ShapeSeqTyped, Produces: lib.ShapeSeqTyped, SerialOnly: true}
	return lib.WriteCodeFragment(f)
}

// typedConvertCall wraps a call yielding iter.Seq[from] so it yields
// iter.Seq[to] when the two compatible schemas (assertCompatibleSchemas)
// are different Go types, as a process substitution's own row type
// against the left side's: a by-name struct rebuild through
// typed.Select. Same type: the call as given.
func typedConvertCall(call string, from, to *lib.TypedSchema) string {
	if from.TypeName == to.TypeName {
		return call
	}
	var assigns []string
	for _, f := range to.Fields {
		ff, _ := lookupSchemaField(from, f.Name)
		assigns = append(assigns, fmt.Sprintf("%s: r.%s", f.GoName, ff.GoName))
	}
	return fmt.Sprintf("typed.Select(func(r %s) %s { return %s{%s} })(%s)", from.TypeName, to.TypeName, to.TypeName, strings.Join(assigns, ", "), call)
}

// typedSetOpKeys renders the two key functions for a keyed set op. A
// composite key is a struct literal of the parts; int and float parts
// widen to float64 as join keys do, so a column typed float by one file
// still matches the other file's ints.
func typedSetOpKeys(left, right *lib.TypedSchema, key setOpKey) (leftKey, rightKey string, err error) {
	numeric := func(t string) bool { return t == "int64" || t == "float64" }
	var types, lparts, rparts []string
	for i := range key.left {
		lf, ok := lookupSchemaField(left, key.left[i])
		if !ok {
			return "", "", fmt.Errorf("left field %q not found in %s", key.left[i], left.TypeName)
		}
		rf, ok := lookupSchemaField(right, key.right[i])
		if !ok {
			return "", "", fmt.Errorf("right field %q not found in %s", key.right[i], right.TypeName)
		}
		t, le, re := lf.GoType, "l."+lf.GoName, "r."+rf.GoName
		if lf.GoType != rf.GoType {
			if !numeric(lf.GoType) || !numeric(rf.GoType) {
				return "", "", fmt.Errorf("key types differ (left %s: %s, right %s: %s)", lf.Name, lf.GoType, rf.Name, rf.GoType)
			}
			t, le, re = "float64", "float64("+le+")", "float64("+re+")"
		}
		types = append(types, t)
		lparts = append(lparts, le)
		rparts = append(rparts, re)
	}
	if len(types) == 1 {
		return fmt.Sprintf("func(l %s) %s { return %s }", left.TypeName, types[0], lparts[0]),
			fmt.Sprintf("func(r %s) %s { return %s }", right.TypeName, types[0], rparts[0]), nil
	}
	var fields []string
	for i, t := range types {
		fields = append(fields, fmt.Sprintf("K%d %s", i, t))
	}
	keyType := "struct{ " + strings.Join(fields, "; ") + " }"
	return fmt.Sprintf("func(l %s) %s { return %s{%s} }", left.TypeName, keyType, keyType, strings.Join(lparts, ", ")),
		fmt.Sprintf("func(r %s) %s { return %s{%s} }", right.TypeName, keyType, keyType, strings.Join(rparts, ", ")), nil
}
