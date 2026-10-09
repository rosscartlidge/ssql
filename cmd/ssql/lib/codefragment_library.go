package lib

// Library mode (DFC142 §5a, shape 2): `ssql generate go -package P
// -func F` assembles the same fragment stream the program assemblers
// consume into an importable function
//
//	func F(in iter.Seq[In], p FParams) iter.Seq2[Out, error]
//
// instead of a package main. The source stage becomes the `in`
// argument (plus a FFromCSV / FFromTSV reader form when the source
// was a delimited file), every -param and lifted literal becomes a
// field of FParams (FDefaults() holds the pipeline's own values), the
// sink is dropped (the caller consumes the rows), and the body runs
// under ssql.Safely so a stage failure ends the sequence with
// (zero, err) instead of exiting the process. Generated type names are
// prefixed with F so two pipelines over the same file can share a
// package. The typed source becomes typed.ParallelBatched(in, p.Shards)
// — rows enter the Stream in batches, no channel transit per row
// (claude/concurrency.md §1) — with `records := in` as its serial
// alternative, so the planner keeps or drops the parallel plan exactly
// as it does for a program.

import (
	"fmt"
	"go/format"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// AssembleOptions selects the program skeleton. Zero values give the
// package-main program every mode emits today; Package selects library
// mode (Func defaults to "Pipeline").
type AssembleOptions struct {
	Package string
	Func    string
}

// libField is one FParams field: the CodeParam it stands in for and how
// it renders.
type libField struct {
	Param      CodeParam
	GoName     string
	GoType     string
	DefaultLit string
}

var goIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func assembleLibrary(fragments []*CodeFragment, opts AssembleOptions) (string, error) {
	pkg, fn := opts.Package, opts.Func
	if fn == "" {
		fn = "Pipeline"
	}
	if !goIdentRe.MatchString(pkg) || pkg == "main" {
		return "", fmt.Errorf("ssql generate go -package: %q is not a usable package name (a Go identifier other than main)", pkg)
	}
	if !goIdentRe.MatchString(fn) || !unicode.IsUpper(rune(fn[0])) {
		return "", fmt.Errorf("ssql generate go -func: %q must be an exported Go identifier (start with a capital letter)", fn)
	}

	prefixTypeNames(fragments, fn)

	typed := isTypedPipeline(fragments)

	// The source stage becomes the function's input. Its original code
	// still decides the reader form (FromCSV / FromTSV), so look before
	// rewriting. In typed mode the input enters the Stream runtime
	// through typed.ParallelBatched, with the plain sequence as the
	// serial alternative the planner may choose.
	var srcInit *CodeFragment
	for _, f := range fragments {
		if f.Type == "init" {
			if srcInit != nil {
				return "", fmt.Errorf("library mode: a pipeline with several sources (%q and %q) is not supported yet — the function takes one input sequence", srcInit.Command, f.Command)
			}
			srcInit = f
		}
	}
	if srcInit == nil {
		return "", fmt.Errorf("library mode: the pipeline has no source stage")
	}
	if typed && srcInit.OutputTypedSchema == nil {
		return "", fmt.Errorf("ssql generate go -typed: source %q cannot enter typed mode (no sampled schema); run with SSQL_MODE=record", srcInit.Command)
	}
	origSrcCode := srcInit.Code
	if typed {
		srcInit.Code = srcInit.Var + " := typed.ParallelBatched(in, *flagShards)"
		srcInit.Imports = append(libraryKeptImports(srcInit.Imports), "github.com/rosscartlidge/ssql/v4/typed")
		srcInit.IsStream = true
		srcInit.Capabilities = &Capabilities{Accepts: ShapeNone, Produces: ShapeStream}
		srcInit.AltCodeIfSeq = srcInit.Var + " := in"
		srcInit.AltImportsIfSeq = libraryKeptImports(srcInit.Imports)
		srcInit.AltCapabilitiesIfSeq = &Capabilities{Accepts: ShapeNone, Produces: ShapeSeqTyped}
	} else {
		srcInit.Code = srcInit.Var + " := in"
		srcInit.Imports = libraryKeptImports(srcInit.Imports)
	}

	if typed {
		tagRecordModeFragments(fragments)
		fragments = applyPlannerBoundaries(fragments)
		for _, frag := range fragments {
			if frag.Type == "func" && len(frag.FuncBody) > 0 {
				tagRecordModeFragments(frag.FuncBody) // a record stage in the body, as at top level
				frag.FuncBody = applyPlannerBoundaries(frag.FuncBody)
				frag.Imports = mergeBodyImports(frag.FuncBody)
			}
		}
	}

	var inits, stmts, finals, funcs []*CodeFragment
	for _, frag := range fragments {
		switch frag.Type {
		case "init":
			inits = append(inits, frag)
		case "stmt":
			stmts = append(stmts, frag)
		case "final":
			finals = append(finals, frag)
		case "func":
			funcs = append(funcs, frag)
		}
	}
	src := inits[0]

	// Element types. A Stream at the end is drained with .Serial().
	inType, outType := "ssql.Record", "ssql.Record"
	last := src
	if len(stmts) > 0 {
		last = stmts[len(stmts)-1]
	}
	returnExpr := last.Var
	if typed {
		inType = src.OutputTypedSchema.TypeName
		switch {
		case last.Capabilities != nil && last.Capabilities.Produces == ShapeSeqRecord:
			outType = "ssql.Record"
		case last.OutputTypedSchema != nil:
			outType = last.OutputTypedSchema.TypeName
			if last.Capabilities != nil && last.Capabilities.Produces == ShapeStream {
				returnExpr = last.Var + ".Serial()"
			}
		default:
			return "", fmt.Errorf("library mode: cannot determine the element type %q produces", last.Command)
		}
	}

	// Parameters: everything except the dropped source's and sinks'.
	drop := map[string]bool{}
	for _, p := range src.Params {
		drop[p.VarName] = true
	}
	for _, f := range finals {
		for _, p := range f.Params {
			drop[p.VarName] = true
		}
	}
	var params []CodeParam
	for _, p := range collectParams(fragments) {
		if !drop[p.VarName] {
			params = append(params, p)
		}
	}
	if strings.Contains(src.Code, "*flagShards") {
		params = append(params, CodeParam{Name: "shards", Default: "0", Help: "parallel shards (0 = every core)", VarName: "flagShards", Type: "int"})
	}
	fields := libraryFields(params)
	paramsType := fn + "Params"
	// A hoisted pre-compile var that closes over a parameter (the VM
	// tier's thunk reads *flagX) cannot live at package level here: the
	// bindings are locals of the function. Such vars stay in the body.
	usesParam := func(line string) bool {
		for _, f := range fields {
			if strings.Contains(line, "*"+f.Param.VarName) {
				return true
			}
		}
		return false
	}

	// Package-level declarations. where hoists its regexp/VM vars through
	// StructDefs; one that reads a parameter moves into the body too.
	var structDefs, bodyDecls []string
	for _, def := range collectTypedStructs(fragments) {
		if usesParam(def) {
			bodyDecls = append(bodyDecls, def)
		} else {
			structDefs = append(structDefs, def)
		}
	}
	var preVars []string
	seenPre := map[string]bool{}
	for _, def := range structDefs {
		seenPre[strings.TrimSpace(def)] = true
	}
	for _, v := range extractPreCompileVars(append(append([]*CodeFragment{}, stmts...), funcs...)) {
		if !seenPre[v] && !usesParam(v) {
			preVars = append(preVars, v)
			seenPre[v] = true
		}
	}
	for _, ff := range funcs {
		for _, v := range extractPreCompileVars(ff.FuncBody) {
			if !seenPre[v] {
				preVars = append(preVars, v)
				seenPre[v] = true
			}
		}
	}

	// Subprocess functions take the params struct so their bodies can
	// bind *flagX the way the main body does.
	var funcText strings.Builder
	for _, ff := range funcs {
		var text string
		if typed {
			text = generateTypedSubprocessFunction(ff)
		} else {
			text = generateSubprocessFunction(ff)
		}
		text = strings.Replace(text, "func "+ff.FuncName+"() iter.Seq[", "func "+ff.FuncName+"(p "+paramsType+") iter.Seq[", 1)
		if i := strings.Index(text, "{\n"); i >= 0 {
			text = text[:i+2] + bindParams(fields, "\t") + text[i+2:]
		}
		funcText.WriteString(text)
		funcText.WriteString("\n")
		for _, s := range stmts {
			s.Code = strings.ReplaceAll(s.Code, ff.FuncName+"()", ff.FuncName+"(p)")
		}
	}

	// The body: parameter-bound declarations, every stage in order, then
	// the last variable.
	var body strings.Builder
	for _, d := range bodyDecls {
		body.WriteString(indentLines(strings.TrimRight(d, "\n"), "\t\t"))
		body.WriteString("\n")
	}
	body.WriteString(indentLines(src.Code, "\t\t"))
	body.WriteString("\n")
	for _, s := range stmts {
		body.WriteString(indentLines(libraryStmt(s.Code, usesParam), "\t\t"))
		body.WriteString("\n")
	}
	fmt.Fprintf(&body, "\t\treturn %s\n", returnExpr)

	// Reader form for a delimited-file source.
	readerFn, readerExpr := libraryReaderForm(origSrcCode, typed, inType)

	// Imports: the kept fragments' plus what the skeleton uses, pruned
	// to those the emitted text references (an import a stage listed
	// for an error path that is now a panic would otherwise fail the
	// build).
	importSet := map[string]bool{"iter": true, "github.com/rosscartlidge/ssql/v4": true}
	if typed {
		importSet["github.com/rosscartlidge/ssql/v4/typed"] = true
	}
	if readerFn != "" {
		importSet["io"] = true
	}
	for _, imp := range src.Imports {
		if imp != "" {
			importSet[imp] = true
		}
	}
	for _, frag := range append(append([]*CodeFragment{}, stmts...), funcs...) {
		for _, imp := range frag.Imports {
			if imp != "" {
				importSet[imp] = true
			}
		}
		for _, bf := range frag.FuncBody {
			for _, imp := range bf.Imports {
				if imp != "" {
					importSet[imp] = true
				}
			}
		}
	}

	// Assemble everything after the import block first, so the imports
	// can be pruned against it.
	var rest strings.Builder
	for _, def := range structDefs {
		rest.WriteString(strings.TrimRight(def, "\n"))
		rest.WriteString("\n\n")
	}
	for _, v := range preVars {
		rest.WriteString(v + "\n")
	}
	if len(preVars) > 0 {
		rest.WriteString("\n")
	}
	rest.WriteString(funcText.String())

	fmt.Fprintf(&rest, "// %s holds the pipeline's parameters. A zero value is NOT the\n", paramsType)
	fmt.Fprintf(&rest, "// pipeline's own literals: start from %sDefaults().\n", fn)
	fmt.Fprintf(&rest, "type %s struct {\n", paramsType)
	nameW, typeW := 0, 0
	for _, f := range fields {
		nameW, typeW = max(nameW, len(f.GoName)), max(typeW, len(f.GoType))
	}
	for _, f := range fields {
		fmt.Fprintf(&rest, "\t%-*s %-*s // -%s %s\n", nameW, f.GoName, typeW, f.GoType, f.Param.Name, f.Param.Default)
	}
	rest.WriteString("}\n\n")
	fmt.Fprintf(&rest, "// %sDefaults returns the parameter values the pipeline was written with.\n", fn)
	fmt.Fprintf(&rest, "func %sDefaults() %s {\n\treturn %s{", fn, paramsType, paramsType)
	for i, f := range fields {
		if i > 0 {
			rest.WriteString(", ")
		}
		fmt.Fprintf(&rest, "%s: %s", f.GoName, f.DefaultLit)
	}
	rest.WriteString("}\n}\n\n")

	fmt.Fprintf(&rest, "// %s runs the pipeline over in. A failure in any stage ends the\n", fn)
	rest.WriteString("// sequence with (zero, err) as its last element (ssql.Safely); nothing\n")
	rest.WriteString("// is printed and nothing exits. Stop early by returning false from the\n")
	rest.WriteString("// range body, as with any iterator.\n")
	fmt.Fprintf(&rest, "func %s(in iter.Seq[%s], p %s) iter.Seq2[%s, error] {\n", fn, inType, paramsType, outType)
	rest.WriteString(bindParams(fields, "\t"))
	fmt.Fprintf(&rest, "\treturn ssql.Safely(func(in iter.Seq[%s]) iter.Seq[%s] {\n", inType, outType)
	rest.WriteString(body.String())
	rest.WriteString("\t})(ssql.Safe(in))\n}\n")
	if readerFn != "" {
		fmt.Fprintf(&rest, "\n// %s%s reads the pipeline's source format from r and runs %s over it.\n", fn, readerFn, fn)
		fmt.Fprintf(&rest, "func %s%s(r io.Reader, p %s) iter.Seq2[%s, error] {\n", fn, readerFn, paramsType, outType)
		fmt.Fprintf(&rest, "\treturn %s(%s, p)\n}\n", fn, readerExpr)
	}

	var imports []string
	for imp := range importSet {
		if importUsed(imp, rest.String()) {
			imports = append(imports, imp)
		}
	}
	sortImports(imports)

	// Header.
	var code strings.Builder
	fmt.Fprintf(&code, "package %s\n\n", pkg)
	code.WriteString("/*\n")
	mode := "record"
	if typed {
		mode = "typed"
	}
	fmt.Fprintf(&code, "Generated by ssql %s (library mode, %s):\n\n", version.Version, mode)
	fmt.Fprintf(&code, "(export SSQL_MODE=%s\n", mode)
	for _, frag := range fragments {
		if frag.Type != "func" && frag.Command != "" {
			code.WriteString(commentSafe(frag.Command) + " |\n")
		}
	}
	fmt.Fprintf(&code, "ssql generate go -package %s -func %s)\n\n", pkg, fn)
	fmt.Fprintf(&code, "The source stage `%s` supplies the `in` argument", commentSafe(src.Command))
	if readerFn != "" {
		fmt.Fprintf(&code, " (%s%s reads it from an io.Reader)", fn, readerFn)
	}
	code.WriteString(".\n")
	for _, f := range finals {
		fmt.Fprintf(&code, "The sink `%s` is dropped: the caller consumes the rows.\n", commentSafe(f.Command))
	}
	if HeaderNote != "" {
		code.WriteString("\n" + commentSafe(HeaderNote) + "\n")
	}
	code.WriteString("*/\n\n")
	if len(imports) > 0 {
		code.WriteString("import (\n")
		for _, imp := range imports {
			code.WriteString("\t" + renderImport(imp) + "\n")
		}
		code.WriteString(")\n\n")
	}
	code.WriteString(rest.String())
	// A library is a file the caller commits beside hand-written Go:
	// hand it over gofmt-clean. (Stage templates indent for a run()
	// body; here they sit one level deeper.) A formatting error is a
	// bug in the generator, but the unformatted source still compiles,
	// so emit it rather than fail.
	if formatted, err := format.Source([]byte(code.String())); err == nil {
		return string(formatted), nil
	}
	return code.String(), nil
}

// prefixTypeNames renames every struct type the fragments define (and
// every reference to it) to prefix+Name, so two generated functions over
// the same file can live in one package. Names are replaced longest
// first on word boundaries, so EmployeesRow inside EmployeesRowGroup is
// handled by the longer name's own replacement.
func prefixTypeNames(fragments []*CodeFragment, prefix string) {
	nameSet := map[string]bool{}
	var collect func(fs []*CodeFragment)
	collect = func(fs []*CodeFragment) {
		for _, f := range fs {
			for _, def := range f.StructDefs {
				for _, m := range structTypeRe.FindAllStringSubmatch(def, -1) {
					nameSet[m[1]] = true
				}
			}
			for _, s := range []*TypedSchema{f.InputTypedSchema, f.OutputTypedSchema} {
				if s != nil && s.TypeName != "" && !strings.Contains(s.TypeName, ".") {
					nameSet[s.TypeName] = true
				}
			}
			collect(f.FuncBody)
		}
	}
	collect(fragments)
	if len(nameSet) == 0 {
		return
	}
	names := make([]string, 0, len(nameSet))
	for n := range nameSet {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	res := make([]*regexp.Regexp, len(names))
	for i, n := range names {
		res[i] = regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b`)
	}
	rewrite := func(s string) string {
		for i, re := range res {
			s = re.ReplaceAllString(s, prefix+names[i])
		}
		return s
	}
	var apply func(fs []*CodeFragment)
	apply = func(fs []*CodeFragment) {
		for _, f := range fs {
			f.Code = rewrite(f.Code)
			f.AltCodeIfSeq = rewrite(f.AltCodeIfSeq)
			for i, def := range f.StructDefs {
				f.StructDefs[i] = rewrite(def)
			}
			for _, s := range []*TypedSchema{f.InputTypedSchema, f.OutputTypedSchema} {
				if s != nil && nameSet[s.TypeName] {
					s.TypeName = prefix + s.TypeName
				}
			}
			apply(f.FuncBody)
		}
	}
	apply(fragments)
}

var structTypeRe = regexp.MustCompile(`(?m)^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+struct\b`)

// libraryFields turns the kept parameters into FParams fields.
func libraryFields(params []CodeParam) []libField {
	var fields []libField
	used := map[string]bool{}
	for _, p := range params {
		name := GoNameFromColumn(strings.TrimPrefix(p.Name, "param-"))
		base := name
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s%d", base, n)
		}
		used[name] = true
		f := libField{Param: p, GoName: name}
		switch p.Type {
		case "int":
			f.GoType, f.DefaultLit = "int", orDefault(p.Default, "0")
		case "float":
			f.GoType, f.DefaultLit = "float64", orDefault(p.Default, "0")
		case "bool":
			f.GoType, f.DefaultLit = "bool", orDefault(p.Default, "false")
		default:
			f.GoType, f.DefaultLit = "string", strconv.Quote(p.Default)
		}
		fields = append(fields, f)
	}
	return fields
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// bindParams emits `flagX := &p.Field` for every field so fragment code
// written against *flagX compiles unchanged — the second renderer of
// the one CodeParam list (flagDecl is the first).
func bindParams(fields []libField, indent string) string {
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "%s%s := &p.%s\n%s_ = %s\n", indent, f.Param.VarName, f.GoName, indent, f.Param.VarName)
	}
	return b.String()
}

var (
	returnErrorfRe = regexp.MustCompile(`(?m)^(\s*)return (fmt\.Errorf\(.*\))\s*$`)
	returnErrRe    = regexp.MustCompile(`(?m)^(\s*)return err\s*$`)
)

// libraryStmt adapts a stage's code to the Safely closure: hoisted
// pre-compile vars go to package level (unless keepVar says the line
// must stay in the body, because it reads a parameter), and an error
// return written for run() becomes a panic with the same error (Safely
// delivers it).
func libraryStmt(code string, keepVar func(string) bool) string {
	var kept []string
	droppedFirst := false
	for i, line := range splitLines(code) {
		t := trimSpace(line)
		if startsWith(t, "var ") && findString(t, "runtime.MustCompile") != -1 && (keepVar == nil || !keepVar(t)) {
			droppedFirst = droppedFirst || i == 0
			continue
		}
		kept = append(kept, line)
	}
	if droppedFirst {
		// A fragment's first line is flush left and its continuation
		// lines carry one tab; with the first line gone, drop that tab.
		for i, line := range kept {
			kept[i] = strings.TrimPrefix(line, "\t")
		}
	}
	code = joinLines(kept)
	code = returnErrorfRe.ReplaceAllString(code, "${1}panic(${2})")
	code = returnErrRe.ReplaceAllString(code, "${1}panic(err)")
	return code
}

func indentLines(code, indent string) string {
	lines := splitLines(code)
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = indent + l
		}
	}
	return joinLines(lines)
}

var (
	recordReadRe = regexp.MustCompile(`ssql\.Read(CSV|TSV)\(\*flagInput((?:,[^\n]*?)?)\)\s*\n`)
	typedReadRe  = regexp.MustCompile(`typed\.Read(CSV|Delim)(?:Parallel)?\[[A-Za-z0-9_.]+\]\(\*flagInput(?:, (?:0|runtime\.GOMAXPROCS\(0\)))?\)`)
)

// libraryReaderForm decides whether the source was a plain delimited
// file read and, if so, returns the reader function's suffix (FromCSV /
// FromTSV) and the expression that reads `r` into iter.Seq[inType].
func libraryReaderForm(srcCode string, typed bool, inType string) (suffix, expr string) {
	if typed {
		m := typedReadRe.FindStringSubmatch(srcCode)
		if m == nil {
			return "", ""
		}
		if m[1] == "CSV" {
			return "FromCSV", fmt.Sprintf("typed.ReadCSVFromReader[%s](r)", inType)
		}
		return "FromTSV", fmt.Sprintf("typed.ReadDelimFromReader[%s](r)", inType)
	}
	m := recordReadRe.FindStringSubmatch(srcCode + "\n")
	if m == nil {
		return "", ""
	}
	cfg := strings.TrimSpace(strings.TrimPrefix(m[2], ","))
	if m[1] == "CSV" {
		if cfg != "" {
			return "FromCSV", "ssql.ReadCSVFromReader(r, " + cfg + ")"
		}
		return "FromCSV", "ssql.ReadCSVFromReader(r)"
	}
	if cfg != "" {
		return "FromTSV", "ssql.ReadTSVFromReaderWithConfig(r, " + cfg + ")"
	}
	return "FromTSV", "ssql.ReadTSVFromReader(r)"
}

// importUsed reports whether the emitted text references the package an
// import path brings in (`alias path` entries use the alias; a /vN
// major-version suffix is skipped).
func importUsed(imp, text string) bool {
	ident := imp
	if i := strings.IndexByte(imp, ' '); i >= 0 {
		ident = imp[:i]
	} else {
		parts := strings.Split(imp, "/")
		ident = parts[len(parts)-1]
		if len(parts) > 1 && regexp.MustCompile(`^v\d+$`).MatchString(ident) {
			ident = parts[len(parts)-2]
		}
	}
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(ident) + `\.`).MatchString(text)
}

func mergeBodyImports(body []*CodeFragment) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range body {
		for _, imp := range f.Imports {
			if imp == "" || seen[imp] {
				continue
			}
			seen[imp] = true
			out = append(out, imp)
		}
	}
	return out
}

// libraryKeptImports drops from a source's import list what its replaced
// code needed (the reader, the flag block, the process) and keeps what
// its struct definition may still reference (time for time.Time fields).
func libraryKeptImports(imports []string) []string {
	var kept []string
	for _, imp := range imports {
		switch imp {
		case "", "runtime", "fmt", "os", "flag", "github.com/rosscartlidge/ssql/v4/typed":
		default:
			kept = append(kept, imp)
		}
	}
	return kept
}
