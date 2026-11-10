package commands

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// Ctrl-O value completion samples the upstream's own output (see
// ValueSourceFile). Running a pipeline on a keypress is bounded three
// ways: only ssql commands that do not write (upstreamSafeToRun), at most
// valueSampleRecords records (autocli samples no more from a file), and
// valueSampleTimeout of wall — a streaming upstream has its sample in a
// fraction of a second (0.14 s over 1.2 GB of CSV, 2026-11-02); a sort or
// group-by over a large file runs out of time and the source file is
// sampled instead.
const (
	valueSampleRecords = 10000
	valueSampleTimeout = 2 * time.Second
)

// sampleUpstream runs src into `ssql limit N`, writing the records to a
// file in the user cache dir that is overwritten on every call (nothing
// is kept across pipelines). false when src may not run, fails, runs out
// of time or yields no record.
func sampleUpstream(src string) (string, bool) {
	if !upstreamSafeToRun(src) {
		return "", false
	}
	self, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	dir = filepath.Join(dir, "ssql")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false
	}
	path := filepath.Join(dir, "ctrl-o-values.jsonl")
	out, err := os.Create(path)
	if err != nil {
		return "", false
	}
	defer out.Close()
	ctx, cancel := context.WithTimeout(context.Background(), valueSampleTimeout)
	defer cancel()
	// No pipefail: the stages before limit end on SIGPIPE once it has
	// its records, and the pipeline's status is limit's.
	cmd := exec.CommandContext(ctx, "bash", "-c", src+" | "+ssql.ShellQuote(self)+" limit "+strconv.Itoa(valueSampleRecords))
	// Data, never code fragments, whatever mode the user's shell exports.
	cmd.Env = append(os.Environ(), "SSQL_MODE=", "SSQLGO=")
	cmd.Stdout = out
	inOwnProcessGroup(cmd)
	if err := cmd.Run(); err != nil || ctx.Err() != nil {
		return "", false
	}
	if !hasRecord(path) {
		return "", false
	}
	return path, true
}

// hasRecord reports whether a JSONL file holds a record beyond its
// `_schema` header.
func hasRecord(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) > 0 && !strings.HasPrefix(string(line), `{"_schema"`) {
			return true
		}
	}
	return false
}

// upstreamSafeToRun reports whether a keypress may run src: every
// command in it — each top-level stage and each stage inside a <(…) —
// is ssql (or ssql_gpu) running a subcommand that writes nothing, and
// nothing outside quotes redirects output, runs a command substitution
// or chains a second command. Anything else is sampled from its source
// file instead.
func upstreamSafeToRun(src string) bool {
	return ssqlCommandsOnly(src, writingSubcommands)
}

// pipelineSafeToDescribe reports whether src may be run in fragment mode
// (SSQL_MODE=record) without the user asking, as the editor binding and
// the language server do: the shell text is upstreamSafeToRun's, but a
// sink may name a file, since in fragment mode a command describes
// itself and writes nothing (`to csv out.csv`, `tee x` and `to parquet`
// each leave no file, checked 2026-11-10). generate and serve still may
// not run.
func pipelineSafeToDescribe(src string) bool {
	return ssqlCommandsOnly(src, map[string]bool{"serve": true, "generate": true})
}

// ssqlCommandsOnly reports whether every command in src — each top-level
// stage and each stage inside a <(…) — is ssql (or ssql_gpu) running a
// subcommand not in refused, and nothing redirects output, substitutes a
// command or chains a second command. bash still substitutes `$(…)` and
// backticks inside double quotes, and a backslash outside single quotes
// makes the next character literal (`\'` opens no quote).
func ssqlCommandsOnly(src string, refused map[string]bool) bool {
	cmds, ok := shellCommandTexts(src)
	if !ok {
		return false
	}
	n := 0
	for _, c := range cmds {
		c = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(c), "<"))
		if c == "" {
			continue
		}
		words := tokenizeStage(c)
		if len(words) < 2 || (words[0] != "ssql" && words[0] != "ssql_gpu") {
			return false
		}
		if refused[words[1]] {
			return false
		}
		n++
	}
	return n > 0
}

// shellCommandTexts splits src into its command texts, false when src
// holds anything that runs more than a pipeline of simple commands: a
// redirection, `;` or `&`, a command substitution (also inside double
// quotes), or an unclosed quote. Text after a `)` continues the command
// the <(…) sat in (`join <(…) -using k`), so it is not one.
func shellCommandTexts(src string) ([]string, bool) {
	var cmds []string
	var cur strings.Builder
	var quote byte
	continuation := false
	cut := func(closing bool) {
		if !continuation {
			cmds = append(cmds, cur.String())
		}
		cur.Reset()
		continuation = closing
	}
	substitutes := func(i int) bool {
		return src[i] == '`' || (src[i] == '$' && i+1 < len(src) && src[i+1] == '(')
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		case c == '\\':
			// Outside single quotes a backslash makes the next byte literal.
			cur.WriteByte(c)
			if i+1 < len(src) {
				i++
				cur.WriteByte(src[i])
			}
			continue
		case quote == '"':
			if substitutes(i) {
				return nil, false
			}
			if c == '"' {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
			continue
		case c == '>' || c == ';' || c == '&' || substitutes(i):
			return nil, false
		case c == '|' || c == '(' || c == ')':
			cut(c == ')')
			continue
		}
		cur.WriteByte(c)
	}
	if quote != 0 {
		return nil, false
	}
	cut(false)
	return cmds, true
}

// writingSubcommands are the ssql subcommands that write somewhere other
// than stdout, or serve, or run a generated program — never run on a
// keypress.
var writingSubcommands = map[string]bool{"tee": true, "to": true, "serve": true, "generate": true}

// upstreamFile is the source file of src for sampling when the upstream
// is not run: the last argument (never a command word) that exists as a
// regular file. The scan used to include command words, so a file named
// `ssql` in the working directory (a checkout's build) was "the source".
func upstreamFile(src string) string {
	stages := splitTopLevelPipes(src)
	for s := len(stages) - 1; s >= 0; s-- {
		toks := tokenizeStage(strings.TrimSpace(stages[s]))
		for i := len(toks) - 1; i >= 2; i-- {
			if st, err := os.Stat(toks[i]); err == nil && st.Mode().IsRegular() {
				return toks[i]
			}
		}
	}
	return ""
}
