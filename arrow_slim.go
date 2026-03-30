//go:build slim

package ssql

import (
	"fmt"
	"io"
	"iter"
)

func ReadArrow(filename string) (iter.Seq[Record], error) {
	return nil, fmt.Errorf("arrow support not available in slim build")
}

func ReadArrowFromReader(r io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {}
}

func WriteArrow(records iter.Seq[Record], filename string) error {
	return fmt.Errorf("arrow support not available in slim build")
}

func WriteArrowToWriter(records iter.Seq[Record], w io.Writer) error {
	return fmt.Errorf("arrow support not available in slim build")
}

func ExtractSignalFromArrow(filename string, field string) (Signal, error) {
	return Signal{}, fmt.Errorf("arrow support not available in slim build")
}

func ExtractSignalFromArrowReader(r io.Reader, field string) (Signal, error) {
	return Signal{}, fmt.Errorf("arrow support not available in slim build")
}
