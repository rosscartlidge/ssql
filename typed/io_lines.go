package typed

import (
	"bufio"
	"io"
	"iter"
	"os"
)

// Line is the row type of `ssql from lines` in typed pipelines:
// 1-based line number and the text.
type Line struct {
	LineNumber int64  `ssql:"line_number"`
	Line       string `ssql:"line"`
}

// ReadLines yields one Line per text line of the file (1-based
// numbering, like sed/awk). Errors opening the file are reported on
// stderr by the caller's contract for sources; an unreadable file yields
// nothing. Serial: line boundaries are sequential.
func ReadLines(filename string) iter.Seq[Line] {
	return func(yield func(Line) bool) {
		f, err := os.Open(filename)
		if err != nil {
			return
		}
		defer f.Close()
		for l := range ReadLinesFromReader(f) {
			if !yield(l) {
				return
			}
		}
	}
}

// ReadLinesFromReader is ReadLines over any reader (stdin).
func ReadLinesFromReader(r io.Reader) iter.Seq[Line] {
	return func(yield func(Line) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		var n int64
		for sc.Scan() {
			n++
			if !yield(Line{LineNumber: n, Line: sc.Text()}) {
				return
			}
		}
	}
}
