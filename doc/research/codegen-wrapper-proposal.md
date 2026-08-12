# Codegen Wrapper Proposal

Reference: DFC092
Created: 2026-05-03
Last modified: 2026-05-03

[Back to Index](./README.md)

**Status:** Proposal
**Date:** 2026-05-03
**ssql version target:** v4.41+
**Related:** Phase A (v4.39), Phase B (v4.40), `ai-cli-generation.md`

## TL;DR

The current way to invoke code generation is:

```bash
(export SSQLGO=record; ssql from data.csv \
  | ssql where -if age gt 25 \
  | ssql to csv) | ssql generate go
```

This is powerful — it composes naturally with the rest of the
shell — but the `(export VAR=val; ...) | ...` shape is unfamiliar
to many shell users. Two thin wrappers, ~half-a-day of work
together, would lower the bar significantly without changing the
underlying mechanism:

1. **`ssql -shell-helpers`** — emits a `ssqlgen` bash function that
   takes the pipeline as a single quoted string. Same install
   pattern as `ssql -completion-script`.
2. **`ssql generate go -script PATH`** — reads the pipeline from a
   file (or `<(heredoc)` via process substitution). Multi-line
   readable; comments allowed; works in CI.

Both build on the existing `SSQLGO` + fragment-pipe mechanism;
neither replaces it. Power users keep the explicit form; new
users get the simpler wrappers.

## The pain points (in order)

1. **`(export VAR=val; cmd)` syntax is unusual.** Most shell users
   know `VAR=val cmd` (which only sets the var for the first
   command, broken across pipes) but not `(export VAR=val; cmd)`.
2. **Forgetting `export` is a silent failure mode.** Without the
   `export`, only the first command sees `SSQLGO`; downstream
   stages then complain about JSONL parse errors. Easy to
   misdiagnose.
3. **The trailing `| ssql generate go` is fiddly to place.** Has to
   be outside the subshell, after the `)`. Easy to misplace
   inside.
4. **Picking the right mode value.** Users have to know that
   `record` / `typed` / `parallel` exist and what each does. The
   wrappers can default sensibly (typed) and accept a flag for
   the rare cases.

## Solution 1 — Shell helper function

`ssql -shell-helpers` prints a small set of bash functions to
stdout. Users source it once via `eval`:

```bash
# add to ~/.bashrc:
eval "$(ssql -shell-helpers)"
```

The emitted function:

```bash
ssqlgen() {
    local mode=typed
    case "$1" in
        -record|-typed|-parallel) mode="${1#-}"; shift ;;
    esac
    local script="$1"; shift
    (export SSQLGO="$mode"; eval "$script") | command ssql generate go "$@"
}
```

### Usage examples

```bash
# Default (typed mode):
ssqlgen 'ssql from data.csv | ssql where -if age gt 25 | ssql to csv'

# Record mode:
ssqlgen -record 'ssql from x.csv | ssql to csv' -run

# Pass through generate-go flags:
ssqlgen 'ssql from x.csv | ssql group-by dept -count n | ssql to table' \
    -optimise -build /tmp/myprog
```

### Why a function (not a wrapper script)

A bash function preserves stdin/stdout/stderr and signal
handling exactly as the user expects. A wrapper script (e.g.
`/usr/local/bin/ssqlgen`) would need extra plumbing for tty
behaviour. Functions are also faster — no extra process, no
shebang dispatch.

### Future helpers in the same eval

Once `-shell-helpers` exists, it's a natural place for other
small wrappers users might want:

- `ssqlrun` — same as `ssqlgen -typed ... -run` (build, exec, exit).
- `ssqlsql` — same shape but pipes through `generate sql` instead.
- `ssqloptimise` — pipes through `generate ssql` (the rewrite
  optimiser).

Tier these as we see what people use.

## Solution 2 — `-script PATH` flag on `ssql generate go`

The wrapper's quoting cost is fine for a single-line pipeline
but tiresome for a 5-stage analytics query. `-script PATH` reads
the pipeline from a file:

```bash
ssql generate go -script analytics.ssql -optimise -run
```

Where `analytics.ssql` is:

```
# Daily error rate by service
ssql from logs.csv
| ssql where -if status ge 500
| ssql group-by service -count errors
| ssql sort -desc errors
| ssql limit 10
| ssql to table
```

### The cute heredoc form

Process substitution makes `-script` work for transient inline
scripts too:

```bash
ssql generate go -script <(cat <<'!'
  ssql from data.csv
  | ssql where -if age gt 25
  | ssql group-by dept -count n
  | ssql to csv
!
) -mode record -run
```

`<(cat <<'!')` opens a `/dev/fd/N` pipe; `-script` opens it as a
regular file. One code path handles both saved scripts and
inline heredocs.

### What the script syntax allows

Beyond plain shell pipelines, the preprocessor recognises a few
niceties:

1. **Leading-pipe continuation** (your preferred style):
   ```
   ssql from data.csv
   | ssql where -if age gt 25
   | ssql to csv
   ```
   Bash natively supports trailing-pipe continuation but not
   leading; the preprocessor joins lines so both work.

2. **Trailing-pipe continuation** (bash native, also fine):
   ```
   ssql from data.csv |
   ssql where -if age gt 25 |
   ssql to csv
   ```

3. **`#` comments** — start-of-line and end-of-line. Stripped
   before exec.

4. **Blank lines** — ignored.

The output of the preprocessor is one bash pipeline string,
passed to `bash -c "export SSQLGO=$mode; $pipeline" | ssql generate go ...`.

### Mode flag

`ssql generate go -script PATH` adds a `-mode` flag (or piggy-
backs on existing flags):

```bash
ssql generate go -script ... -mode typed     # default
ssql generate go -script ... -mode record
ssql generate go -script ... -mode parallel  # alias of typed
```

If the user prefers, environment still wins: `SSQLGO=record ssql
generate go -script ...` works because we propagate the inherited
env to the subshell.

## How they compose

The four shapes coexist:

| Use case | Form |
|---|---|
| One-line interactive | `ssqlgen 'ssql from x.csv \| ssql to csv'` |
| Multi-line interactive | `ssql generate go -script <(cat <<! ... !)` |
| Saved pipeline | `ssql generate go -script my-pipeline.ssql` |
| Power-user / scripts | `(export SSQLGO=typed; ...) \| ssql generate go` |

All four use exactly the same underlying machinery:
fragment-emission via `SSQLGO`, then `generate go` for assembly.
The wrappers are pure plumbing.

## Implementation sketch

### `-shell-helpers` (~50 lines)

New `cmd/ssql/commands/shell_helpers.go`:

```go
func registerShellHelpers(cmd *cf.CommandBuilder) {
    cmd.Flag("-shell-helpers").Bool().Global().
        Help("Print bash helper functions; eval in ~/.bashrc").Done()
    // Existing bootstrap-flag handler in main.go: if flag set, print
    // the embedded helper script and exit 0.
}
```

The helper text is a `const` string in the file; no dynamic
parts. ~30 lines of bash + a couple of Go lines to print it.

Tests: a unit test that runs `bash -c "$(ssql -shell-helpers); ssqlgen 'ssql from x.csv | ssql to csv'"` against a tiny CSV
and asserts the generated Go runs.

### `-script PATH` (~80 lines)

In `cmd/ssql/commands/generate_go.go`:

1. Add `-script` and `-mode` flags.
2. If `-script` is set, read the file, preprocess, and exec bash:
   ```go
   pipeline := preprocessScript(string(src))
   bin, _ := os.Executable()
   shellCmd := fmt.Sprintf(`export SSQLGO=%s; %s | %s generate go %s`,
       mode, pipeline, bin, joinFlags(passThrough))
   cmd := exec.Command("bash", "-c", shellCmd)
   cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
   return cmd.Run()
   ```

3. `preprocessScript`: strip comments, strip leading whitespace,
   join leading-`|` lines with the previous line.

Tests: a corpus of small `.ssql` script files (saved + heredoc
form, both pipe styles) that should each produce identical
generated Go to the equivalent explicit `(export ...; ...) |
generate go` form.

### Combined effort

About half a day for both, tests included. Both extensible —
once `-script` exists, future enhancements (multi-pipeline files,
imports, parameter substitution) live there without touching the
existing assembly code.

## Failure modes

### Quoting in `ssqlgen 'pipeline-with-quotes'`

If the pipeline contains single quotes (e.g. expression-language
predicates: `-if-expr 'name == "Alice"'`), the user has to use
double quotes outside or escape inside. Standard shell quoting
caveat. Document it; it's the same in any "command-as-string"
pattern.

### `bash` not on the remote/CI host

If bash is missing, `-script` falls back to printing a helpful
error: "ssql generate go -script needs bash; pipeline must be
inlined as `(export SSQLGO=...; ...) | ssql generate go`."
Tests need to be skipped on bash-less platforms (Windows
probably; CI Linux always has it).

### Preprocessor surprises

`#` inside a string (e.g. `where -if-expr 'tag == "#urgent"'`)
shouldn't be treated as a comment. Standard fix: only strip `#`
when not inside a quoted region. The preprocessor is small but
non-trivial in this respect.

### The `-script` flag is wired up but the file path is invalid

Clear error: "ssql generate go -script: cannot read FILE: NO SUCH FILE OR DIRECTORY".
Distinguish "no such file" from "unreadable" so users debug
faster.

## Open questions

1. **Should `ssqlgen` print the equivalent explicit form on `-x`?**
   Like `ssqlgen -x '...'` shows the `(export SSQLGO=...; ...) | ...`
   incantation it's wrapping. Useful for "I want to learn what
   this is doing" or "I want to copy-paste into a CI script".
2. **Should `-script` ever load > 1 pipeline per file?** E.g. a
   file with multiple named pipelines, callable via
   `-script file.ssql -name daily-errors`. Probably no for v1
   (YAGNI); revisit if user demand appears.
3. **Should we add `ssqlrun` / `ssqlsql` / `ssqloptimise` in the
   first ship, or just `ssqlgen`?** Lean: just `ssqlgen` for v1.
   The others trivially compose: `ssqlgen ... -run`, `... | ssql
   generate sql` (no wrapper needed for sql since the user typed
   the pipeline once already and just pipes through a different
   final command).

## See also

- `ai-cli-generation.md` — current "remember to export" guidance
- `cli-codelab.md` — Code Generation section will need a new
  subsection showing `ssqlgen` + `-script` once shipped
- `remote-go-execution-proposal.md` — `-script` composes nicely
  with remote-Go execution (a `.ssql` script becomes the
  natural unit of remote work)
