package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
)

// A pipeline document (DFC134 §5.1) is the pipeline as the operating system
// receives it: a list of stages, each a list of argument strings. There is
// no shell and so no grammar to escape from: an untrusted value occupies
// one element, and no sequence of characters inside it ends the element.
//
//	[
//	  ["from", "csv", "-arg", "orders.csv"],
//	  ["join", ["from", "csv", "-arg", "customers.csv"], "-on", "id"],
//	  ["where", "-if", "customer", "eq", "<anything at all>"],
//	  ["to", "csv"]
//	]
//
// An element that is itself a list of stages is a nested pipeline: it runs
// concurrently and the stage receives its output as a file argument, which
// is what `join <(ssql from …)` does in a shell, without the shell. A stage's
// first element is the command path, which is not data: it must name a
// command (the validator asks the real parser, autocli's Check).
//
// Positionals in a document are written `-arg VALUE` (§5.2) so that no
// value can be read as a flag or a clause separator; the renderer emits
// that form for any value that would be misread.

// docStage is one stage: argument strings, some of which may be nested
// pipelines standing for a file argument.
type docStage []docArg

type docArg struct {
	Text   string
	Nested []docStage // non-nil: a nested pipeline in this argument's place
}

// parsePipelineDoc reads the JSON form.
func parsePipelineDoc(data []byte) ([]docStage, error) {
	var raw any
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("pipeline document: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("pipeline document: trailing content after the pipeline")
	}
	// {"pipeline": [...]} is accepted as the object form, for documents
	// that will grow policy fields (§5.4) beside the stages.
	if obj, ok := raw.(map[string]any); ok {
		inner, ok := obj["pipeline"]
		if !ok {
			return nil, fmt.Errorf("pipeline document: an object needs a \"pipeline\" field holding the stages")
		}
		raw = inner
	}
	stages, err := docStagesFrom(raw, "")
	if err != nil {
		return nil, fmt.Errorf("pipeline document: %w", err)
	}
	return stages, nil
}

func docStagesFrom(raw any, where string) ([]docStage, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%sexpected a list of stages, got %s", where, jsonKind(raw))
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%sthe pipeline has no stages", where)
	}
	// A single stage written as a list of strings is that one-stage
	// pipeline: `["join", ["from", "csv", "-arg", "b.csv"], "-on", "id"]`.
	if len(list) > 0 {
		if _, isString := list[0].(string); isString {
			list = []any{list}
		}
	}
	stages := make([]docStage, 0, len(list))
	for i, s := range list {
		elems, ok := s.([]any)
		if !ok {
			return nil, fmt.Errorf("%sstage %d: expected a list of arguments, got %s", where, i+1, jsonKind(s))
		}
		if len(elems) == 0 {
			return nil, fmt.Errorf("%sstage %d is empty", where, i+1)
		}
		stage := make(docStage, 0, len(elems))
		for j, e := range elems {
			switch v := e.(type) {
			case string:
				stage = append(stage, docArg{Text: v})
			case []any:
				if j == 0 {
					return nil, fmt.Errorf("%sstage %d: the first element is the command name and cannot be a nested pipeline", where, i+1)
				}
				nested, err := docStagesFrom(v, fmt.Sprintf("%sstage %d, argument %d: ", where, i+1, j+1))
				if err != nil {
					return nil, err
				}
				stage = append(stage, docArg{Nested: nested})
			default:
				return nil, fmt.Errorf("%sstage %d, argument %d: every argument is a string (or a nested pipeline), got %s", where, i+1, j+1, jsonKind(e))
			}
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number (write it as a string: the command decides what it is)"
	case string:
		return "a string"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprintf("%T", v)
}

// checkPipelineDoc validates every stage, nested ones included, against the
// real command grammar before anything runs: the command path must be a
// command, and autocli's Check must accept the argv. A nested pipeline is
// checked in place with a placeholder path, since the fd it will become
// does not exist yet.
func checkPipelineDoc(root *cf.Command, stages []docStage, where string) error {
	for i, st := range stages {
		loc := fmt.Sprintf("%sstage %d (%s)", where, i+1, st.name(root))
		first := st[0].Text
		if first == "" || strings.HasPrefix(first, "-") || strings.HasPrefix(first, "+") {
			return fmt.Errorf("%s: the first element must be a command name, not %q", loc, first)
		}
		if first == "run" {
			return fmt.Errorf("%s: a document does not run documents; inline the stages", loc)
		}
		for j, a := range st {
			if a.Nested != nil {
				if err := checkPipelineDoc(root, a.Nested, fmt.Sprintf("%s, argument %d: ", loc, j+1)); err != nil {
					return err
				}
			}
		}
		if err := root.Check(st.argv(func(int) string { return "/dev/fd/0" })); err != nil {
			return fmt.Errorf("%s: %w", loc, err)
		}
	}
	return nil
}

// name is the stage's command path for messages: the first element, plus
// the second when it names a subcommand of the first (`from csv`).
func (st docStage) name(root *cf.Command) string {
	first := st[0].Text
	if len(st) > 1 && st[1].Nested == nil && root != nil {
		if sc := root.GetSubcommand(first); sc != nil && sc.Subcommands[st[1].Text] != nil {
			return first + " " + st[1].Text
		}
	}
	return first
}

// argv renders the stage's argument strings; nested pipelines become the
// path fd gives for their index among the stage's nested arguments.
func (st docStage) argv(fd func(k int) string) []string {
	out := make([]string, 0, len(st))
	k := 0
	for _, a := range st {
		if a.Nested != nil {
			out = append(out, fd(k))
			k++
			continue
		}
		out = append(out, a.Text)
	}
	return out
}

// renderPipelineDoc writes the shell form, the way a person would type it:
// `ssql a | ssql b <(ssql c | ssql d)`, every element shell-quoted. It
// means exactly what the document means, since the shell hands the same
// elements to the same parser; a document that carries a positional bare
// where -arg was needed is wrong in both forms alike.
func renderPipelineDoc(stages []docStage) string {
	var parts []string
	for _, st := range stages {
		words := []string{"ssql"}
		for _, a := range st {
			if a.Nested != nil {
				words = append(words, "<("+renderPipelineDoc(a.Nested)+")")
				continue
			}
			words = append(words, ssql.ShellQuote(a.Text))
		}
		parts = append(parts, strings.Join(words, " "))
	}
	return strings.Join(parts, " | ")
}

// chainOptions configures startChain.
type chainOptions struct {
	Dir    string
	Env    []string   // extra environment entries; nil inherits
	Stdin  io.Reader  // first stage's stdin; nil = none
	Stdout io.Writer  // last stage's stdout; nil = a pipe, read via stageChain.out
	Stderr []io.Writer // per stage; nil = captured per stage (stageChain.stderr)
	Extra  [][]*os.File // per stage: files passed as fd 3, 4, … (nested pipelines)
}

// startChain wires and starts `self stage[0] | self stage[1] | …`.
//
// Intermediate links are explicit os.Pipe()s and the parent CLOSES its
// copies right after starting the children — like a shell, the children
// must be the only holders. (The first version used exec.StdoutPipe, whose
// read end the parent keeps until Wait: when a downstream stage exited
// early — `… | limit 10` on a 1.2GB file — upstream never got EPIPE,
// filled the 64KB pipe buffer, and the chain deadlocked. Found live by
// Ross; pinned by TestServeExecuteEarlyExit.)
func startChain(ctx context.Context, self string, stages [][]string, o chainOptions) (*stageChain, error) {
	ch := &stageChain{stderrs: make([]bytes.Buffer, len(stages))}
	ch.cmds = make([]*exec.Cmd, len(stages))
	var parentCopies []*os.File
	closeParentCopies := func() {
		for _, f := range parentCopies {
			f.Close()
		}
	}
	for i, args := range stages {
		ch.cmds[i] = exec.CommandContext(ctx, self, args...)
		ch.cmds[i].Dir = o.Dir
		if len(o.Env) > 0 {
			ch.cmds[i].Env = append(os.Environ(), o.Env...)
		}
		if o.Stderr != nil && o.Stderr[i] != nil {
			ch.cmds[i].Stderr = o.Stderr[i]
		} else {
			ch.cmds[i].Stderr = &ch.stderrs[i]
		}
		if i < len(o.Extra) {
			ch.cmds[i].ExtraFiles = o.Extra[i]
		}
		if i == 0 {
			ch.cmds[i].Stdin = o.Stdin
		} else {
			r, w, err := os.Pipe()
			if err != nil {
				closeParentCopies()
				return nil, err
			}
			ch.cmds[i-1].Stdout = w
			ch.cmds[i].Stdin = r
			parentCopies = append(parentCopies, r, w)
		}
	}
	last := ch.cmds[len(ch.cmds)-1]
	if o.Stdout != nil {
		last.Stdout = o.Stdout
	} else {
		out, err := last.StdoutPipe()
		if err != nil {
			closeParentCopies()
			return nil, err
		}
		ch.out = out
	}
	for _, c := range ch.cmds {
		if err := c.Start(); err != nil {
			closeParentCopies()
			return nil, err
		}
	}
	// The children hold dups now; the parent must not keep the pipes
	// alive or early-exiting consumers can't EPIPE their producers.
	closeParentCopies()
	return ch, nil
}

// runPipelineDoc runs a document with the runner's own stdin, stdout and
// stderr, nested pipelines wired as /dev/fd/N. It returns the first
// failure with the stage named, shell status semantics (stageChain.wait).
func runPipelineDoc(ctx context.Context, root *cf.Command, self string, stages []docStage, stdin io.Reader, stdout, stderr io.Writer, env ...string) error {
	var nestedChains []*stageChain
	var nestedClose []*os.File
	defer func() {
		for _, f := range nestedClose {
			f.Close()
		}
	}()
	argv := make([][]string, len(stages))
	extra := make([][]*os.File, len(stages))
	stderrs := make([]io.Writer, len(stages))
	for i, st := range stages {
		stderrs[i] = stderr
		var files []*os.File
		for _, a := range st {
			if a.Nested == nil {
				continue
			}
			r, w, err := os.Pipe()
			if err != nil {
				return err
			}
			// The nested pipeline writes into w; the stage reads r as
			// /dev/fd/(3+k). The parent's copies close once both have
			// started, so an early-exiting reader EPIPEs the writer.
			nested, err := runPipelineDocTo(ctx, self, a.Nested, w, stderr, env...)
			w.Close()
			if err != nil {
				r.Close()
				return err
			}
			nestedChains = append(nestedChains, nested)
			nestedClose = append(nestedClose, r)
			files = append(files, r)
		}
		extra[i] = files
		argv[i] = st.argv(func(k int) string { return fmt.Sprintf("/dev/fd/%d", 3+k) })
	}
	ch, err := startChain(ctx, self, argv, chainOptions{Stdin: stdin, Stdout: stdout, Stderr: stderrs, Extra: extra, Env: env})
	// The stages hold their dups of the nested read ends now.
	for _, f := range nestedClose {
		f.Close()
	}
	nestedClose = nil
	if err != nil {
		return err
	}
	err = ch.waitNamed(root, stages)
	for _, n := range nestedChains {
		if nerr := n.wait(); nerr != nil && err == nil {
			err = fmt.Errorf("nested pipeline: %w", nerr)
		}
	}
	return err
}

// runPipelineDocTo starts a nested document writing to w (recursively
// wiring its own nested pipelines) and returns it running.
func runPipelineDocTo(ctx context.Context, self string, stages []docStage, w *os.File, stderr io.Writer, env ...string) (*stageChain, error) {
	for _, st := range stages {
		for _, a := range st {
			if a.Nested != nil {
				// Two levels of process substitution is possible in a shell
				// too, but a document that needs it is better written flat;
				// refuse rather than half-support it.
				return nil, errors.New("a nested pipeline cannot itself contain a nested pipeline")
			}
		}
	}
	argv := make([][]string, len(stages))
	stderrs := make([]io.Writer, len(stages))
	for i, st := range stages {
		argv[i] = st.argv(nil)
		stderrs[i] = stderr
	}
	return startChain(ctx, self, argv, chainOptions{Stdout: w, Stderr: stderrs, Env: env})
}

// waitNamed is wait with the failing stage named in the error.
func (ch *stageChain) waitNamed(root *cf.Command, stages []docStage) error {
	var lastErr, upstreamErr error
	for i, c := range ch.cmds {
		err := c.Wait()
		if err == nil {
			continue
		}
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == -1 && i < len(ch.cmds)-1 {
			continue // killed by SIGPIPE after its consumer exited early: normal
		}
		named := fmt.Errorf("stage %d (%s): %w", i+1, stages[i].name(root), err)
		if i == len(ch.cmds)-1 {
			lastErr = named
		} else if upstreamErr == nil {
			upstreamErr = named
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return upstreamErr
}

// pipelineSourceFlags registers the fragment-source flags on a generate
// target, ONCE for all four (DFC139 §3.C, the half-way step before the
// flags can live on `generate` itself): -pipeline, -script and -json each
// name where the fragments come from instead of stdin, -mode the
// SSQL_MODE the source pipeline runs under. verb is what the target does
// with the fragments ("generate Go from", "translate", "optimize",
// "re-emit"). Every generate leaf MUST call this; the registration drift
// test pins it, since -script had gone missing from three targets by
// being declared per target.
func pipelineSourceFlags(sb *cf.SubcommandBuilder, verb, modeDefault string) *cf.SubcommandBuilder {
	return sb.
		Flag("-pipeline", "-p").
		String().
		Global().
		Default("").
		Help("Run PIPELINE (a quoted ssql pipeline string) under -mode and " + verb + " its fragments — no export/subshell ceremony. Mutually exclusive with -script/-json.").
		Done().
		Flag("-script", "-s").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.ssql"}).
		Global().
		Default("").
		Help("Run the pipeline in script FILE (or <(heredoc)) under -mode and " + verb + " its fragments; # comments stripped, leading-| continuation lines joined. Mutually exclusive with -pipeline/-json.").
		Done().
		Flag("-json", "-j").
		String().
		Completer(&cf.FileCompleter{Pattern: "*.json"}).
		Global().
		Default("").
		Help("Run the pipeline document FILE (as `ssql run` does: no shell, validated first) under -mode and " + verb + " its fragments. Mutually exclusive with -pipeline/-script.").
		Done().
		Flag("-mode").
		String().
		Completer(&cf.StaticCompleter{Options: []string{"record", "typed"}}).
		Global().
		Default("").
		Help("With -pipeline/-script/-json: the SSQL_MODE the source pipeline runs under (record or typed; parallel is a deprecated alias for typed). Default: the shell's SSQL_MODE when this target can use it, else " + modeDefault + ".").
		Done()
}

// runDocForFragments validates and runs a pipeline document with SSQL_MODE
// set for every stage, returning the fragment stream the stages emit.
func runDocForFragments(root *cf.Command, path, mode, label string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ssql generate %s: %w", label, err)
	}
	stages, err := parsePipelineDoc(data)
	if err != nil {
		return nil, fmt.Errorf("ssql generate %s: %w", label, err)
	}
	if err := checkPipelineDoc(root, stages, ""); err != nil {
		return nil, fmt.Errorf("ssql generate %s: %w", label, err)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := runPipelineDoc(context.Background(), root, self, stages, nil, &out, os.Stderr, "SSQL_MODE="+mode); err != nil {
		return nil, fmt.Errorf("ssql generate %s: pipeline failed (mode=%s): %w", label, mode, err)
	}
	return out.Bytes(), nil
}

// sourceModeFromEnv is the SSQL_MODE a -pipeline/-script/-json source
// runs under when -mode is not given: the calling shell's SSQL_MODE (or
// the deprecated SSQLGO) when it names a codegen mode — record, typed,
// or parallel as typed's alias, SSQLGO=1/true as record's — else
// fallback. Until 2026-10-09 the flag forms ignored the environment, so
// `export SSQL_MODE=record; ssql generate go -run -pipeline …` ran the
// stages typed.
func sourceModeFromEnv(fallback string) string {
	switch m := strings.ToLower(modeEnv()); m {
	case "record", "typed":
		return m
	case "parallel":
		return "typed"
	case "1", "true":
		return "record"
	}
	return fallback
}

// generateFragmentSource resolves a generate subcommand's fragment source:
// stdin unless exactly one of -pipeline, -script or -json names it.
// mode is the SSQL_MODE the stages run under.
func generateFragmentSource(ctx *cf.Context, mode, label string) (io.Reader, error) {
	get := func(name string) string {
		v, _ := ctx.GlobalFlags[name].(string)
		return v
	}
	pipeline, script, doc := get("-pipeline"), get("-script"), get("-json")
	if m := get("-mode"); m != "" && label != "go" && m != mode {
		// sql, ssql and json read record-mode fragments, schema reads a
		// schema header; a run in another mode would produce output the
		// target does not consume. Loud, not ignored.
		return nil, fmt.Errorf("ssql generate %s: -mode %s has no meaning here (this target reads %s-mode output); drop -mode or use %s", label, m, mode, mode)
	}
	set := 0
	for _, v := range []string{pipeline, script, doc} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return nil, fmt.Errorf("ssql generate %s: -pipeline, -script and -json are mutually exclusive (each names the pipeline source)", label)
	}
	var fragments []byte
	var err error
	switch {
	case doc != "":
		fragments, err = runDocForFragments(ctx.Command, doc, mode, label+" -json")
	case script != "":
		fragments, err = runScriptForFragments(script, mode)
	case pipeline != "":
		fragments, err = runPipelineForFragments(pipeline, mode, label+" -pipeline")
	default:
		return ctx.Stdin(), nil
	}
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(fragments), nil
}

// checkPipelineFields validates a document's FIELD references before
// anything runs (DFC138 §4), by asking the commands themselves: the
// source stage runs under SSQL_MODE=schema and answers with its header
// (names and, since v4.109, types); every following stage then runs in
// ordinary exec mode on that header ALONE — no rows — so the command's
// own validation refuses a field it reads that is not there ("field
// "statuss" not found (available: …)") and its output header, with the
// fields it creates, feeds the next stage. No table of which arguments
// are reads and which create fields is needed: `update -set new 1` is
// legal because update says so. Nested pipelines (a join's <(…)>) are
// checked first, as sources of their own. The walk stops at a sink or
// tee (they write files) and where a stage's header is not known (its
// output is inferred from rows it did not get); syntax has already
// been checked to the end by checkPipelineDoc.
func checkPipelineFields(ctx context.Context, root *cf.Command, self string, stages []docStage, where string) error {
	if len(stages) == 0 {
		return nil
	}
	for i, st := range stages {
		for j, a := range st {
			if a.Nested != nil {
				loc := fmt.Sprintf("%sstage %d (%s), argument %d: ", where, i+1, st.name(root), j+1)
				if err := checkPipelineFields(ctx, root, self, a.Nested, loc); err != nil {
					return err
				}
			}
		}
	}
	// Exec mode for the stages whatever the caller's environment says:
	// an inherited SSQL_MODE=record would make them emit fragments.
	env := []string{"SSQL_MODE=", "SSQLGO="}
	var out, errb bytes.Buffer
	if err := runPipelineDoc(ctx, root, self, stages[:1], nil, &out, &errb, "SSQL_MODE=schema", "SSQLGO="); err != nil {
		return fmt.Errorf("%sstage 1 (%s): %s", where, stages[0].name(root), firstStderrLine(errb.Bytes(), err))
	}
	header := schemaHeaderLine(out.Bytes())
	for i := 1; i < len(stages) && header != nil; i++ {
		st := stages[i]
		if first := st[0].Text; first == "to" || first == "tee" {
			break
		}
		out.Reset()
		errb.Reset()
		if err := runPipelineDoc(ctx, root, self, stages[i:i+1], bytes.NewReader(header), &out, &errb, env...); err != nil {
			return fmt.Errorf("%sstage %d (%s): %s", where, i+1, st.name(root), firstStderrLine(errb.Bytes(), err))
		}
		header = schemaHeaderLine(out.Bytes())
	}
	return nil
}

// schemaHeaderLine returns the `_schema` header line of a stage's output
// (with its newline), or nil when the output carries none.
func schemaHeaderLine(out []byte) []byte {
	line := out
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		line = out[:i+1]
	}
	if !bytes.Contains(line, []byte(`"_schema"`)) {
		return nil
	}
	return line
}

// firstStderrLine is a stage's own error message (its first stderr line
// without the "Error: " prefix), or the exit error when it wrote none.
func firstStderrLine(stderr []byte, err error) string {
	for _, line := range strings.Split(strings.TrimSpace(string(stderr)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return strings.TrimPrefix(line, "Error: ")
		}
	}
	return err.Error()
}
