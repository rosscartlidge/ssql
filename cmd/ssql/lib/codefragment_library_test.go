package lib

import (
	"strings"
	"testing"
)

// Library mode (DFC142 §5a shape 2): the same fragments that make a
// package-main program make an importable function when
// AssembleOptions.Package is set. These are string-level checks of the
// skeleton; cmd/ssql/library_mode_test.go compiles and runs the result.

func libraryRecordFragments() []*CodeFragment {
	src := NewInitFragment("records", "records, err := ssql.ReadCSV(*flagInput)\n\tif err != nil {\n\t\treturn fmt.Errorf(\"reading CSV: %w\", err)\n\t}", []string{"fmt", "os"}, "ssql from people.csv")
	src.Params = []CodeParam{{Name: "input", Default: "people.csv", Help: "input CSV file", VarName: "flagInput"}}
	where := NewStmtFragment("filtered", "records", "var exprFilter1 = runtime.MustCompileExprFilter(\"age > min\")\n\tfiltered := ssql.Where(exprFilter1(*flagParamMin))(records)", []string{"github.com/rosscartlidge/ssql/v4/cmd/ssql/runtime"}, "ssql where -param min int 30 -if-expr 'age > min'")
	where.Params = []CodeParam{{Name: "param-min", Default: "30", Help: "min", VarName: "flagParamMin", Type: "int"}}
	sink := NewFinalFragment("filtered", "if err := ssql.WriteCSV(filtered, *flagOutput); err != nil {\n\t\tfmt.Fprintf(os.Stderr, \"write: %v\\n\", err)\n\t\tos.Exit(1)\n\t}", []string{"fmt", "os"}, "ssql to csv out.csv")
	sink.Params = []CodeParam{{Name: "output", Default: "out.csv", Help: "output CSV file", VarName: "flagOutput"}}
	return []*CodeFragment{src, where, sink}
}

func TestLibraryRecordSkeleton(t *testing.T) {
	code, err := assembleLibrary(libraryRecordFragments(), AssembleOptions{Package: "reports", Func: "Adults"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package reports\n",
		"func Adults(in iter.Seq[ssql.Record], p AdultsParams) iter.Seq2[ssql.Record, error] {",
		"type AdultsParams struct {\n\tMin int // -param-min 30\n}",
		"func AdultsDefaults() AdultsParams {\n\treturn AdultsParams{Min: 30}",
		"flagParamMin := &p.Min",
		"return ssql.Safely(func(records iter.Seq[ssql.Record]) iter.Seq[ssql.Record] {",
		"filtered := ssql.Where(exprFilter1(*flagParamMin))(records)",
		"return filtered\n",
		"})(ssql.Safe(in))",
		"var exprFilter1 = runtime.MustCompileExprFilter(\"age > min\")\n",
		"func AdultsFromCSV(r io.Reader, p AdultsParams) iter.Seq2[ssql.Record, error] {\n\treturn Adults(ssql.ReadCSVFromReader(r), p)",
		"The sink `ssql to csv out.csv` is dropped",
		"\t\"io\"\n",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("library lacks %q:\n%s", want, code)
		}
	}
	for _, bad := range []string{"package main", "func main(", "flag.", "os.Exit(", "flagOutput", "flagInput", "Output string", "Input string", "\t\"os\"\n", "\t\"fmt\"\n"} {
		if strings.Contains(code, bad) {
			t.Errorf("library contains %q:\n%s", bad, code)
		}
	}
}

func TestLibraryTypedSkeletonPrefixesTypes(t *testing.T) {
	schema := &TypedSchema{TypeName: "PeopleRow", Fields: []TypedSchemaField{{Name: "name", GoName: "Name", GoType: "string"}, {Name: "age", GoName: "Age", GoType: "int64"}}}
	src := NewInitFragment("records", "records := typed.ReadCSVParallel[PeopleRow](*flagInput, runtime.GOMAXPROCS(0))", []string{"github.com/rosscartlidge/ssql/v4/typed", "runtime"}, "ssql from people.csv")
	src.Params = []CodeParam{{Name: "input", Default: "people.csv", VarName: "flagInput"}}
	src.OutputTypedSchema = schema
	src.StructDefs = []string{RenderStructDef(schema)}
	src.IsStream = true
	src.Capabilities = &Capabilities{Accepts: ShapeNone, Produces: ShapeStream}
	src.AltCodeIfSeq = "records := typed.ReadCSV[PeopleRow](*flagInput)"
	src.AltImportsIfSeq = []string{"github.com/rosscartlidge/ssql/v4/typed"}
	src.AltCapabilitiesIfSeq = &Capabilities{Accepts: ShapeNone, Produces: ShapeSeqTyped}
	where := NewStmtFragment("filtered", "records", "filtered := records.Where(func(r PeopleRow) bool { return r.Age > int64(*flagLimit) })", []string{"github.com/rosscartlidge/ssql/v4/typed"}, "ssql where -if age gt 30")
	where.Params = []CodeParam{{Name: "limit", Default: "30", VarName: "flagLimit", Type: "int"}}
	where.InputTypedSchema, where.OutputTypedSchema = schema, schema
	where.Capabilities = &Capabilities{Accepts: ShapeStream, Produces: ShapeStream}
	where.AltCodeIfSeq = "filtered := typed.Where(func(r PeopleRow) bool { return r.Age > int64(*flagLimit) })(records)"
	where.AltCapabilitiesIfSeq = &Capabilities{Accepts: ShapeSeqTyped, Produces: ShapeSeqTyped}
	sink := NewFinalFragment("filtered", "if err := filtered.WriteCSVToWriter(os.Stdout); err != nil {\n\t\tos.Exit(1)\n\t}", []string{"os"}, "ssql to csv")
	sink.InputTypedSchema = schema
	sink.Capabilities = &Capabilities{Accepts: ShapeStream, Produces: ShapeNone}
	sink.AltCodeIfSeq = "if err := typed.WriteCSVToWriter(filtered, os.Stdout); err != nil {\n\t\tos.Exit(1)\n\t}"
	sink.AltCapabilitiesIfSeq = &Capabilities{Accepts: ShapeSeqTyped, Produces: ShapeNone}

	code, err := assembleLibrary([]*CodeFragment{src, where, sink}, AssembleOptions{Package: "reports"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type PipelinePeopleRow struct {",
		"func Pipeline(in iter.Seq[PipelinePeopleRow], p PipelineParams) iter.Seq2[PipelinePeopleRow, error] {",
		"type PipelineParams struct {\n\tLimit int // -limit 30\n}",
		// the serial plan: the Stream form is swapped for its iter.Seq alternative
		"filtered := typed.Where(func(r PipelinePeopleRow) bool { return r.Age > int64(*flagLimit) })(records)",
		"func PipelineFromCSV(r io.Reader, p PipelineParams) iter.Seq2[PipelinePeopleRow, error] {\n\treturn Pipeline(typed.ReadCSVFromReader[PipelinePeopleRow](r), p)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("library lacks %q:\n%s", want, code)
		}
	}
	for _, bad := range []string{"records.Where(", ".Serial()", "runtime.GOMAXPROCS", "\t\"runtime\"\n", "\t\"os\"\n", "flagInput", "WriteCSV"} {
		if strings.Contains(code, bad) {
			t.Errorf("library contains %q:\n%s", bad, code)
		}
	}
	if strings.Contains(code, " PeopleRow") || strings.Contains(code, "[PeopleRow]") {
		t.Errorf("an unprefixed PeopleRow survived:\n%s", code)
	}
}

func TestLibraryRefusals(t *testing.T) {
	frags := libraryRecordFragments()
	if _, err := assembleLibrary(frags, AssembleOptions{Package: "main"}); err == nil || !strings.Contains(err.Error(), "package name") {
		t.Errorf("package main accepted: %v", err)
	}
	if _, err := assembleLibrary(frags, AssembleOptions{Package: "reports", Func: "adults"}); err == nil || !strings.Contains(err.Error(), "exported") {
		t.Errorf("unexported func accepted: %v", err)
	}
	two := append(libraryRecordFragments(), NewInitFragment("unionFile1", "unionFile1, err := ssql.ReadCSV(\"b.csv\")", nil, "ssql union b.csv"))
	if _, err := assembleLibrary(two, AssembleOptions{Package: "reports"}); err == nil || !strings.Contains(err.Error(), "several sources") {
		t.Errorf("two sources accepted: %v", err)
	}
}

func TestLibraryStmtAdaptsErrorReturns(t *testing.T) {
	in := "var exprX = runtime.MustCompileExpr(\"a\")\n\tout, err := ssql.ResampleRecords(records, cfg)\n\tif err != nil {\n\t\treturn fmt.Errorf(\"resample: %w\", err)\n\t}\n\tif bad {\n\t\treturn err\n\t}\n\tf := func(yield func(int) bool) {\n\t\tif !yield(1) {\n\t\t\treturn\n\t\t}\n\t}"
	got := libraryStmt(in, nil)
	for _, want := range []string{"panic(fmt.Errorf(\"resample: %w\", err))", "\t\tpanic(err)\n", "\t\t\treturn\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("libraryStmt lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"return fmt.Errorf", "return err\n", "MustCompileExpr"} {
		if strings.Contains(got, bad) {
			t.Errorf("libraryStmt kept %q:\n%s", bad, got)
		}
	}
}
