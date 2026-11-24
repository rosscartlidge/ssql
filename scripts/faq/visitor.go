package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	dir   string        // holds the visitor's files, beside the samples
	files []visitorFile // in the order pasted
}

// visitorFile is one pasted file: data.csv when nothing names it.
type visitorFile struct {
	name string
	cols []string // its header
	rows int      // data rows pasted (0: names only)
	// replaces: a sample file has this name, and the visitor's takes its place
	replaces bool
	csv      []byte // the file as written, for a playground workspace link
}

// A paste holds several files when it is split by lines naming them
// (Ross, 2026-11-24, "named sections"): `--- orders.csv` starts orders.csv.
// Text before the first such line is data.csv.
const maxFiles = 8

var (
	sectionLine = regexp.MustCompile(`^\s*-{3,}\s*([^\s-]\S*?)\s*-*\s*$`)
	fileName    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
)

// splitSections splits a paste at its `--- NAME` lines. A name without an
// extension gets .csv; any other extension is refused, since the file is
// written as CSV.
func splitSections(text string) (names, bodies []string, err error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil, nil, fmt.Errorf("nothing pasted")
	}
	if len(text) > maxPaste {
		return nil, nil, fmt.Errorf("the pasted data is over %d KB; paste the header and a few rows", maxPaste>>10)
	}
	name, body := "data.csv", []string{}
	flush := func() {
		if b := strings.TrimSpace(strings.Join(body, "\n")); b != "" || name != "data.csv" {
			names, bodies = append(names, name), append(bodies, b)
		}
	}
	seen := map[string]bool{}
	for _, ln := range strings.Split(text, "\n") {
		m := sectionLine.FindStringSubmatch(ln)
		if m == nil {
			body = append(body, ln)
			continue
		}
		flush()
		n := m[1]
		if !strings.Contains(n, ".") {
			n += ".csv"
		}
		if !fileName.MatchString(n) || strings.Contains(n, "..") || !strings.HasSuffix(n, ".csv") {
			return nil, nil, fmt.Errorf("%q cannot name a file: use letters, digits, _ - and . ending in .csv, as in --- orders.csv", m[1])
		}
		if seen[n] {
			return nil, nil, fmt.Errorf("%s is named twice", n)
		}
		seen[n] = true
		name, body = n, nil
	}
	flush()
	if len(names) > maxFiles {
		return nil, nil, fmt.Errorf("%d files; paste at most %d", len(names), maxFiles)
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("nothing pasted")
	}
	return names, bodies, nil
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

// newVisitor writes each pasted file (data.csv when the paste names none)
// in a new directory under parent, beside a copy of the sample files (an
// example over employees.csv still runs). A pasted file of a sample's name
// replaces it. The caller removes v.dir.
func newVisitor(parent, samples, text string) (*visitor, error) {
	names, bodies, err := splitSections(text)
	if err != nil {
		return nil, err
	}
	v := &visitor{}
	recs := make([][][]string, len(names))
	for i, b := range bodies {
		if recs[i], err = parseVisitor(b); err != nil {
			if len(names) > 1 || names[0] != "data.csv" {
				err = fmt.Errorf("%s: %v", names[i], err)
			}
			return nil, err
		}
		_, err := os.Stat(filepath.Join(samples, names[i]))
		v.files = append(v.files, visitorFile{name: names[i], cols: recs[i][0], rows: len(recs[i]) - 1, replaces: err == nil})
	}
	if v.dir, err = os.MkdirTemp(parent, "faq-visitor-"); err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(samples, "*"))
	for _, sf := range files {
		if b, err := os.ReadFile(sf); err == nil {
			os.WriteFile(filepath.Join(v.dir, filepath.Base(sf)), b, 0o644)
		}
	}
	for i := range v.files {
		vf := &v.files[i]
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		err := w.WriteAll(recs[i])
		if err == nil {
			err = os.WriteFile(filepath.Join(v.dir, vf.name), buf.Bytes(), 0o644)
		}
		if err != nil {
			os.RemoveAll(v.dir)
			return nil, fmt.Errorf("writing %s: %v", vf.name, err)
		}
		vf.csv = buf.Bytes()
	}
	return v, nil
}

// prompt tells the model about the visitor's files: context for a question
// about their data, not an order to write a pipeline. "Can I turn a
// pipeline into a Go program?" asked with columns pasted was answered with
// a pipeline over them until 2026-11-16, never with the answer.
func (v *visitor) prompt() string {
	var sb strings.Builder
	sb.WriteString("The visitor has their own data:")
	for _, f := range v.files {
		rows := "only the column names were given"
		if f.rows > 0 {
			rows = fmt.Sprintf("with %d sample rows", f.rows)
		}
		fmt.Fprintf(&sb, "\n- `%s`, with the columns %s (%s)", f.name, strings.Join(f.cols, ", "), rows)
		if f.replaces {
			sb.WriteString("; it replaces the sample file of that name")
		}
	}
	what := "their file"
	if len(v.files) > 1 {
		what = "their files"
	}
	fmt.Fprintf(&sb, "\nWhen the question asks for something done with their data, answer with a pipeline that reads %s. "+
		"When it asks how ssql works, answer that; the columns are only context.", what)
	return sb.String()
}

// names lists the visitor's file names, for the correction turn.
func (v *visitor) names() string {
	var ns []string
	for _, f := range v.files {
		ns = append(ns, f.name)
	}
	return strings.Join(ns, ", ")
}

// read lists the visitor's files a pipeline reads: a name not inside a
// longer one (mydata.csv is not data.csv).
func (v *visitor) read(pipeline string) []visitorFile {
	var out []visitorFile
	for _, f := range v.files {
		re := regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(f.name) + `($|[^A-Za-z0-9_.-])`)
		if re.MatchString(pipeline) {
			out = append(out, f)
		}
	}
	return out
}

// readBy reports whether a pipeline reads any of the visitor's files.
func (v *visitor) readBy(pipeline string) bool {
	return len(v.read(pipeline)) > 0
}

// columnsOnly reports whether a file the pipeline reads has no rows, so
// running it would answer from nothing: it is checked by schema mode only.
func (v *visitor) columnsOnly(pipeline string) bool {
	for _, f := range v.read(pipeline) {
		if f.rows == 0 {
			return true
		}
	}
	return false
}

// counts sums the pasted columns and rows, for the log.
func (v *visitor) counts() (files, cols, rows int) {
	for _, f := range v.files {
		cols += len(f.cols)
		rows += f.rows
	}
	return len(v.files), cols, rows
}
