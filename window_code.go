package ssql

import "fmt"

// WindowFuncCode renders the Go constructor call that rebuilds fn — the
// text `generate go` emits for a window stage. It lives with the types
// (DFC115: the command is the authority on itself) so codegen never has
// to reconstruct an unexported struct from its printed form: until
// v4.100.0 the CLI formatted the value with %v and parsed the text back,
// and NTILE's N came out as 0 in every generated program (found by the
// DFC130 unit-0 gate).
func WindowFuncCode(fn WindowFunc) string {
	switch f := fn.(type) {
	case wRowNumber:
		return "ssql.WRowNumber()"
	case wRank:
		return "ssql.WRank()"
	case wDenseRank:
		return "ssql.WDenseRank()"
	case wNtile:
		return fmt.Sprintf("ssql.WNtile(%d)", f.N)
	case wPercentRank:
		return "ssql.WPercentRank()"
	case wLag:
		return fmt.Sprintf("ssql.WLag(%q, %d)", f.Field, f.Offset)
	case wLead:
		return fmt.Sprintf("ssql.WLead(%q, %d)", f.Field, f.Offset)
	case wFirst:
		return fmt.Sprintf("ssql.WFirst(%q)", f.Field)
	case wLast:
		return fmt.Sprintf("ssql.WLast(%q)", f.Field)
	case wSum:
		return fmt.Sprintf("ssql.WSum(%q)", f.Field)
	case wAvg:
		return fmt.Sprintf("ssql.WAvg(%q)", f.Field)
	case wCount:
		return "ssql.WCount()"
	case wMin:
		return fmt.Sprintf("ssql.WMin(%q)", f.Field)
	case wMax:
		return fmt.Sprintf("ssql.WMax(%q)", f.Field)
	case wCumeDist:
		return "ssql.WCumeDist()"
	case wNthValue:
		return fmt.Sprintf("ssql.WNthValue(%q, %d)", f.Field, f.N)
	case wLagDefault:
		return fmt.Sprintf("ssql.WLagDefault(%q, %d, %s)", f.Field, f.Offset, goLiteral(f.Default))
	case wLeadDefault:
		return fmt.Sprintf("ssql.WLeadDefault(%q, %d, %s)", f.Field, f.Offset, goLiteral(f.Default))
	case wCountField:
		return fmt.Sprintf("ssql.WCountField(%q)", f.Field)
	}
	panic(fmt.Sprintf("ssql.WindowFuncCode: unknown window function %T", fn))
}

// WindowFuncField is the source field a window function reads ("" and
// false for the fieldless ranking/count functions).
func WindowFuncField(fn WindowFunc) (string, bool) {
	switch f := fn.(type) {
	case wLag:
		return f.Field, true
	case wLead:
		return f.Field, true
	case wFirst:
		return f.Field, true
	case wLast:
		return f.Field, true
	case wSum:
		return f.Field, true
	case wAvg:
		return f.Field, true
	case wMin:
		return f.Field, true
	case wMax:
		return f.Field, true
	case wNthValue:
		return f.Field, true
	case wLagDefault:
		return f.Field, true
	case wLeadDefault:
		return f.Field, true
	case wCountField:
		return f.Field, true
	}
	return "", false
}

// goLiteral renders a LAG/LEAD default as Go source: the canonical scalar
// types the CLI's value parser produces.
func goLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case string:
		return fmt.Sprintf("%q", x)
	case int64:
		return fmt.Sprintf("int64(%d)", x)
	case float64:
		return fmt.Sprintf("float64(%v)", x)
	case bool:
		return fmt.Sprintf("%t", x)
	}
	return fmt.Sprintf("%#v", v)
}

// WindowFuncResultKind is the wire type of a window function's result:
// "int" (row_number, rank, dense_rank, ntile, count), "float"
// (percent_rank, sum, avg), or "" when the result has the SOURCE FIELD's
// type (lag, lead, first, last, min, max).
func WindowFuncResultKind(fn WindowFunc) string {
	switch fn.(type) {
	case wRowNumber, wRank, wDenseRank, wNtile, wCount, wCountField:
		return "int"
	case wPercentRank, wSum, wAvg, wCumeDist:
		return "float"
	}
	return ""
}
