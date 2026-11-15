package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/commands"
)

// TestSchemaOpsShadowSchemaMode: the hand-written schema ops (serve's
// completion walk and the generators' fragmentFields run them without
// running the command) must name the fields the command itself answers in
// schema mode (DFC170). For every equivalence pipeline without process
// substitution, each stage's op is given the names schema mode produced
// for the stages before it, and must predict the names schema mode
// produces after it. DFC096 §9 designed this shadow in May 2026; it was
// never built, and the ops drifted (update's -s/-e/-b, group-by's -e, the
// -arg form).
func TestSchemaOpsShadowSchemaMode(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every equivalence pipeline's prefixes")
	}
	bin := corpusBin(t)
	data := corpusData(t)
	repl := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data)
	for _, c := range equivCases {
		c := c
		if strings.Contains(c.Pipeline, "<(") || strings.Contains(c.Pipeline, "$(") || strings.Contains(c.Pipeline, "{{.url}}") {
			continue
		}
		stages, err := shadowStages(c.Pipeline)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		env := []string{"TZ=UTC", "SSQL_MODE=schema"}
		for _, e := range c.Env {
			env = append(env, repl.Replace(e))
		}
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			var in []string
			for i, st := range stages {
				prefix := repl.Replace(strings.Join(stages[:i+1], " | "))
				h, err := schemaLaneHeader(prefix, env)
				if err != nil || h == nil {
					return // schema mode refused or does not know: the lane's business
				}
				if i > 0 {
					argv, err := shadowArgv(st)
					if err != nil {
						t.Fatalf("stage %q: %v", st, err)
					}
					got, ok := commands.SchemaOpFields(argv, in)
					d := ""
					switch {
					case !ok:
						// an op may decline (another source's columns);
						// the walk then offers nothing, never a wrong list
					case !slices.Equal(got, h.Fields):
						d = fmt.Sprintf("op predicts %v; schema mode answers %v", got, h.Fields)
					}
					if d != "" {
						key := argv[0]
						if reason, known := schemaShadowKnown[key]; known {
							t.Logf("known (%s): %s: %s", key, reason, d)
						} else {
							t.Errorf("stage %q: %s\n  from %v", st, d, in)
						}
					}
				}
				in = h.Fields
			}
		})
	}
}

// schemaShadowKnown names the commands whose op the in-process walk has
// not caught up with, and why; each leaves with its fix (DFC170).
var schemaShadowKnown = map[string]string{}

// shadowStages splits a pipeline at its unquoted pipes.
func shadowStages(p string) ([]string, error) {
	var stages []string
	var quote byte
	start := 0
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '|':
			stages = append(stages, strings.TrimSpace(p[start:i]))
			start = i + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	return append(stages, strings.TrimSpace(p[start:])), nil
}

// shadowArgv is a stage's words after the binary, quotes removed.
func shadowArgv(stage string) ([]string, error) {
	var words []string
	var w strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(stage); i++ {
		c := stage[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				w.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == ' ' || c == '\t':
			if in {
				words = append(words, w.String())
				w.Reset()
				in = false
			}
		default:
			w.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, w.String())
	}
	if len(words) < 2 || words[0] != "{{.bin}}" {
		return nil, fmt.Errorf("stage does not start with {{.bin}}")
	}
	return words[1:], nil
}
