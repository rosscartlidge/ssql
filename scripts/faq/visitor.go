package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// A visitor's own data (DFC169 §9.3, Ross 2026-11-16: paste, no upload):
// the column names, or a CSV header with a few rows, pasted beside the
// question. It becomes data.csv in a directory of its own, so the answer is
// shaped against the visitor's real columns and checked by schema mode
// (unknown fields come back with the list of real ones, DFC170); pasted
// rows are run too, in the same jail as the sample data.

const (
	maxPaste   = 20 << 10
	maxColumns = 200
	maxRows    = 200
)

type visitor struct {
	dir  string   // holds data.csv
	cols []string // its header
	rows int      // data rows pasted (0: names only)
}

// parseVisitor reads what was pasted: a CSV header line (commas or tabs),
// optionally followed by rows, or one column name per line.
func parseVisitor(text string) ([][]string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil, fmt.Errorf("nothing pasted")
	}
	if len(text) > maxPaste {
		return nil, fmt.Errorf("the pasted data is over %d KB; paste the header and a few rows", maxPaste>>10)
	}
	lines := strings.Split(text, "\n")
	first := lines[0]
	var recs [][]string
	switch {
	case strings.Contains(first, "\t"):
		r := csv.NewReader(strings.NewReader(text))
		r.Comma, r.FieldsPerRecord = '\t', -1
		var err error
		if recs, err = r.ReadAll(); err != nil {
			return nil, fmt.Errorf("reading the pasted rows: %v", err)
		}
	case strings.Contains(first, ","):
		r := csv.NewReader(strings.NewReader(text))
		r.FieldsPerRecord = -1
		var err error
		if recs, err = r.ReadAll(); err != nil {
			return nil, fmt.Errorf("reading the pasted rows: %v", err)
		}
	default: // one name per line
		var names []string
		for _, ln := range lines {
			if ln = strings.TrimSpace(ln); ln != "" {
				names = append(names, ln)
			}
		}
		recs = [][]string{names}
	}
	header := recs[0]
	if len(recs) == 1 && len(header) <= 2 {
		for _, c := range header {
			if looksLikeSentence(c) {
				return nil, fmt.Errorf("%q looks like a question, not a column name: ask it in the question box (the FAQ keeps the conversation), and paste only your data's column names here", strings.TrimSpace(c))
			}
		}
	}
	if len(header) > maxColumns {
		return nil, fmt.Errorf("%d columns; at most %d", len(header), maxColumns)
	}
	seen := map[string]bool{}
	for i, c := range header {
		c = strings.TrimSpace(c)
		header[i] = c
		if c == "" {
			return nil, fmt.Errorf("column %d has no name", i+1)
		}
		if strings.IndexFunc(c, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("column %q has a control character", c)
		}
		if seen[c] {
			return nil, fmt.Errorf("column %q appears twice", c)
		}
		seen[c] = true
	}
	if len(recs)-1 > maxRows {
		return nil, fmt.Errorf("%d rows; paste at most %d", len(recs)-1, maxRows)
	}
	for i, r := range recs[1:] {
		if len(r) != len(header) {
			return nil, fmt.Errorf("row %d has %d values, the header %d", i+1, len(r), len(header))
		}
	}
	return recs, nil
}

// looksLikeSentence: a "column" that ends in a question mark, or runs to
// five words or more. Ross typed a follow-up into the paste box, and the
// FAQ built a pipeline over a column named "How does it do the
// conversion" (2026-11-16). Real names that long are rare, and a header
// with more than two columns is never refused this way.
func looksLikeSentence(c string) bool {
	c = strings.TrimSpace(c)
	return strings.HasSuffix(c, "?") || len(strings.Fields(c)) >= 5
}

// newVisitor writes the pasted data as data.csv in a new directory under
// parent, beside a copy of the sample files (an example over employees.csv
// still runs). The caller removes v.dir.
func newVisitor(parent, samples, text string) (*visitor, error) {
	recs, err := parseVisitor(text)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "faq-visitor-")
	if err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(samples, "*"))
	for _, sf := range files {
		if b, err := os.ReadFile(sf); err == nil {
			os.WriteFile(filepath.Join(dir, filepath.Base(sf)), b, 0o644)
		}
	}
	f, err := os.Create(filepath.Join(dir, "data.csv"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	w := csv.NewWriter(f)
	w.WriteAll(recs)
	if err := f.Close(); err != nil || w.Error() != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("writing data.csv: %v", err)
	}
	return &visitor{dir: dir, cols: recs[0], rows: len(recs) - 1}, nil
}

// prompt tells the model about the visitor's file: context for a question
// about their data, not an order to write a pipeline. "Can I turn a
// pipeline into a Go program?" asked with columns pasted was answered with
// a pipeline over them until 2026-11-16, never with the answer.
func (v *visitor) prompt() string {
	rows := "only the column names were given"
	if v.rows > 0 {
		rows = fmt.Sprintf("with %d sample rows", v.rows)
	}
	return fmt.Sprintf("The visitor has their own data: `data.csv`, with the columns %s (%s). "+
		"When the question asks for something done with their data, answer with a pipeline that reads data.csv. "+
		"When it asks how ssql works, answer that; the columns are only context.",
		strings.Join(v.cols, ", "), rows)
}

// readBy reports whether a pipeline reads the visitor's file.
func (v *visitor) readBy(pipeline string) bool {
	return strings.Contains(pipeline, "data.csv")
}
