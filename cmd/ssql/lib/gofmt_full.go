//go:build !slim

package lib

import "go/format"

// gofmtSource hands library-mode output over gofmt-clean.
func gofmtSource(src []byte) ([]byte, error) { return format.Source(src) }
