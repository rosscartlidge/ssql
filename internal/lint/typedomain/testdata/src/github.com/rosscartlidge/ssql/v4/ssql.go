// Package ssql is a stand-in for the root package: its struct scalars and
// FieldType, by the names the lint knows.
package ssql

type Int128 struct{ hi, lo uint64 }

type Decimal struct{ units Int128 }

type Inet struct{ s string }

type FieldType int

const (
	FieldTypeAuto FieldType = iota
	FieldTypeString
	FieldTypeInt
	FieldTypeFloat
	FieldTypeBool
	FieldTypeTime
	FieldTypeInt128
	FieldTypeInet
)

func (ft FieldType) IsDecimal() bool { return ft&256 != 0 }
