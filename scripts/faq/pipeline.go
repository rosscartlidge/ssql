package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A model's pipeline is shell text, and nothing a model writes is run by a
// shell before this file has read it (DFC169 §4.1). The accepted language
// is ssql stages joined by `|`, words quoted with ' or ", and `<( … )` whose
// inside is again such a pipeline (join's right side). Everything else a
// shell would honour — `;`, `&`, `$`, backticks, redirection, a command
// other than ssql, an absolute path or `..` in an argument — is refused,
// with the reason, so the refusal can go back to the model.

var fenced = regexp.MustCompile("(?s)```(?:bash|sh|shell)?\\s*\\n(.*?)```")

// extractPipeline is the first fenced block's pipeline (continuation lines
// joined), or "" when the reply has none.
func extractPipeline(reply string) string {
	return strings.Join(blockCommands(reply), " ")
}

// blockCommands is the commands in the reply's first bash block. A line
// continues the one before when that ends in \ or |, or when it starts
// with |; any other line starts a new command. Joined blindly, two
// commands made one line no correction could fix (2026-11-23).
func blockCommands(reply string) []string {
	m := fenced.FindStringSubmatch(reply)
	if m == nil {
		return nil
	}
	var cmds []string
	open := false // the last command continues onto the next line
	for _, ln := range strings.Split(m[1], "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		cont := strings.HasSuffix(ln, "\\")
		ln = strings.TrimSpace(strings.TrimSuffix(ln, "\\"))
		if len(cmds) > 0 && (open || strings.HasPrefix(ln, "|")) {
			cmds[len(cmds)-1] += " " + ln
		} else {
			cmds = append(cmds, ln)
		}
		open = cont || strings.HasSuffix(ln, "|")
	}
	return cmds
}

// saveTo is a trailing `> FILE` (or `>> FILE`) naming a plain file in the
// working directory.
var saveTo = regexp.MustCompile(`\s*>>?\s*([A-Za-z0-9_][A-Za-z0-9_.-]*)\s*$`)

// runnable is the pipeline to verify: the text without a trailing
// redirection to a plain file name. "Save it with > report.go" is ordinary
// advice, and gemma kept it through every correction (2026-11-16); what is
// verified is the pipeline that produces the file. The validator still
// sees any other `>` (a second one, `2>&1`, a path) and refuses it.
func runnable(p string) string {
	if m := saveTo.FindStringSubmatchIndex(p); m != nil && !strings.Contains(p[m[2]:m[3]], "..") {
		return strings.TrimSpace(p[:m[0]])
	}
	return p
}

// shownProgram is the pipeline with each `ssql generate go` or `generate
// rust` stage's -run and -build BIN (-r, -b) dropped, so the FAQ shows the
// program instead of compiling it: the jail has no network to fetch a
// toolchain or a module, and a visitor asking for code wants the code
// (Ross, 2026-11-22, the first public question: "Can I turn a pipeline
// into a Go program?" came back unverified with go's download error; on
// 2026-11-24 "how do you create rust code?" did the same with "cargo not
// found"). `generate sql -run` and a bare `| duckdb` after `generate sql`
// (the two forms the docs teach) need DuckDB, which the jail lacks too, so
// the SQL is shown instead. The reply keeps the command the model wrote,
// which is right on the visitor's machine. lang is the program's
// language, Go, Rust or SQL, or "" when nothing was dropped.
func shownProgram(p string) (shown, lang string) {
	stages, err := splitTop(p)
	if err != nil {
		return p, ""
	}
	for i := 0; i < len(stages); i++ {
		ws, subs, err := words(stages[i])
		if err != nil || len(subs) > 0 || len(ws) < 3 || ws[0] != "ssql" || ws[1] != "generate" || programLang[ws[2]] == "" {
			continue
		}
		if ws[2] == "sql" && i == len(stages)-2 && strings.TrimSpace(stages[i+1]) == "duckdb" {
			stages, lang = stages[:i+1], "SQL"
		}
		kept := []string{"ssql", "generate", ws[2]}
		dropped := false
		for j := 3; j < len(ws); j++ {
			switch ws[j] {
			case "-run", "-r":
				dropped = true
			case "-build", "-b":
				if ws[2] == "sql" { // no -build: leave it to the command's error
					kept = append(kept, shellQuote(ws[j]))
					continue
				}
				dropped = true
				j++ // and the binary's name
			default:
				kept = append(kept, shellQuote(ws[j]))
			}
		}
		if dropped {
			stages[i], lang = strings.Join(kept, " "), programLang[ws[2]]
		}
	}
	return strings.Join(stages, " | "), lang
}

// programLang names the generate targets whose -run builds a program.
var programLang = map[string]string{"go": "Go", "rust": "Rust", "sql": "SQL"}

// shellQuote is w as one shell word: bare when it is plainly safe, else in
// single quotes.
func shellQuote(w string) string {
	if w != "" && strings.Trim(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.,/=:@+") == "" {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// validatePipeline checks the text is ssql stages only, and returns the
// commands it uses (for -help on a retry).
func validatePipeline(p string) ([]string, error) {
	cmds := map[string]bool{}
	if err := validateStages(p, cmds, 0, false); err != nil {
		return nil, err
	}
	var out []string
	for c := range cmds {
		out = append(out, c)
	}
	return out, nil
}

func validateStages(p string, cmds map[string]bool, depth int, docSub bool) error {
	if depth > 2 {
		return fmt.Errorf("process substitution nested too deep")
	}
	stages, err := splitTop(p)
	if err != nil {
		return err
	}
	for i, st := range stages {
		words, subs, err := words(st)
		if err != nil {
			return err
		}
		if len(words) == 0 {
			return fmt.Errorf("an empty stage")
		}
		// echo hands a pipeline document to ssql run, as the docs do:
		// `echo '[…]' | ssql run -stdin` or `ssql run <(echo '[…]')`.
		if words[0] == "echo" && i == 0 && (docSub && len(stages) == 1 || len(stages) > 1 && feedsRun(stages[1])) {
			if err := validateDocument(words[1:]); err != nil {
				return err
			}
			continue
		}
		if words[0] != "ssql" {
			return fmt.Errorf("every stage must be an ssql command; %q is not", words[0])
		}
		if len(words) > 1 {
			cmds[words[1]] = true
		}
		for _, w := range words[1:] {
			if strings.HasPrefix(w, "/") || strings.Contains(w, "..") {
				return fmt.Errorf("argument %q: only the sample files in the working directory can be read", w)
			}
		}
		for _, s := range subs {
			if err := validateStages(s, cmds, depth+1, len(words) > 1 && words[1] == "run"); err != nil {
				return err
			}
		}
	}
	return nil
}

// splitTop splits at the pipes outside quotes and parentheses.
func splitTop(p string) ([]string, error) {
	var stages []string
	var quote byte
	depth, start := 0, 0
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case quote == '"' && c == '\\':
			i++ // an escaped character inside double quotes
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == '|' && depth == 0:
			stages = append(stages, strings.TrimSpace(p[start:i]))
			start = i + 1
		}
	}
	if quote != 0 || depth != 0 {
		return nil, fmt.Errorf("unbalanced quotes or parentheses")
	}
	return append(stages, strings.TrimSpace(p[start:])), nil
}

// words splits a stage into its words, unquoting, and lifts out each
// `<( … )` as a sub-pipeline. A character a shell would act on outside
// quotes is refused.
// echoesDocument reports whether the pipeline hands ssql run a document
// with echo, which the playground cannot run (it has no echo).
func echoesDocument(p string) bool {
	return echoStage.MatchString(p)
}

var echoStage = regexp.MustCompile(`(^|\||<\()\s*echo\s`)

// feedsRun reports whether stage is `ssql run …`.
func feedsRun(stage string) bool {
	ws, _, err := words(stage)
	return err == nil && len(ws) > 1 && ws[0] == "ssql" && ws[1] == "run"
}

// validateDocument checks echo's arguments are one pipeline document (a
// JSON list of argument lists) whose arguments obey the rule an ssql
// stage's do: only the sample files in the working directory.
func validateDocument(args []string) error {
	var doc [][]string
	if len(args) != 1 || json.Unmarshal([]byte(args[0]), &doc) != nil || len(doc) == 0 {
		return fmt.Errorf("echo may only hand ssql run one pipeline document, a JSON list of argument lists in single quotes")
	}
	for _, stage := range doc {
		for _, w := range stage {
			if strings.HasPrefix(w, "/") || strings.Contains(w, "..") {
				return fmt.Errorf("argument %q: only the sample files in the working directory can be read", w)
			}
		}
	}
	return nil
}

func words(st string) (out, subs []string, err error) {
	var w strings.Builder
	in := false
	flush := func() {
		if in {
			out = append(out, w.String())
			w.Reset()
			in = false
		}
	}
	for i := 0; i < len(st); i++ {
		c := st[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(st[i+1:], c)
			if j < 0 {
				return nil, nil, fmt.Errorf("unterminated quote")
			}
			w.WriteString(st[i+1 : i+1+j]) // single quotes: every byte literal
			in = true
			i += j + 1
		case c == '"':
			// Double quotes, as bash reads them: \" \\ \$ \` are the
			// character; a bare $ or ` would expand, so it is refused.
			in = true
			closed := false
			for i++; i < len(st); i++ {
				d := st[i]
				if d == '"' {
					closed = true
					break
				}
				if d == '\\' && i+1 < len(st) && strings.IndexByte("\"\\$`", st[i+1]) >= 0 {
					i++
					w.WriteByte(st[i])
					continue
				}
				if d == '$' || d == '`' {
					return nil, nil, fmt.Errorf("%q inside double quotes is shell expansion; use single quotes", string(d))
				}
				w.WriteByte(d)
			}
			if !closed {
				return nil, nil, fmt.Errorf("unterminated quote")
			}
		case c == '<' && i+1 < len(st) && st[i+1] == '(':
			depth, j := 1, i+2
			for ; j < len(st) && depth > 0; j++ {
				switch st[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth != 0 {
				return nil, nil, fmt.Errorf("unterminated <(")
			}
			subs = append(subs, st[i+2:j-1])
			w.WriteString("<(…)")
			in = true
			i = j - 1
		case c == ' ' || c == '\t':
			flush()
		case strings.IndexByte(";&$`><()\\{}", c) >= 0:
			return nil, nil, fmt.Errorf("%q is shell syntax; only ssql stages joined by | are run", string(c))
		default:
			w.WriteByte(c)
			in = true
		}
	}
	flush()
	return out, subs, nil
}

// sandbox runs validated pipelines in a scratch copy of the data, with
// only ssql (and bash, which ssql uses for its own stages) on PATH and a time
// limit. With jail set (DFC169 §4.1, phase 2) the run happens inside
// bubblewrap: no network, nothing of the host but /usr read-only and the
// scratch directory, inside a user systemd scope that caps its tasks and
// memory. The validator is the first wall; the jail is the second, for
// whatever the validator misses.
type sandbox struct {
	ssql    string // absolute path of the ssql binary
	dataDir string // the files a pipeline may read
	timeout time.Duration
	jail    bool
}

type runResult struct {
	stdout, stderr string
	err            error
}

// jailTools are the programs a jailed run needs on the host.
var jailTools = []string{"bwrap", "systemd-run", "prlimit"}

// jailAvailable reports the first tool the jail lacks, or "".
func jailAvailable() string {
	for _, t := range jailTools {
		if _, err := exec.LookPath(t); err != nil {
			return t
		}
	}
	return ""
}

// withData is the same sandbox reading another directory's files.
func (s sandbox) withData(dir string) sandbox {
	s.dataDir = dir
	return s
}

// stageStatus follows a pipeline and decides its exit status from every
// stage's. The last stage must succeed. A stage before it may succeed or
// die of SIGPIPE (141), which is what a stage gets when a later one stops
// reading: limit, or generate schema, over a file larger than a pipe
// buffer. Any other status fails the run. pipefail called the SIGPIPE a
// failure, and cast.csv was the first sample large enough to show it
// (2026-11-26, TestRunAcceptsAnEarlyStop).
const stageStatus = `st=("${PIPESTATUS[@]}"); last=${st[${#st[@]}-1]}
[ "$last" = 0 ] || exit "$last"
for x in "${st[@]}"; do [ "$x" = 0 ] || [ "$x" = 141 ] || exit "$x"; done`

func (s sandbox) run(pipeline string, env ...string) runResult {
	dir, err := os.MkdirTemp("", "faq-run-")
	if err != nil {
		return runResult{err: err}
	}
	defer os.RemoveAll(dir)
	files, _ := filepath.Glob(filepath.Join(s.dataDir, "*"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	script := pipeline + "\n" + stageStatus
	var cmd *exec.Cmd
	if s.jail {
		cmd = exec.CommandContext(ctx, "systemd-run", jailArgs(s.ssql, dir, script, env)...)
	} else {
		bin := filepath.Join(dir, ".bin")
		os.Mkdir(bin, 0o755)
		if err := os.Symlink(s.ssql, filepath.Join(bin, "ssql")); err != nil {
			return runResult{err: err}
		}
		cmd = exec.CommandContext(ctx, "/bin/bash", "-c", script)
		cmd.Dir = dir
		// ssql first; /usr/bin:/bin after it because ssql runs its own
		// stages through bash (generate -pipeline, process substitution).
		// The text a model wrote never reaches them: it was validated to
		// ssql stages.
		cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + dir, "TZ=UTC"}, env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %v", s.timeout)
	}
	return runResult{stdout.String(), stderr.String(), err}
}

// jailArgs is systemd-run's argument list for one jailed run: a transient
// user scope (at most 256 tasks, 1 GiB, no swap), prlimit (a 64 MiB file,
// 30 s of CPU), then bubblewrap with every namespace unshared, so no
// network, the host's /usr read-only, the scratch directory as /work, and
// ssql at /run/ssql/ssql; everything but /work and /tmp is read-only.
// GOMAXPROCS=2: every stage is a Go process with a thread per core by
// default (24 on derra), so under the old 64 tasks a four-stage pipeline
// failed now and then with "pthread_create failed"; the sample data is
// small enough that two threads lose nothing.
func jailArgs(ssql, dir, script string, env []string) []string {
	args := []string{
		"--user", "--scope", "--quiet", "--collect",
		"-p", "TasksMax=256", "-p", "MemoryMax=1G", "-p", "MemorySwapMax=0",
		"--",
		"prlimit", "--fsize=67108864", "--cpu=30", "--",
		"bwrap", "--unshare-all", "--die-with-parent", "--new-session",
		"--ro-bind", "/usr", "/usr",
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/lib", "/lib",
		"--symlink", "usr/lib64", "/lib64",
		"--ro-bind", ssql, "/run/ssql/ssql",
		"--bind", dir, "/work",
		"--chdir", "/work",
		"--tmpfs", "/tmp",
		"--proc", "/proc",
		"--dev", "/dev",
		"--remount-ro", "/", // the root bwrap built: only /work and /tmp stay writable
		"--clearenv",
		"--setenv", "PATH", "/run/ssql:/usr/bin:/bin",
		"--setenv", "HOME", "/work",
		"--setenv", "TZ", "UTC",
		"--setenv", "GOMAXPROCS", "2",
	}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		args = append(args, "--setenv", k, v)
	}
	return append(args, "/bin/bash", "-c", script)
}

// dataFor turns a pipeline's output into JSONL rows for comparison. A
// final sink that writes to stdout is replaced: by `to jsonl`, or, when it
// formats times (-time-format), by `to csv` with the same format read back,
// so the rows hold what the answer displays (2024-01, not a timestamp). A
// `count` is compared as its text; a sink that names a file is left alone
// (and compares as no rows).
func dataFor(pipeline string) string {
	stages, err := splitTop(pipeline)
	if err != nil || len(stages) == 0 {
		return pipeline
	}
	ws, _, _ := words(stages[len(stages)-1])
	if len(ws) >= 2 && ws[1] == "count" {
		return pipeline // a number, compared as text
	}
	if len(ws) >= 3 && ws[1] == "to" {
		timeFormat, file := "", false
		for i := 3; i < len(ws); i++ {
			switch ws[i] {
			case "-time-format":
				if i+1 < len(ws) {
					timeFormat = ws[i+1]
				}
				i++
			case "-max-width", "-sample":
				i++
			default:
				if !strings.HasPrefix(ws[i], "-") {
					file = true
				}
			}
		}
		if !file {
			stages = stages[:len(stages)-1]
			if timeFormat != "" {
				return strings.Join(stages, " | ") + " | ssql to csv -time-format '" + timeFormat + "' | ssql from csv | ssql to jsonl"
			}
		}
	}
	return strings.Join(stages, " | ") + " | ssql to jsonl"
}

// A shell setup answer is not a pipeline: "is there pipeline-aware field
// completion in bash?" is answered by `eval "$(ssql -shell-init)"` in
// ~/.bashrc, and until 2026-11-17 the validator refused it ("eval is not
// an ssql command") through every correction. A fenced block whose every
// line is an `eval "$(ssql -FLAG)"` or a documented install command is a
// setup block: each eval is checked (ssql prints the script, bash parses
// it), an install line is shown as it is.
var (
	// The codelab's form guards the eval: command -v ssql >/dev/null && eval ….
	setupEval    = regexp.MustCompile(`^(?:command -v ssql\s*>\s*/dev/null\s*&&\s*)?eval\s+(["']?)\$\(ssql\s+(-[a-z][a-z-]*)\)(["']?)$`)
	setupInstall = regexp.MustCompile(`^(go install github\.com/rosscartlidge/ssql/v4/cmd/ssql(@\S+)?|sudo dpkg -i ssql(-gpu)?_[0-9.]+_amd64\.deb)$`)
	trailingNote = regexp.MustCompile(`\s+#.*$`)
)

// installDocs are the files that own the install commands.
var installDocs = []string{"README.md", "doc/install.md"}

// installLines is every line of the install docs' code blocks, split at
// &&, so an install line in a reply is accepted when the docs say it
// (Ross's log, 2026-11-22: the README's own `brew tap rosscartlidge/ssql
// && brew install ssql` was refused as a pipeline, twice corrected, and
// came back unverified). The lines are shown to the visitor, never run.
func installLines(repo string) (map[string]bool, error) {
	known := map[string]bool{}
	for _, name := range installDocs {
		b, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			return nil, err
		}
		for _, m := range fencedAll.FindAllStringSubmatch(string(b), -1) {
			for _, ln := range strings.Split(m[1], "\n") {
				for _, part := range strings.Split(trailingNote.ReplaceAllString(ln, ""), "&&") {
					if part = strings.TrimSpace(part); part != "" && !strings.HasPrefix(part, "#") {
						known[part] = true
					}
				}
			}
		}
	}
	if !known["go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest"] {
		return nil, fmt.Errorf("the install docs (%s) no longer show `go install …@latest`; installLines reads their code blocks", strings.Join(installDocs, ", "))
	}
	return known, nil
}

// fencedAll is any fenced block, whatever its language.
var fencedAll = regexp.MustCompile("(?s)```[a-z]*\\s*\\n(.*?)```")

// setupLines is the reply's first fenced block as setup lines, or nil when
// any line is something else: a setup eval, or install commands (joined by
// && or not) that the install docs show, or a .deb line of any version.
func setupLines(reply string, install map[string]bool) []string {
	m := fenced.FindStringSubmatch(reply)
	if m == nil {
		return nil
	}
	var lines []string
	for _, ln := range strings.Split(m[1], "\n") {
		ln = strings.TrimSpace(trailingNote.ReplaceAllString(ln, ""))
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if !setupEval.MatchString(ln) && !documented(ln, install) {
			return nil
		}
		lines = append(lines, ln)
	}
	return lines
}

// documented reports whether every &&-joined part of ln is an install line.
func documented(ln string, install map[string]bool) bool {
	for _, part := range strings.Split(ln, "&&") {
		part = strings.TrimSpace(part)
		if !install[part] && !setupInstall.MatchString(part) {
			return false
		}
	}
	return true
}

// checkSetup returns what is wrong with a setup block, in words for the
// model, and whether any line was checked by running ssql.
func (s sandbox) checkSetup(lines []string) (problem string, ran bool) {
	for _, ln := range lines {
		m := setupEval.FindStringSubmatch(ln)
		if m == nil {
			continue // an install line
		}
		if m[1] != `"` || m[3] != `"` {
			return fmt.Sprintf("%s: the $(...) must be in double quotes, eval \"$(ssql %s)\": "+
				"in single quotes it never runs, and unquoted its output is split into words", ln, m[2]), false
		}
		// The flag is a word of lowercase letters and dashes (the regexp),
		// so this text is safe to hand to bash.
		if r := s.run("ssql " + m[2] + " > setup.sh && bash -n setup.sh"); r.err != nil {
			msg := strings.TrimSpace(r.stderr)
			if msg == "" {
				msg = r.err.Error()
			}
			return fmt.Sprintf("%s: %s", ln, msg), false
		}
		ran = true
	}
	return "", ran
}
