package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// registerGeneratePretty registers `generate pretty`: the pipeline as
// written, one stage to a line and within -width columns, as bash runs
// it. The source is the pipeline document (`generate json`), each
// stage's own argv, so no shell text is parsed; where a stage's clauses
// start comes from the stage's own flag declarations.
func registerGeneratePretty(cmd *cf.SubcommandBuilder) {
	sub := cmd.Subcommand("pretty").
		Description("Print the pipeline as written, one stage to a line and within 80 columns, as a bash pipeline").
		Example("ssql generate pretty -pipeline 'ssql from csv data.csv | ssql where -if age gt 25 | ssql to csv'", "Lay out a pipeline").
		Example("ssql generate pretty -pipeline \"$(cat long.sh)\" > pipeline.sh && bash pipeline.sh", "Rewrite a long one-liner as a script that runs the same").
		Example("(export SSQL_MODE=record; ssql from csv a.csv | ssql join <(ssql from csv b.csv) -using id | ssql to table) | ssql generate pretty", "A process substitution is laid out in place").

		Flag("-width", "-w").
			Int().
			Global().
			Default(80).
			Help("The column every line fits within (at least 40). An argument longer than a line, such as a long quoted expression, cannot be split without changing it: its line runs over, and a warning names it").
			Done()
	pipelineSourceFlags(sub, "lay out", "record").
		Handler(func(ctx *cf.Context) error {
			width := 80
			if v, ok := ctx.GlobalFlags["-width"]; ok {
				width = v.(int)
			}
			if width < 40 {
				return fmt.Errorf("ssql generate pretty -width %d: the width is at least 40", width)
			}
			src, err := generateFragmentSource(ctx, "record", "pretty")
			if err != nil {
				return err
			}
			fragments, err := lib.ReadCodeFragmentsFromReader(src)
			if err != nil {
				return fmt.Errorf("reading fragments: %w", err)
			}
			text, over, err := prettyFromFragments(ctx.Command, fragments, width)
			if err != nil {
				return err
			}
			for _, o := range over {
				fmt.Fprintf(os.Stderr, "ssql generate pretty: line %d is %d columns: %s is longer than a line and cannot be split without changing it\n", o.line, o.cols, o.word)
			}
			fmt.Fprint(ctx.Stdout(), text)
			return nil
		}).
		Done()
}

// prettyFromFragments lays out the pipeline the fragments describe, as
// written: the zone a source pinned for itself and the argv a command
// rewrote for itself come off (as the explore preview does), so what is
// printed is what was typed.
func prettyFromFragments(root *cf.Command, fragments []*lib.CodeFragment, width int) (string, []prettyOverflow, error) {
	for _, frag := range fragments {
		dropResolvedZone(frag)
		restoreAsWritten(frag)
	}
	raw, err := pipelineDocFromFragments(fragments)
	if err != nil {
		return "", nil, err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return "", nil, err
	}
	stages, err := parsePipelineDoc(data)
	if err != nil {
		return "", nil, err
	}
	text, over := prettyPipeline(root, stages, width)
	return text, over, nil
}

// PrettyLineWidth is the width the editor binding and the language
// server lay a pipeline out at (Ross, 2026-11-10: a fixed 80, since the
// file outlives the terminal it was typed in).
const PrettyLineWidth = 80

// prettyLineTimeout bounds describing a pipeline for PrettyLine: the
// editor key waits for it, and so does the terminal.
const prettyLineTimeout = 10 * time.Second

// PrettyLine is a shell line with its ssql pipeline laid out, for the
// editor binding (-pretty-line) and the language server's formatting.
// Text before the pipeline (`cat x |`) stays on a line of its own, and
// text after it (`| less`, `> out.txt`) follows the last stage. The
// pipeline is described in fragment mode, which runs it under bash, so
// it is laid out only when pipelineSafeToDescribe allows: a `$(…)` on
// the line never runs because a key was pressed. The error says why the
// line is left as it is.
func PrettyLine(line string, root *cf.Command, width int) (string, error) {
	prefix, segment, suffix, err := SplitPipeline(line)
	if err != nil {
		return "", err
	}
	if !pipelineSafeToDescribe(segment) {
		return "", fmt.Errorf("a pipeline is laid out only when it is ssql commands alone, with no command substitution, redirection or second command")
	}
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), prettyLineTimeout)
	defer cancel()
	out, err := describePipeline(ctx, segment, "record", "pretty", &stderr)
	if err != nil {
		// The refusing stage's own message, not the shell's exit status.
		if msg := firstStderrLine(stderr.Bytes(), err); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	fragments, err := lib.ReadCodeFragmentsFromReader(bytes.NewReader(out))
	if err != nil {
		return "", fmt.Errorf("reading fragments: %w", err)
	}
	text, _, err := prettyFromFragments(root, fragments, width)
	if err != nil {
		return "", err
	}
	text = strings.TrimSuffix(text, "\n")
	if prefix != "" {
		text = prefix + "\n" + text
	}
	if suffix != "" {
		text += " " + suffix
	}
	return text, nil
}

// prettyOverflow is a line that runs past the width, and the word that
// made it.
type prettyOverflow struct {
	line, cols int
	word       string
}

// prettyPipeline lays the pipeline out as bash runs it. Stages are
// joined by a `|` at the end of a line, each stage after the first
// indented two columns; a stage that does not fit continues on lines
// ending in a backslash, indented two more, broken where one of its
// clauses starts (a flag it declares, or a clause separator) when the
// clause fits on a line, else between words. A process substitution that
// does not fit on a line is laid out the same way inside its `<(…)`, its
// stages aligned under its first. Neither a backslash-newline nor a
// newline after `|` changes the words bash passes, so the layout cannot
// change what runs; root (the command tree) only decides where the
// breaks read best, and may be nil.
func prettyPipeline(root *cf.Command, stages []docStage, width int) (string, []prettyOverflow) {
	p := &prettyLayout{root: root, width: width, fresh: true}
	p.pipeline(stages, 2)
	p.lines = append(p.lines, string(p.cur))
	p.longest = append(p.longest, p.curLong)
	var over []prettyOverflow
	for i, l := range p.lines {
		if n := len([]rune(l)); n > width {
			over = append(over, prettyOverflow{line: i + 1, cols: n, word: shorten(p.longest[i])})
		}
	}
	return strings.Join(p.lines, "\n") + "\n", over
}

type prettyLayout struct {
	root    *cf.Command
	width   int
	reserve int // columns kept free at the end of a line: " \" or " |", plus a ")" per enclosing <(…)
	lines   []string
	longest []string // per line, the longest word written on it (for the warning)
	cur     []rune
	curLong string
	fresh   bool // nothing written on the current line after its indent
}

func (p *prettyLayout) col() int { return len(p.cur) }

// fits reports whether n more columns fit on the current line, with the
// line's ending kept free.
func (p *prettyLayout) fits(n int) bool { return p.col()+n+2+p.reserve <= p.width }

func (p *prettyLayout) write(s string) {
	p.cur = append(p.cur, []rune(s)...)
	p.fresh = false
	if len(s) > len(p.curLong) {
		p.curLong = s
	}
}

// word writes s after a space, unless it opens the line.
func (p *prettyLayout) word(s string) {
	if !p.fresh {
		p.write(" ")
	}
	p.write(s)
}

// newline ends the line with end (" \" or " |") and indents the next.
func (p *prettyLayout) newline(end string, indent int) {
	p.lines = append(p.lines, string(p.cur)+end)
	p.longest = append(p.longest, p.curLong)
	p.cur = []rune(strings.Repeat(" ", indent))
	p.curLong = ""
	p.fresh = true
}

// pipeline writes the stages from the current column; stages after the
// first start at indent, and a stage's continuation lines at indent+2.
func (p *prettyLayout) pipeline(stages []docStage, indent int) {
	for i, st := range stages {
		if i > 0 {
			p.newline(" |", indent)
		}
		p.stage(st, indent+2)
	}
}

// stage writes one stage, clause by clause. A clause that had to be
// split across lines is followed by a fresh line, so the next clause
// does not start on the last line of the one before.
func (p *prettyLayout) stage(st docStage, cont int) {
	split := false
	for i, g := range p.clauses(st) {
		inline := prettyInline(g)
		w := len([]rune(inline))
		if split && !p.fresh {
			p.newline(" \\", cont)
		}
		before := len(p.lines)
		split = false
		switch {
		case i == 0:
			p.words(g, cont)
			split = len(p.lines) > before
		case p.fits(1 + w):
			p.word(inline)
		case cont+w+2+p.reserve <= p.width:
			p.newline(" \\", cont)
			p.write(inline)
		default:
			p.words(g, cont)
			split = len(p.lines) > before+1 || len(p.lines) > before && !p.fresh
		}
	}
}

// words writes a clause word by word, breaking where a word does not fit.
func (p *prettyLayout) words(g []docArg, cont int) {
	for _, a := range g {
		if a.Nested != nil {
			p.nested(a.Nested, cont)
			continue
		}
		t := ssql.ShellQuote(a.Text)
		if !p.fresh && !p.fits(1+len([]rune(t))) {
			p.newline(" \\", cont)
		}
		p.word(t)
	}
}

// nested writes a process substitution: on the line when it fits there,
// else on a line of its own, else laid out inside its <(…) with its
// stages aligned under the first.
func (p *prettyLayout) nested(stages []docStage, cont int) {
	inline := "<(" + renderPipelineDoc(stages) + ")"
	w := len([]rune(inline))
	switch {
	case p.fits(1 + w):
		p.word(inline)
		return
	case !p.fresh && cont+w+2+p.reserve <= p.width:
		p.newline(" \\", cont)
		p.write(inline)
		return
	}
	if !p.fresh {
		p.newline(" \\", cont)
	}
	p.word("<(")
	p.fresh = true // the inner pipeline's first word follows "<(" directly
	p.reserve++    // room for the ")"
	p.pipeline(stages, p.col())
	p.reserve--
	p.write(")")
}

// clauses splits a stage into its head (ssql, the command, the words up
// to its first flag) and one group per clause: a declared flag with the
// words up to the next, a clause separator joined to the flag after it.
func (p *prettyLayout) clauses(st docStage) [][]docArg {
	flags, seps := p.spec(st)
	isFlag := func(a docArg) bool {
		t := a.Text
		if a.Nested != nil || len(t) < 2 || (t[0] != '-' && t[0] != '+') {
			return false
		}
		return flags["-"+t[1:]]
	}
	isSep := func(a docArg) bool { return a.Nested == nil && slices.Contains(seps, a.Text) }
	groups := [][]docArg{{{Text: "ssql"}}}
	for i, a := range st {
		cur := &groups[len(groups)-1]
		startsClause := isSep(a) || isFlag(a) && !(i > 0 && isSep(st[i-1]))
		if startsClause && i > 0 {
			groups = append(groups, []docArg{a})
			continue
		}
		*cur = append(*cur, a)
	}
	return groups
}

// spec is the stage's declared flag names and clause separators, from
// the command tree; with no tree, none (the stage breaks between words).
func (p *prettyLayout) spec(st docStage) (map[string]bool, []string) {
	flags := map[string]bool{}
	if p.root == nil || len(st) == 0 {
		return flags, nil
	}
	sc := p.root.GetSubcommand(st[0].Text)
	if sc == nil {
		return flags, nil
	}
	if len(st) > 1 && st[1].Nested == nil && sc.Subcommands[st[1].Text] != nil {
		sc = sc.Subcommands[st[1].Text]
	}
	for _, f := range sc.Flags {
		for _, n := range f.Names {
			if strings.HasPrefix(n, "-") {
				flags[n] = true
			}
		}
	}
	return flags, sc.Separators
}

// prettyInline is a clause on one line, as the shell form writes it.
func prettyInline(g []docArg) string {
	words := make([]string, len(g))
	for i, a := range g {
		if a.Nested != nil {
			words[i] = "<(" + renderPipelineDoc(a.Nested) + ")"
			continue
		}
		words[i] = ssql.ShellQuote(a.Text)
	}
	return strings.Join(words, " ")
}

// shorten keeps a warning's quoted argument to a readable length.
func shorten(w string) string {
	if r := []rune(w); len(r) > 40 {
		return string(r[:37]) + "..."
	}
	return w
}
