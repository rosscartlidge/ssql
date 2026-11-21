package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The eval (DFC169 §6) grades by what ssql does, not by how a pipeline is
// spelled: many pipelines are right. Three kinds of case:
//
//   - pipeline: the answer's pipeline must run, and its rows must equal the
//     golden pipeline's (each written and checked by hand) as a multiset of
//     rows, each row a multiset of values: column NAMES are the answer's
//     own business (n or count), and a column too many or too few, or a
//     wrong value, differs. Ordered cases compare the row order too.
//   - doc: every fact (a lower-case phrase) must appear in the reply;
//     {{latest}} is the latest release from the CHANGELOG.
//   - refuse: the reply must start "UNSUPPORTED:", and no pipeline it
//     offers may use an invented spelling listed (prose may name one to
//     say it does not exist).

type evalCase struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"` // pipeline | doc | refuse
	Question string   `json:"question"`
	Golden   string   `json:"golden,omitempty"`
	Ordered  bool     `json:"ordered,omitempty"`
	Facts    []string `json:"facts,omitempty"`
	Forbid   []string `json:"forbid,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type evalResult struct {
	Case    evalCase
	Answer  answer
	Pass    bool
	Why     string
	Elapsed time.Duration
}

func loadCases(path string) ([]evalCase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []evalCase
	if err := json.Unmarshal(b, &cases); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	ids := map[string]bool{}
	for _, c := range cases {
		if ids[c.ID] {
			return nil, fmt.Errorf("%s: case %q twice", path, c.ID)
		}
		ids[c.ID] = true
	}
	return cases, nil
}

// checkGoldens runs every golden pipeline, so a broken golden fails loudly
// before any model is graded against it.
func checkGoldens(box sandbox, cases []evalCase) error {
	for _, c := range cases {
		if c.Kind != "pipeline" {
			continue
		}
		if _, err := validatePipeline(c.Golden); err != nil {
			return fmt.Errorf("case %s: golden: %v", c.ID, err)
		}
		r := box.run(dataFor(c.Golden))
		if r.err != nil {
			return fmt.Errorf("case %s: golden fails: %v: %s", c.ID, r.err, r.stderr)
		}
		if strings.TrimSpace(r.stdout) == "" {
			return fmt.Errorf("case %s: golden produces no rows", c.ID)
		}
	}
	return nil
}

// grade is pass, rows right (pass, or a pipeline whose rows hold the
// golden's values with extra columns), and why it failed.
func grade(f *faq, c evalCase, a answer) (pass, rows bool, why string) {
	pass, why = gradeAnswer(f, c, a)
	if pass {
		return true, true, ""
	}
	if c.Kind == "pipeline" && a.Ran && !a.Illustrative && !a.Unsupported {
		got := f.box.run(dataFor(runnable(a.Pipeline)))
		want := f.box.run(dataFor(c.Golden))
		if got.err == nil && want.err == nil && rowsHold(canonRows(got.stdout, c.Ordered), canonRows(want.stdout, c.Ordered), c.Ordered) {
			return false, true, "rows right, shape missed: " + why
		}
	}
	return false, false, why
}

func gradeAnswer(f *faq, c evalCase, a answer) (bool, string) {
	reply := strings.ToLower(a.Reply)
	switch c.Kind {
	case "refuse":
		if !a.Unsupported {
			return false, "did not say UNSUPPORTED"
		}
		// An invented spelling counts in a pipeline the answer offers,
		// not in prose saying it does not exist ("ssql has no from pdf").
		code := strings.ToLower(strings.Join(fenced.FindAllString(a.Reply, -1), "\n"))
		for _, w := range c.Forbid {
			if strings.Contains(code, strings.ToLower(w)) {
				return false, "offered the invented " + w
			}
		}
		return true, ""
	case "doc":
		for _, fact := range c.Facts {
			if f != nil {
				fact = strings.ReplaceAll(fact, "{{latest}}", f.latest)
			}
			if !strings.Contains(reply, strings.ToLower(fact)) {
				return false, "missing fact: " + fact
			}
		}
		return true, ""
	}
	if a.Unsupported {
		return false, "said UNSUPPORTED"
	}
	if a.Pipeline == "" {
		return false, "no pipeline"
	}
	if a.Illustrative {
		return false, "named a file the sample data does not have"
	}
	if !a.Ran {
		return false, "never ran: " + firstLine(a.Failure)
	}
	got := f.box.run(dataFor(runnable(a.Pipeline)))
	want := f.box.run(dataFor(c.Golden))
	if got.err != nil {
		return false, "its rows could not be read: " + firstLine(got.stderr)
	}
	g, w := canonRows(got.stdout, c.Ordered), canonRows(want.stdout, c.Ordered)
	if !slices.Equal(g, w) {
		return false, fmt.Sprintf("rows differ: %d rows, want %d; first %s, want %s", len(g), len(w), first(g), first(w))
	}
	return true, ""
}

func first(rows []string) string {
	if len(rows) == 0 {
		return "(none)"
	}
	return rows[0]
}

// canonRows reads JSONL rows (or a bare value, `count`'s output) into
// canonical strings: each row its values sorted, numbers to 6 significant
// digits, the rows sorted unless the order is the answer.
func canonRows(out string, ordered bool) []string {
	var rows []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, `{"_schema"`) {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(ln), &m) != nil {
			rows = append(rows, canonValue(ln))
			continue
		}
		var vals []string
		for _, v := range m {
			vals = append(vals, canonValue(v))
		}
		sort.Strings(vals)
		rows = append(rows, strings.Join(vals, "\x1f"))
	}
	if !ordered {
		sort.Strings(rows)
	}
	return rows
}

func canonValue(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'g', 6, 64)
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil && !math.IsNaN(f) {
			return strconv.FormatFloat(f, 'g', 6, 64)
		}
		return x
	case nil:
		return "∅"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// rowsHold reports whether got has want's rows, each got row holding its
// want row's values and possibly more (a column not asked for). Ordered
// rows pair by position; otherwise each want row takes an unused got row.
func rowsHold(got, want []string, ordered bool) bool {
	if len(got) != len(want) {
		return false
	}
	holds := func(g, w string) bool {
		have := map[string]int{}
		for _, v := range strings.Split(g, "\x1f") {
			have[v]++
		}
		for _, v := range strings.Split(w, "\x1f") {
			if have[v] == 0 {
				return false
			}
			have[v]--
		}
		return true
	}
	used := make([]bool, len(got))
	for i, w := range want {
		if ordered {
			if !holds(got[i], w) {
				return false
			}
			continue
		}
		found := false
		for j, g := range got {
			if !used[j] && holds(g, w) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
