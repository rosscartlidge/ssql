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
func runPipelineDoc(ctx context.Context, root *cf.Command, self string, stages []docStage, stdin io.Reader, stdout, stderr io.Writer) error {
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
			nested, err := runPipelineDocTo(ctx, self, a.Nested, w, stderr)
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
	ch, err := startChain(ctx, self, argv, chainOptions{Stdin: stdin, Stdout: stdout, Stderr: stderrs, Extra: extra})
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
func runPipelineDocTo(ctx context.Context, self string, stages []docStage, w *os.File, stderr io.Writer) (*stageChain, error) {
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
	return startChain(ctx, self, argv, chainOptions{Stdout: w, Stderr: stderrs})
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
