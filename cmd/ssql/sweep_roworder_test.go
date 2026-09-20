package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestRowOrderSweep is DFC133 instrument 1: a metamorphic sweep that needs
// no second implementation. For every pipeline in the equivalence corpus
// whose result does not depend on input order BY DEFINITION, the output —
// as a multiset of rows, plus the header's field set and types — must be
// identical whichever input row comes first. The largest class of defects
// found in the week of DFC128 was exactly a dependence on the first record
// (a nullable column dropped, a type locked by a first-row null, `where`
// validating against record one), and no hand-written case had looked for
// it. Opt-in: SSQL_SWEEP=1. Each finding is promoted to a permanent
// equivalence case; this gate is the search, the corpus is the memory.
func TestRowOrderSweep(t *testing.T) {
	if os.Getenv("SSQL_SWEEP") == "" {
		t.Skip("opt-in: SSQL_SWEEP=1 (DFC133)")
	}
	bin := corpusBin(t)
	data := corpusData(t)

	var ran, skipped, findings int
	for _, c := range equivCases {
		if why := rowOrderDependent(c.Pipeline); why != "" {
			skipped++
			continue
		}
		m := sweepSourceRe.FindStringSubmatch(c.Pipeline)
		if m == nil {
			skipped++
			continue
		}
		src := filepath.Join(data, m[1])
		rows, render, ok := sweepSplit(src)
		if !ok || len(rows) < 2 {
			skipped++
			continue
		}
		ran++
		c := c
		t.Run(c.Name, func(t *testing.T) {
			dir := t.TempDir()
			// The reference is the ORIGINAL order, augmented with the
			// adversarial rows when the pipeline tolerates them.
			base := rows
			if aug := sweepAugment(src, rows); aug != nil {
				if _, _, err := sweepRun(t, bin, data, dir, c.Pipeline, m[1], render(aug), "ref-aug"); err == nil {
					base = aug
				}
			}
			refRows, refHdr, err := sweepRun(t, bin, data, dir, c.Pipeline, m[1], render(base), "ref")
			if err != nil {
				t.Skipf("reference run fails (%v) — not a row-order question", err)
			}
			check := func(what, pipeline, srcName string, render func([]string) []byte, refRows []string, refHdr string) {
				for name, perm := range sweepPermutations(base) {
					gotRows, gotHdr, err := sweepRun(t, bin, data, dir, pipeline, srcName, render(perm), what+name)
					switch {
					case err != nil:
						findings++
						t.Errorf("ROW-ORDER: %s%s fails after %s, succeeds in the original order: %v", c.Name, what, name, err)
					case !slices.Equal(gotRows, refRows):
						findings++
						t.Errorf("ROW-ORDER: %s%s differs after %s\n  original: %s\n  permuted: %s", c.Name, what, name, strings.Join(refRows, " "), strings.Join(gotRows, " "))
					case gotHdr != refHdr:
						findings++
						t.Errorf("HEADER: %s%s header differs after %s\n  original: %s\n  permuted: %s", c.Name, what, name, refHdr, gotHdr)
					}
				}
			}
			check("", c.Pipeline, m[1], render, refRows, refHdr)

			// The same rows as JSON Lines and as one JSON array, empty cells
			// as explicit nulls: the first-record defects were per reader.
			// Each format is its own reference — a JSON source may type or
			// order columns differently from CSV; what must hold is that
			// the answer does not depend on the row order WITHIN a format.
			if strings.HasSuffix(strings.ToLower(m[1]), ".csv") {
				header := strings.Split(strings.SplitN(string(render(nil)), "\n", 2)[0], ",")
				for _, f := range []struct{ ext, verb string }{{".jsonl", "jsonl"}, {".json", "json"}} {
					name := strings.TrimSuffix(m[1], ".csv") + f.ext
					jr := func(rows []string) []byte { return sweepCSVRowsAsJSON(header, rows, f.ext == ".json") }
					pl := sweepSourceRe.ReplaceAllString(c.Pipeline, "from "+f.verb+" {{.data}}/"+name)
					rRows, rHdr, err := sweepRun(t, bin, data, dir, pl, name, jr(base), "ref"+f.ext)
					if err != nil {
						continue // this pipeline does not run on the JSON form at all (e.g. CSV-only flags)
					}
					check(" [as "+f.verb+"]", pl, name, jr, rRows, rHdr)
				}
			}
		})
	}
	t.Logf("row-order sweep: %d pipelines swept, %d skipped (order-dependent by definition or no permutable source), %d findings", ran, skipped, findings)
}

// sweepSourceRe finds the pipeline's FIRST source file in the corpus dir.
var sweepSourceRe = regexp.MustCompile(`from (?:csv |tsv |jsonl |json )?\{\{\.data\}\}/([A-Za-z0-9_.-]+)`)

// rowOrderDependent names the construct that makes a pipeline's result
// depend on input order BY DEFINITION (DFC133 §3) — these are skipped, not
// compared loosely.
func rowOrderDependent(pipeline string) string {
	for _, tok := range []string{
		" -first ", " -last ", " -any ", " -string-agg ", " -collect ", " -arg-max ", " -arg-min ", " -mode ",
		" sample", " -sample", " fill ", " window ", " -stream-expr", " -presorted", " offset ", " lines ",
		" limit ", " top ", " union ", " distinct", " first(", " last(", " -expr ", " tee ", " describe",
	} {
		if strings.Contains(pipeline+" ", tok) {
			return strings.TrimSpace(tok)
		}
	}
	return ""
}

// sweepSplit reads a fixture into its data rows and returns a renderer that
// puts any row sequence back into the file's format. CSV/TSV keep the
// header line; JSONL keeps a leading _schema line; a JSON array is
// re-marshalled element by element.
func sweepSplit(path string) (rows []string, render func([]string) []byte, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv":
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) < 2 || strings.Contains(string(raw), "\"\n") && strings.Count(string(raw), "\"")%2 != 0 {
			return nil, nil, false
		}
		header := lines[0]
		return lines[1:], func(r []string) []byte { return []byte(header + "\n" + strings.Join(r, "\n") + "\n") }, true
	case ".jsonl":
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		prefix := ""
		if len(lines) > 0 && strings.HasPrefix(lines[0], `{"_schema"`) {
			prefix, lines = lines[0]+"\n", lines[1:]
		}
		return lines, func(r []string) []byte { return []byte(prefix + strings.Join(r, "\n") + "\n") }, true
	case ".json":
		var elems []json.RawMessage
		if json.Unmarshal(raw, &elems) != nil {
			return nil, nil, false
		}
		for _, e := range elems {
			rows = append(rows, string(e))
		}
		return rows, func(r []string) []byte { return []byte("[" + strings.Join(r, ",") + "]") }, true
	}
	return nil, nil, false
}

// sweepAugment appends adversarial rows (DFC133 §3): for delimited files a
// row that keeps only its first cell, and a copy of row one whose integer
// cells became floats; for JSON a copy of row one with every value null.
// nil when the format has no safe augmentation.
func sweepAugment(path string, rows []string) []string {
	out := slices.Clone(rows)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv":
		sep := ","
		if strings.HasSuffix(strings.ToLower(path), ".tsv") {
			sep = "\t"
		}
		if strings.Contains(rows[0], `"`) {
			return nil
		}
		cells := strings.Split(rows[0], sep)
		empty := make([]string, len(cells))
		empty[0] = cells[0]
		floats := slices.Clone(cells)
		for i, c := range floats {
			if i > 0 && sweepIntRe.MatchString(c) {
				floats[i] = c + ".5"
			}
		}
		return append(out, strings.Join(empty, sep), strings.Join(floats, sep))
	case ".jsonl", ".json":
		var obj map[string]any
		if json.Unmarshal([]byte(rows[0]), &obj) != nil {
			return nil
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		first := true
		var b bytes.Buffer
		b.WriteByte('{')
		for _, k := range keys {
			if !first {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			if first {
				vb, _ := json.Marshal(obj[k]) // keep one value so the row is not empty
				b.WriteString(":" + string(vb))
			} else {
				b.WriteString(":null")
			}
			first = false
		}
		b.WriteByte('}')
		return append(out, b.String())
	}
	return nil
}

var sweepIntRe = regexp.MustCompile(`^-?[0-9]+$`)
var sweepNumRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// sweepCSVRowsAsJSON renders simple (unquoted) CSV rows as JSON objects:
// numbers and booleans bare, an empty cell as an explicit null.
func sweepCSVRowsAsJSON(header, rows []string, array bool) []byte {
	var objs []string
	for _, row := range rows {
		cells := strings.Split(row, ",")
		var parts []string
		for i, h := range header {
			v := ""
			if i < len(cells) {
				v = cells[i]
			}
			kb, _ := json.Marshal(h)
			switch {
			case v == "":
				parts = append(parts, string(kb)+":null")
			case sweepNumRe.MatchString(v), v == "true", v == "false":
				parts = append(parts, string(kb)+":"+v)
			default:
				vb, _ := json.Marshal(v)
				parts = append(parts, string(kb)+":"+string(vb))
			}
		}
		objs = append(objs, "{"+strings.Join(parts, ",")+"}")
	}
	if array {
		return []byte("[" + strings.Join(objs, ",") + "]")
	}
	return []byte(strings.Join(objs, "\n") + "\n")
}

// sweepPermutations: every row moved to the FRONT (the first-record class
// exactly; capped), the reversal, and one seeded shuffle.
func sweepPermutations(rows []string) map[string][]string {
	perms := map[string][]string{}
	n := len(rows)
	step := 1
	if n > 12 {
		step = n / 12
	}
	for i := 1; i < n; i += step {
		p := append([]string{rows[i]}, rows[:i]...)
		p = append(p, rows[i+1:]...)
		perms[fmt.Sprintf("row %d moved first", i+1)] = p
	}
	rev := slices.Clone(rows)
	slices.Reverse(rev)
	perms["reversal"] = rev
	sh := slices.Clone(rows)
	rand.New(rand.NewSource(133)).Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
	perms["shuffle(seed 133)"] = sh
	return perms
}

// sweepRun runs the pipeline with the named source replaced by content,
// returning the canonical row multiset and the header's field SET and
// types (field ORDER is reported by a separate check: JSON input has no
// column order to keep — a logged limitation, not this sweep's finding).
func sweepRun(t *testing.T, bin, data, dir, pipeline, srcName string, content []byte, tag string) ([]string, string, error) {
	t.Helper()
	variant := filepath.Join(dir, fmt.Sprintf("%x", []byte(tag))[:min(16, 2*len(tag))]+"_"+srcName)
	if err := os.WriteFile(variant, content, 0o644); err != nil {
		t.Fatal(err)
	}
	p := strings.Replace(pipeline, "{{.data}}/"+srcName, variant, 1)
	p = strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(p)
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+p)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	hdr := ""
	if first, _, _ := strings.Cut(stdout.String(), "\n"); strings.HasPrefix(first, `{"_schema"`) {
		var h struct {
			Schema struct {
				Fields []string          `json:"fields"`
				Types  map[string]string `json:"types"`
			} `json:"_schema"`
		}
		if json.Unmarshal([]byte(first), &h) == nil {
			fs := slices.Clone(h.Schema.Fields)
			sort.Strings(fs)
			parts := make([]string, len(fs))
			for i, f := range fs {
				parts[i] = f + ":" + h.Schema.Types[f]
			}
			hdr = strings.Join(parts, " ")
		}
	}
	recs := equivParse(t, "sweep", stdout.String())
	// Floating-point accumulation is order-dependent in the last place
	// (Welford variance, compensated sums): a documented effect, not a
	// finding. Compare floats to 12 significant digits.
	for _, r := range recs {
		for k, v := range r {
			if f, ok := v.(float64); ok {
				if g, err := strconv.ParseFloat(strconv.FormatFloat(f, 'g', 12, 64), 64); err == nil {
					r[k] = g
				}
			}
		}
	}
	return equivCanon(recs, false), hdr, nil
}
