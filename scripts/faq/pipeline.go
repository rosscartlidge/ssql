package main

import (
	"bytes"
	"context"
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
	m := fenced.FindStringSubmatch(reply)
	if m == nil {
		return ""
	}
	var lines []string
	for _, ln := range strings.Split(m[1], "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		lines = append(lines, strings.TrimSpace(strings.TrimSuffix(ln, "\\")))
	}
	return strings.TrimSpace(strings.Join(lines, " "))
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

// validatePipeline checks the text is ssql stages only, and returns the
// commands it uses (for -help on a retry).
func validatePipeline(p string) ([]string, error) {
	cmds := map[string]bool{}
	if err := validateStages(p, cmds, 0); err != nil {
		return nil, err
	}
	var out []string
	for c := range cmds {
		out = append(out, c)
	}
	return out, nil
}

func validateStages(p string, cmds map[string]bool, depth int) error {
	if depth > 2 {
		return fmt.Errorf("process substitution nested too deep")
	}
	stages, err := splitTop(p)
	if err != nil {
		return err
	}
	for _, st := range stages {
		words, subs, err := words(st)
		if err != nil {
			return err
		}
		if len(words) == 0 {
			return fmt.Errorf("an empty stage")
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
			if err := validateStages(s, cmds, depth+1); err != nil {
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
	script := "set -o pipefail; " + pipeline
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
// user scope (at most 64 tasks, 1 GiB, no swap), prlimit (a 64 MiB file,
// 30 s of CPU), then bubblewrap with every namespace unshared, so no
// network, the host's /usr read-only, the scratch directory as /work, and
// ssql at /run/ssql/ssql; everything but /work and /tmp is read-only.
func jailArgs(ssql, dir, script string, env []string) []string {
	args := []string{
		"--user", "--scope", "--quiet", "--collect",
		"-p", "TasksMax=64", "-p", "MemoryMax=1G", "-p", "MemorySwapMax=0",
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
