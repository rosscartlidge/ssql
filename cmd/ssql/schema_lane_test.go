package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestSchemaModeMatchesExec is the schema lane (DFC170): schema mode is a
// prediction of the header exec writes, so it is held to it over every
// TestPipelineEquivalence case. Each pipeline runs twice: in exec, keeping
// the _schema header its last stage writes, and under SSQL_MODE=schema,
// whose last stage writes the predicted header. Names must agree in order;
// a type both sides know must agree; a predicted "any" where exec knows
// the type is a gap; no header (unknown) is allowed only after a stage
// whose columns come from the data.
//
// Until 2026-11-15 schema mode had no such gate. DFC096 §9 designed it in
// May; in November `group-by department` passed silently in schema mode
// while exec failed, and an undeterminable pivot made the next stage
// report a false unknown field.
func TestSchemaModeMatchesExec(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every equivalence pipeline twice")
	}
	bin := corpusBin(t)
	data := corpusData(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(data)))
	t.Cleanup(srv.Close)
	repl := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data, "{{.url}}", srv.URL)
	var mu sync.Mutex
	var gaps []string
	t.Cleanup(func() {
		// The "any" gaps are a ratchet, not a list: 300-odd columns across
		// 160 cases on 2026-11-15, closed command by command (DFC170 §4
		// step 6). The count may only fall; lower the ceiling with each fix.
		// The one way up: a case leaving schemaKnown, whose names now
		// agree, starts counting its columns (a strict failure counts
		// none).
		if t.Failed() {
			return
		}
		slices.Sort(gaps)
		if len(gaps) > schemaAnyCeiling {
			t.Errorf("%d columns predicted \"any\" where exec knows the type, ceiling %d: a fix lost a type\n  %s",
				len(gaps), schemaAnyCeiling, strings.Join(gaps, "\n  "))
		} else if len(gaps) < schemaAnyCeiling {
			// A log, not a failure: a case skipped because exec failed
			// once counts no gaps, and a hard floor made that a flake.
			t.Logf("%d columns predicted \"any\", ceiling %d: lower schemaAnyCeiling to %d", len(gaps), schemaAnyCeiling, len(gaps))
		}
	})
	for _, c := range equivCases {
		c := c
		env := []string{"TZ=UTC"}
		for _, e := range c.Env {
			env = append(env, repl.Replace(e))
		}
		pipeline := repl.Replace(c.Pipeline)
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			got, gotErr := schemaLaneHeader(pipeline, append(slices.Clone(env), "SSQL_MODE=schema"))
			want, wantErr := schemaLaneHeader(pipeline, env)
			if wantErr != nil {
				t.Skipf("exec fails, nothing to predict: %v", wantErr)
			}
			d, anyGaps := schemaLaneDiff(c.Pipeline, got, gotErr, want)
			if reason, ok := schemaKnown[c.Name]; ok {
				if d == "" {
					t.Errorf("listed in schemaKnown (%s) but schema mode now agrees: delete the entry", reason)
				}
				t.Skipf("known: %s", reason)
			}
			if d != "" {
				t.Errorf("%s\n  pipeline: %s", d, pipeline)
			}
			mu.Lock()
			for _, g := range anyGaps {
				gaps = append(gaps, c.Name+": "+g)
			}
			mu.Unlock()
		})
	}
}

// schemaAnyCeiling is the number of columns schema mode still predicts
// "any" where exec knows the type (see the Cleanup above).
const schemaAnyCeiling = 62

// schemaKnown lists the cases whose schema-mode answer is known to differ
// from exec, with the reason; each entry is in doc/research/TODO.md and
// leaves with its fix. A listed case that starts agreeing fails, so the
// list cannot outlive its bugs.
var schemaKnown = map[string]string{}

// schemaHeader is a parsed _schema line; nil means no header (unknown).
type schemaHeader struct {
	Fields []string          `json:"fields"`
	Types  map[string]string `json:"types"`
}

// schemaLaneHeader runs the pipeline and returns the first _schema header
// its output carries (nil when there is none).
func schemaLaneHeader(pipeline string, env []string) (*schemaHeader, error) {
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+pipeline)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `{"_schema"`) {
			continue
		}
		var h struct {
			Schema schemaHeader `json:"_schema"`
		}
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			return nil, fmt.Errorf("bad header %s: %v", line, err)
		}
		return &h.Schema, nil
	}
	return nil, nil
}

// schemaUndetermined names the stages whose output columns come from the
// data, after which schema mode may answer "unknown" (no header).
var schemaUndetermined = []string{" pivot "}

// schemaLaneDiff describes how the prediction differs from exec ("" when
// it agrees), and lists the columns predicted "any" whose type exec knows.
func schemaLaneDiff(pipeline string, got *schemaHeader, gotErr error, want *schemaHeader) (string, []string) {
	if gotErr != nil {
		return fmt.Sprintf("schema mode failed where exec ran: %v", gotErr), nil
	}
	if want == nil {
		return "", nil // exec wrote no header: nothing to hold the prediction to
	}
	if got == nil {
		for _, s := range schemaUndetermined {
			if strings.Contains(pipeline+" ", s) {
				return "", nil
			}
		}
		return fmt.Sprintf("schema mode wrote no header; exec wrote %v", want.Fields), nil
	}
	// Schema mode lists a nested value's paths (addr.city) beside the
	// column for completion; exec's header has the column only.
	have := map[string]bool{}
	for _, f := range want.Fields {
		have[f] = true
	}
	var names []string
	for _, f := range got.Fields {
		if !have[f] {
			if i := strings.IndexByte(f, '.'); i > 0 && have[f[:i]] {
				continue
			}
		}
		names = append(names, f)
	}
	if !slices.Equal(names, want.Fields) {
		return fmt.Sprintf("fields: schema mode %v, exec %v", names, want.Fields), nil
	}
	var diffs, anyGaps []string
	for _, f := range want.Fields {
		w, g := want.Types[f], got.Types[f]
		switch {
		case w == "" || w == "any" || g == w:
		case g == "" || g == "any":
			anyGaps = append(anyGaps, fmt.Sprintf("%s (exec %s)", f, w))
		default:
			diffs = append(diffs, fmt.Sprintf("%s: schema mode %q, exec %q", f, g, w))
		}
	}
	if len(diffs) > 0 {
		return "types: " + strings.Join(diffs, "; "), anyGaps
	}
	return "", anyGaps
}
