package typedomain

import (
	"go/types"
	"slices"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "a")
}

// TestScalarDomainIsTheValueConstraint: the `scalar` domain is the scalar
// terms of ssql.Value, read from the root package itself. A new scalar
// type added to Value without a member here fails this test; added here,
// `make vet` names every switch and table that must handle it (DFC163).
func TestScalarDomainIsTheValueConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the root package")
	}
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedTypes, Dir: "../../.."}, ssqlPath)
	if err != nil || len(pkgs) != 1 || len(pkgs[0].Errors) > 0 {
		t.Fatalf("load %s: %v %v", ssqlPath, err, pkgs)
	}
	obj := pkgs[0].Types.Scope().Lookup("Value")
	if obj == nil {
		t.Fatal("ssql.Value not found")
	}
	var got []string
	var walk func(types.Type)
	walk = func(typ types.Type) {
		switch x := typ.(type) {
		case *types.Union:
			for i := range x.Len() {
				walk(x.Term(i).Type())
			}
		case *types.Interface:
			for i := range x.NumEmbeddeds() {
				walk(x.EmbeddedType(i))
			}
		default:
			if m := memberOfType(typ); m != "" && !slices.Contains(got, m) {
				got = append(got, m)
			}
		}
	}
	walk(obj.Type().Underlying())
	want := domains["scalar"]
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ssql.Value's scalars are %v; the scalar domain is %v — add the new type to members and its domains", got, want)
	}
	// The struct scalars are the members whose Go form is not a basic type.
	for _, m := range want {
		basic := m == mInt64 || m == mFloat64 || m == mString || m == mBool
		if structScalars[m] == basic {
			t.Errorf("structScalars[%q] = %v", m, structScalars[m])
		}
	}
}
