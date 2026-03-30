//go:build slim

package ssql

import (
	"fmt"
	"iter"
)

type XLSXConfig struct {
	SheetName string
}

func DefaultXLSXConfig() XLSXConfig {
	return XLSXConfig{}
}

func ReadXLSX(filename string, config ...XLSXConfig) (iter.Seq[Record], error) {
	return nil, fmt.Errorf("xlsx support not available in slim build")
}

func WriteXLSX(records iter.Seq[Record], filename string, config ...XLSXConfig) error {
	return fmt.Errorf("xlsx support not available in slim build")
}

func ReadXLSXSheetNames(filename string) ([]string, error) {
	return nil, fmt.Errorf("xlsx support not available in slim build")
}
