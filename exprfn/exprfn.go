// Package exprfn holds tiny generic helpers called by Go code that ssql
// generates from expr-lang expressions (`-if-expr` / `-set-expr`). It exists
// so the generated code stays readable: only functions whose inline expansion
// would hurt readability live here — everything else is emitted as direct
// strings.*/math.* calls. Zero dependencies; everything inlinable.
package exprfn

import "unicode/utf8"

// Abs is expr-lang's type-preserving abs(): int stays int, float stays float.
func Abs[T int64 | float64](v T) T {
	if v < 0 {
		return -v
	}
	return v
}

// RuneLen is expr-lang's len() on strings: rune count, not bytes
// (len("héllo") == 5).
func RuneLen(s string) int64 {
	return int64(utf8.RuneCountInString(s))
}
