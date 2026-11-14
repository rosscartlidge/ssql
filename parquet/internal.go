package parquet

import (
	"reflect"
	"time"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/typed"
)

// The struct field types the typed reader and writer give their own
// encodings (the typed package keeps the same four for its CSV readers).
var (
	timeType    = reflect.TypeOf(time.Time{})
	int128Type  = reflect.TypeOf(ssql.Int128{})
	decimalType = reflect.TypeOf(ssql.Decimal{})
	inetType    = reflect.TypeOf(ssql.Inet{})
)

// failRead is the typed readers' single exit for a fatal condition: the
// fail-fast *typed.ReadError every typed reader panics with (a
// generated program's main reports it; the *Safe readers yield it).
func failRead(op, source string, err error) {
	panic(&typed.ReadError{Op: op, Source: source, Err: err})
}
