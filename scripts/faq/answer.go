package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// unsupported is a reply that opens with UNSUPPORTED, however a model
// decorates it (**UNSUPPORTED:**, # UNSUPPORTED, > UNSUPPORTED).
var unsupported = regexp.MustCompile("(?i)^[\\s*_#>`]*unsupported\\b")

// The answer loop (DFC169 §4): retrieve doc sections, ground the model in
// the sample data's real columns, draft, check the pipeline in schema mode,
// run it, and on any failure hand ssql's own error and the -help of the
// commands used back to the model, at most `retries` times. A pipeline that
// never runs is reported as such, never shown as an answer.

type faq struct {
	repo    string // the ssql checkout (docs, prompt, docsearch)
	box     sandbox
	retries int
	docsK   int
	system  string // the CLI prompt plus the sample data's schemas
	verbose bool
}

type answer struct {
	Reply        string        // the model's final text
	Pipeline     string        // its pipeline ("" for prose or a refusal)
	Unsupported  bool          // the model said ssql cannot do it
	Ran          bool          // the pipeline passed the pre-flight and ran
	Illustrative bool          // it names a file the sample data lacks: shown, not run
	Output       string        // its stdout, when it ran
	Failure      string        // the last error, when it never ran
	Retries      int           // corrections used
	Docs         []string      // file:line of the sections given to the model
	Wall         time.Duration // model time across the turns
	GenTokens    int
}

const answerFormat = `
## How to answer

You answer questions about ssql for people exploring it. The files below are
in the working directory; use them when the question needs data.

- When the question asks for a result, give ONE pipeline in a single
` + "```bash" + ` block, then one or two sentences on what it does.
- Every stage starts with ` + "`ssql`" + `; no other commands, no redirection, no ` + "`;`" + `.
- Read only the columns the file schemas below list; the columns your own
  stages create are yours to name.
- When ssql cannot do what is asked, start your reply with "UNSUPPORTED:" and
  say what comes closest. Never invent a command or a flag.
- For a question about how ssql works, answer in a few sentences; add a
  pipeline only when it shows the point.
`

// newFAQ builds the system prompt: the tuned CLI prompt, the answer
// format, and each sample file's columns and types as schema mode reports
// them (the authority, not a guess: gemma4:26b wrote `department` for
// `dept` without it, DFC169 §3).
func newFAQ(repo string, box sandbox, retries, docsK int, verbose bool) (*faq, error) {
	prompt, err := os.ReadFile(filepath.Join(repo, "doc", "ai-cli-generation.md"))
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	sb.Write(prompt)
	sb.WriteString(answerFormat)
	sb.WriteString("\n## The files\n\n")
	files, _ := filepath.Glob(filepath.Join(box.dataDir, "*.csv"))
	sort.Strings(files)
	for _, f := range files {
		name := filepath.Base(f)
		r := box.run("ssql from " + name + " | ssql generate schema -data | ssql to csv")
		if r.err != nil {
			return nil, fmt.Errorf("schema of %s: %v: %s", name, r.err, r.stderr)
		}
		var cols []string
		for _, ln := range strings.Split(strings.TrimSpace(r.stdout), "\n")[1:] {
			cols = append(cols, strings.Replace(ln, ",", " (", 1)+")")
		}
		fmt.Fprintf(&sb, "- `%s`: %s\n", name, strings.Join(cols, ", "))
	}
	return &faq{repo: repo, box: box, retries: retries, docsK: docsK, system: sb.String(), verbose: verbose}, nil
}

// userDocs are the sections an answer may cite: the user documentation and
// the CHANGELOG, not research DFCs or journals, where superseded plans read
// as current (DFC169 §4).
var userDocs = regexp.MustCompile(`^(doc/[^/]+\.md|README\.md|CHANGELOG\.md):(\d+)`)

// retrieve runs docsearch and returns the top user-doc sections, each as
// its heading line through the next heading, capped.
func (f *faq) retrieve(question string) (refs []string, text string) {
	cmd := exec.Command(filepath.Join(f.repo, "scripts", "docsearch.sh"), "-k", "40", question)
	cmd.Dir = f.repo
	out, err := cmd.Output()
	if err != nil {
		return nil, ""
	}
	var sb strings.Builder
	seen := map[string]bool{}
	for _, ln := range strings.Split(string(out), "\n") {
		m := userDocs.FindStringSubmatch(ln)
		if m == nil || seen[m[0]] || strings.HasPrefix(m[1], "doc/ai-") {
			continue // the ai-* prompts: the CLI one is the system prompt already
		}
		seen[m[0]] = true
		sec := section(filepath.Join(f.repo, m[1]), m[2])
		if sec == "" {
			continue
		}
		refs = append(refs, m[0])
		fmt.Fprintf(&sb, "\n--- %s ---\n%s\n", m[0], sec)
		if len(refs) == f.docsK {
			break
		}
	}
	return refs, sb.String()
}

// section is the markdown section starting at line (1-based), to the next
// heading, at most 60 lines.
func section(path, line string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var n int
	fmt.Sscan(line, &n)
	lines := strings.Split(string(b), "\n")
	if n < 1 || n > len(lines) {
		return ""
	}
	out := []string{lines[n-1]}
	for i := n; i < len(lines) && len(out) < 60; i++ {
		if strings.HasPrefix(lines[i], "#") {
			break
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

// ask answers one question with one model.
func (f *faq) ask(llm ollama, question string) answer {
	var a answer
	refs, docs := f.retrieve(question)
	a.Docs = refs
	user := question
	if docs != "" {
		user = "Documentation that may help:\n" + docs + "\n\nQuestion: " + question
	}
	msgs := []message{{Role: "system", Content: f.system}, {Role: "user", Content: user}}
	for turn := 0; ; turn++ {
		reply, st, err := llm.chat(msgs)
		a.Wall += st.Wall
		a.GenTokens += st.GenTokens
		if err != nil {
			a.Failure = err.Error()
			return a
		}
		a.Reply = reply
		a.Pipeline = extractPipeline(reply)
		a.Unsupported = unsupported.MatchString(reply)
		if a.Pipeline == "" || a.Unsupported {
			return a // prose, or a refusal
		}
		problem := f.check(&a)
		if problem == "" {
			return a
		}
		if strings.Contains(problem, "no such file or directory") {
			// An example over a file the question imagined (`from tsv
			// data.tsv`): no correction can make it exist. Shown as an
			// illustration, not run (an eval data case still needs a run).
			a.Illustrative, a.Failure = true, ""
			return a
		}
		a.Failure = problem
		if turn == f.retries {
			return a
		}
		a.Retries++
		if f.verbose {
			fmt.Fprintf(os.Stderr, "  retry %d: %s\n    after: %s\n", a.Retries, firstLine(problem), a.Pipeline)
		}
		msgs = append(msgs, message{Role: "assistant", Content: reply},
			message{Role: "user", Content: f.feedback(a.Pipeline, problem)})
	}
}

// check validates, pre-flights in schema mode, and runs the pipeline. It
// returns what went wrong, in ssql's words where ssql said it.
func (f *faq) check(a *answer) string {
	if _, err := validatePipeline(a.Pipeline); err != nil {
		return "the pipeline cannot be run: " + err.Error()
	}
	// Schema mode answers from the headers in milliseconds, and refuses an
	// unknown column with the list of real ones (DFC170).
	if r := f.box.run(a.Pipeline+" | ssql generate schema", "SSQL_MODE=schema"); r.err != nil {
		return strings.TrimSpace(r.stderr)
	}
	r := f.box.run(a.Pipeline)
	if r.err != nil {
		msg := strings.TrimSpace(r.stderr)
		if msg == "" {
			msg = r.err.Error()
		}
		return msg
	}
	a.Ran, a.Output, a.Failure = true, r.stdout, ""
	return ""
}

// feedback is the correction turn: the error, and the authoritative help of
// the commands the pipeline used (generated from the grammar, so it cannot
// drift from the binary; DFC169 §4.0).
func (f *faq) feedback(pipeline, problem string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "That pipeline failed:\n\n%s\n", problem)
	cmds, _ := validatePipeline(pipeline)
	sort.Strings(cmds)
	for _, c := range cmds {
		if r := f.box.run("ssql " + c + " -help"); r.err == nil {
			fmt.Fprintf(&sb, "\nHere is `ssql %s -help`:\n\n%s\n", c, clip(r.stdout, 4000))
		}
	}
	sb.WriteString("\nFix the pipeline and answer again in the same format.")
	return sb.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…"
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
