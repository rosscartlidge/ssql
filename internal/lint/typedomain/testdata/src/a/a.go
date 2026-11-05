package a

import (
	"time"

	"github.com/rosscartlidge/ssql/v4"
)

func unannotated(v any) string {
	switch v.(type) { // want `type switch handles int64, time but declares no domain`
	case int64:
		return "int"
	case time.Time:
		return "time"
	}
	return ""
}

func partial(v any) int {
	// ssql:domain ordered
	switch v.(type) { // want `type switch declares ordered; missing inet`
	case int64, ssql.Int128, ssql.Decimal, float64, string, time.Time:
		return 1
	}
	return 0
}

func covered(v any) int {
	// ssql:domain numeric
	switch v.(type) {
	case int64, ssql.Int128, ssql.Decimal, float64:
		return 1
	}
	return 0
}

func union(v any) int {
	// ssql:domain wide+time
	switch v.(type) {
	case ssql.Int128, ssql.Decimal, time.Time:
		return 1
	}
	return 0
}

func noReason(v any) int {
	// ssql:domain fixed:
	switch v.(type) { // want `ssql:domain fixed needs a reason`
	case time.Time:
		return 1
	}
	return 0
}

func withReason(v any) int {
	// ssql:domain fixed: a time parse
	switch v.(type) {
	case time.Time:
		return 1
	}
	return 0
}

func unknownDomain(v any) int {
	// ssql:domain numerik
	switch v.(type) { // want `"numerik" is no domain or type`
	case ssql.Int128:
		return 1
	}
	return 0
}

func fieldType(ft ssql.FieldType) string {
	// ssql:domain scalar
	switch ft { // want `FieldType switch declares scalar; missing decimal, inet`
	case ssql.FieldTypeInt, ssql.FieldTypeInt128, ssql.FieldTypeFloat, ssql.FieldTypeString, ssql.FieldTypeTime, ssql.FieldTypeBool:
		return "x"
	}
	return ""
}

func fieldTypeDecimalFirst(ft ssql.FieldType) string {
	if ft.IsDecimal() {
		return "decimal"
	}
	// ssql:domain scalar
	switch ft {
	case ssql.FieldTypeInt, ssql.FieldTypeInt128, ssql.FieldTypeFloat, ssql.FieldTypeString, ssql.FieldTypeTime, ssql.FieldTypeBool, ssql.FieldTypeInet:
		return "x"
	}
	return ""
}

// ssql:domain exact
var setters = map[string]string{ // want `type-name table declares exact; missing decimal`
	"int64":       "Int",
	"ssql.Int128": "Int128",
}

func goTypeSwitch(goType string) string {
	// ssql:domain numeric
	switch goType {
	case "int64", "ssql.Int128", "ssql.Decimal", "float64":
		return "n"
	}
	return ""
}

func wireNames(t string) string {
	switch t { // want `type-name switch handles int128, inet but declares no domain`
	case "int128":
		return "HUGEINT"
	case "inet":
		return "INET"
	}
	return ""
}

func notATypeTable(t string) string {
	switch t { // "time" alone does not make a type table
	case "time", "date":
		return "t"
	}
	return ""
}

func plainSwitch(v any) int {
	switch v.(type) { // basic types alone name ordinary code
	case int64, string:
		return 1
	}
	return 0
}
