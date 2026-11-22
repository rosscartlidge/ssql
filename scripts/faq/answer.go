package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	repo    string          // the ssql checkout (docs, prompt, docsearch)
	search  string          // the docsearch command (default: the checkout's scripts/docsearch.sh)
	install map[string]bool // the install docs' command lines (installLines)
	box     sandbox
	retries int
	docsK   int
	system  string // the CLI prompt plus the sample data's schemas
	latest  string // the latest release, X.Y.Z (the eval's {{latest}})
	keys    string // the shell key bindings, as ssql -help-keybinding lists them
	review  bool   // a pipeline that ran is shown its output and the question once more
	verbose bool
}

type answer struct {
	Reply        string        // the model's final text
	Pipeline     string        // its pipeline ("" for prose or a refusal)
	Unsupported  bool          // the model said ssql cannot do it
	Ran          bool          // the pipeline passed the pre-flight and ran
	Checked      bool          // a visitor's columns: it passed schema mode, with no rows to run
	RanOn        string        // "sample", "your-columns" or "your-rows" once verified
	Illustrative bool          // it names a file the sample data lacks: shown, not run
	Setup        bool          // shell setup (eval "$(ssql -FLAG)", install), not a pipeline
	Revised      bool          // the review turn replaced the first pipeline that ran
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
- Shell setup is a line for ~/.bashrc, not a pipeline: give it alone in its
  block, as the docs write it, e.g. ` + "`eval \"$(ssql -shell-init)\"`" + ` (double
  quotes), which turns on completion and every key binding (Ctrl-O completes
  field names from the upstream pipeline).
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
	latest, date, notes, err := latestRelease(filepath.Join(repo, "CHANGELOG.md"))
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&sb, `
## Facts

- The latest release of ssql is v%s, released %s. CHANGELOG.md lists what each release changed.
- Install it with: go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest
- `+"`ssql version`"+` prints the version of the ssql a user has installed, which may be older.

What v%s changed (its CHANGELOG section; for earlier releases, see CHANGELOG.md):

%s
`, latest, date, latest, clip(notes, 3000))
	path, err := readmeSection(filepath.Join(repo, "README.md"), "## Learning Path")
	if err != nil {
		return nil, err
	}
	// Visitors are CLI users unless they say Go: "What's the best codelab to
	// do first" was answered with the Go codelab (2026-11-17). The README's
	// learning path is the authority on the order.
	fmt.Fprintf(&sb, `
- ssql is a command-line tool first. Answer for the CLI unless the question asks about the Go package, the library or Go code.
- Where to start, in order (README.md's learning path; links are relative to README.md):

%s
`, path)
	install, err := installLines(repo)
	if err != nil {
		return nil, err
	}
	keys, err := keyBindings(box)
	if err != nil {
		return nil, err
	}
	// The key list is not in the prompt: every question would carry it, and
	// it moved three data answers in the eval (2026-11-17). A reply naming a
	// wrong key gets it in the correction turn (keysFeedback).
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
		fmt.Fprintf(&sb, "- `%s`: %s%s\n", name, strings.Join(cols, ", "), sampleNotes[name])
	}
	return &faq{repo: repo, box: box, retries: retries, docsK: docsK, system: sb.String(), verbose: verbose, latest: latest, keys: keys, install: install}, nil
}

// releaseHeading is a released CHANGELOG section: ## [4.148.0] - 2026-11-15.
var releaseHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})`)

// latestRelease is the first released section of the CHANGELOG, and its
// text: the facts block states both, so "what is the latest release?" and
// "what changed in it?" have answers that follow every release without
// touching the prompt. (Retrieval alone answered the second from another
// release's section, 2026-11-16.)
func latestRelease(changelog string) (version, date, notes string, err error) {
	b, err := os.ReadFile(changelog)
	if err != nil {
		return "", "", "", err
	}
	m := releaseHeading.FindSubmatchIndex(b)
	if m == nil {
		return "", "", "", fmt.Errorf("%s: no released section (## [X.Y.Z] - DATE)", changelog)
	}
	rest := b[m[1]:]
	if next := regexp.MustCompile(`(?m)^## \[`).FindIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return string(b[m[2]:m[3]]), string(b[m[4]:m[5]]), strings.TrimSpace(string(rest)), nil
}

// userDocs are the sections an answer may cite: the user documentation,
// not research DFCs or journals, where superseded plans read as current
// (DFC169 §4). Nor the CHANGELOG: an old release's section reads as current
// too (v4.47's Ctrl-X Ctrl-F, rebound to Ctrl-O since, was given as the key
// on 2026-11-17), and the latest release's section is in the facts block.
var userDocs = regexp.MustCompile(`^(doc/[^/]+\.md|README\.md):(\d+)`)

// retrieve runs docsearch and returns the top user-doc sections, each as
// its heading line through the next heading, capped.
func (f *faq) retrieve(question string) (refs []string, text string) {
	if f.docsK == 0 {
		return nil, ""
	}
	search := f.search
	if search == "" {
		search = filepath.Join(f.repo, "scripts", "docsearch.sh")
	}
	cmd := exec.Command(search, "-k", "40", question)
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

// ask answers one question with one model, over the sample data, or over
// the columns (and rows) a visitor pasted when v is not nil.
func (f *faq) ask(llm ollama, question string, v *visitor) answer {
	return f.askAfter(llm, question, v, nil)
}

// exchange is an earlier question and the reply it got: a follow-up
// ("how does it do that?") is asked after it, as a conversation.
type exchange struct {
	Question string `json:"question"`
	Reply    string `json:"reply"`
}

// askAfter is ask with the previous exchange, when the question follows
// one. Retrieval sees both questions, so "how does it do the conversion?"
// finds what "can I turn a pipeline into Go?" found.
func (f *faq) askAfter(llm ollama, question string, v *visitor, prev *exchange) answer {
	var a answer
	query := question
	if prev != nil {
		query = prev.Question + " " + question
	}
	refs, docs := f.retrieve(query)
	a.Docs = refs
	user, box := question, f.box
	if v != nil || docs != "" {
		user = "Question: " + question
	}
	if v != nil {
		box = f.box.withData(v.dir)
		user = v.prompt() + "\n\n" + user
	}
	if docs != "" {
		user = "Documentation that may help:\n" + docs + "\n\n" + user
	}
	msgs := []message{{Role: "system", Content: f.system}}
	if prev != nil {
		msgs = append(msgs, message{Role: "user", Content: prev.Question}, message{Role: "assistant", Content: prev.Reply})
	}
	msgs = append(msgs, message{Role: "user", Content: user})
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
		if a.Unsupported {
			return a // a refusal
		}
		problem := f.checkKeys(reply)
		switch {
		case problem != "":
			a.Ran, a.Checked = false, false
		case a.Pipeline == "":
			a.Failure = ""
			return a // prose
		case setupLines(reply, f.install) != nil:
			lines := setupLines(reply, f.install)
			a.Setup, a.Ran, a.RanOn, a.Output = true, false, "", ""
			problem, a.Ran = box.checkSetup(lines)
			if a.Ran {
				a.RanOn = "setup"
			}
		default:
			a.Setup = false
			problem = f.check(&a, box, v)
		}
		if problem == "" {
			if f.review && a.Ran && !a.Setup && a.RanOn != "generated" {
				return f.reviewed(llm, append(msgs, message{Role: "assistant", Content: reply}), question, a, box, v)
			}
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
		fb := f.feedback(a.Pipeline, problem, v)
		if strings.HasPrefix(problem, keysProblem) {
			fb = f.keysFeedback(problem)
		}
		msgs = append(msgs, message{Role: "assistant", Content: reply}, message{Role: "user", Content: fb})
	}
}

// check validates, pre-flights in schema mode, and runs the pipeline. It
// returns what went wrong, in ssql's words where ssql said it. A visitor's
// pasted columns with no rows stop at schema mode: there is nothing to run.
func (f *faq) check(a *answer, box sandbox, v *visitor) string {
	run := runnable(a.Pipeline)
	if _, err := validatePipeline(run); err != nil {
		return "the pipeline cannot be run: " + err.Error()
	}
	run, generated := shownGo(run)
	// Schema mode answers from the headers in milliseconds, and refuses an
	// unknown column with the list of real ones (DFC170). A program is not
	// rows: generate go checks the pipeline it is given itself.
	if !generated {
		if r := box.run(run+" | ssql generate schema", "SSQL_MODE=schema"); r.err != nil {
			return strings.TrimSpace(r.stderr)
		}
	}
	mine := v != nil && v.readBy(a.Pipeline)
	if mine && v.rows == 0 {
		a.Checked, a.RanOn, a.Failure = true, "your-columns", ""
		return ""
	}
	r := box.run(run)
	if r.err != nil {
		msg := strings.TrimSpace(r.stderr)
		if msg == "" {
			msg = r.err.Error()
		}
		return msg
	}
	a.Ran, a.Output, a.Failure, a.RanOn = true, r.stdout, "", "sample"
	if generated {
		a.RanOn = "generated"
	}
	if mine {
		a.RanOn = "your-rows"
	}
	return ""
}

// feedback is the correction turn: the error, and the authoritative help of
// the commands the pipeline used (generated from the grammar, so it cannot
// drift from the binary; DFC169 §4.0).
func (f *faq) feedback(pipeline, problem string, v *visitor) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "That pipeline failed:\n\n%s\n", problem)
	if v != nil {
		sb.WriteString("\nRemember: the visitor's data is data.csv, with the columns given above.\n")
	}
	cmds, _ := validatePipeline(runnable(pipeline))
	sort.Strings(cmds)
	for _, c := range cmds {
		if r := f.box.run("ssql " + c + " -help"); r.err == nil {
			fmt.Fprintf(&sb, "\nHere is `ssql %s -help`:\n\n%s\n", c, clip(r.stdout, 4000))
		}
	}
	// The visitor sees only the final reply: one that explained this
	// correction ("the error occurred because I provided curl…") answered
	// a question nobody asked (2026-11-16).
	sb.WriteString("\nFix the pipeline and answer the visitor's question again, in the same format. " +
		"The visitor never saw the failed answer or this message: do not mention them.")
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

// keyBindings is the cheat-sheet's rows from `ssql -help-keybinding` (the
// KeyBindings table, so it cannot drift from the binary).
func keyBindings(box sandbox) (string, error) {
	r := box.run("ssql -help-keybinding")
	if r.err != nil {
		return "", fmt.Errorf("ssql -help-keybinding: %v: %s", r.err, r.stderr)
	}
	var rows []string
	for _, ln := range strings.Split(r.stdout, "\n") {
		if keyRow.MatchString(ln) {
			rows = append(rows, strings.TrimSpace(ln))
		}
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("ssql -help-keybinding lists no keys")
	}
	return strings.Join(rows, "\n"), nil
}

var (
	keyRow = regexp.MustCompile(`^\s{2}(Ctrl|Alt)-\S`)
	// keyNamed is a key a reply names: Ctrl-O, Ctrl+O, Alt-g, Ctrl-X h,
	// Ctrl-X Ctrl-E.
	keyNamed = regexp.MustCompile(`\b(?:Ctrl|Alt)[-+][A-Za-z](?:\s+(?:Ctrl[-+][A-Za-z]|[A-Za-z])\b)?`)
	// terminalKeys are bash's and the terminal's own, fine to mention.
	terminalKeys = map[string]bool{"CTRL-C": true, "CTRL-D": true, "CTRL-Z": true, "CTRL-R": true, "CTRL-L": true,
		"CTRL-A": true, "CTRL-E": true, "CTRL-U": true, "CTRL-W": true, "CTRL-K": true}
)

const keysProblem = "names keys ssql does not bind: "

// checkKeys refuses a reply that names a key ssql does not bind.
func (f *faq) checkKeys(reply string) string {
	if f.keys == "" {
		return ""
	}
	var bad []string
	for _, k := range keyNamed.FindAllString(reply, -1) {
		k = strings.Join(strings.Fields(strings.ReplaceAll(k, "+", "-")), " ")
		if len(k) > 5 && k[len(k)-2] == ' ' && !strings.HasPrefix(k, "Ctrl-X") {
			k = k[:len(k)-2] // "Ctrl-O completes": the key, then a word
		}
		if !terminalKeys[strings.ToUpper(k)] && !strings.Contains(f.keys, k) && !slices.Contains(bad, k) {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return keysProblem + strings.Join(bad, ", ")
}

func (f *faq) keysFeedback(problem string) string {
	return "That answer " + problem + ". These are all the keys ssql binds (from `ssql -help-keybinding`):\n\n" + f.keys +
		"\n\nAnswer the visitor's question again, in the same format, naming only these keys. " +
		"The visitor never saw the failed answer or this message: do not mention them."
}

// readmeSection is a README section's body: the lines after heading, up to
// the next heading of the same level.
func readmeSection(path, heading string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	_, rest, ok := strings.Cut(string(b), "\n"+heading+"\n")
	if !ok {
		return "", fmt.Errorf("%s has no %q section", path, heading)
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), nil
}

// reviewOK is a review reply that keeps the answer.
var reviewOK = regexp.MustCompile(`(?i)^[\s*_"'` + "`" + `]*ok\b`)

// reviewed shows the model what its pipeline printed, beside the question,
// once: right rows in the wrong shape was every eval miss on 2026-11-21 (a
// column not asked for, a month written 2024-11-01T00:00:00Z where the
// question said 2024-01). A revision replaces the answer only when it
// passes every check the first one did; anything else keeps the first.
func (f *faq) reviewed(llm ollama, msgs []message, question string, a answer, box sandbox, v *visitor) answer {
	out := strings.Split(strings.TrimSpace(a.Output), "\n")
	if len(out) > 12 {
		out = append(out[:12], "…")
	}
	msgs = append(msgs, message{Role: "user", Content: fmt.Sprintf("Your pipeline ran. Its first lines of output:\n\n```\n%s\n```\n\n"+
		"The question was: %s\n\nCheck the output against the question: the columns it asks to see and no others, "+
		"and values written the way it asks. If the output already answers it, reply with exactly OK. "+
		"If not, give your whole answer again, corrected, in the same format. "+
		"The visitor never saw this message: do not mention it.", strings.Join(out, "\n"), question)})
	reply, st, err := llm.chat(msgs)
	a.Wall += st.Wall
	a.GenTokens += st.GenTokens
	p := extractPipeline(reply)
	if err != nil || reviewOK.MatchString(reply) || p == "" || runnable(p) == runnable(a.Pipeline) ||
		unsupported.MatchString(reply) || setupLines(reply, f.install) != nil || f.checkKeys(reply) != "" {
		return a
	}
	b := a
	b.Reply, b.Pipeline, b.Ran, b.Checked, b.Output, b.RanOn = reply, p, false, false, "", ""
	if f.check(&b, box, v) != "" || !b.Ran {
		return a
	}
	b.Revised = true
	return b
}
