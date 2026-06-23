# Completion System Architecture

ssql uses autocli's completion system to provide context-aware tab completion across pipelines, including remote data sources.

## Core Mechanism: source-file cache (NOT field names)

> **History (changed 2026-06-23, autocli ≥ v4.10.0):** Tab completion used to
> cache field **names** across pipe boundaries via `AUTOCLI_FIELDS` and emit
> them as the wrong-looking completions described below. That cache was a
> snapshot of a source header and went **stale** the moment the pipeline
> renamed fields, aggregated (`group-by`), or joined — so it confidently
> completed the *wrong* names. It was removed. Field **names** across a pipe
> now come from the live schema (`ssql -field-keybinding`, **Ctrl-O**, which
> runs `SSQL_MODE=schema | generate schema`); Tab returns an honest `<FIELD>`
> hint when it can't read the names from a file in the *same* command.

What survives is a **source-file path** cache, used only for **value**
completion (it never went stale the same way — it samples real values from the
source, and degrades to a `<VALUE>` hint when the field isn't in the source):

**Flow (value completion):**
1. `from data.csv<TAB>` → `FileCompleter` emits a `field_cache` directive
   carrying only the source file's absolute **path** (no field names).
2. Bash completion script parses it, sets `AUTOCLI_CACHE_FILE=/abs/data.csv`.
3. `| ssql where -if dept eq <TAB>` → `FieldValueCompleter` samples real `dept`
   values from `AUTOCLI_CACHE_FILE`.

**The directive format:**
```json
{"type":"field_cache","filepath":"/abs/data.csv"}
```

Emitted as the first completion result. The bash completion script (autocli)
strips it from visible completions and exports `AUTOCLI_CACHE_FILE`. (Field
names are deliberately NOT cached — see `FieldCompleter.Complete` in autocli.)

## Completer Types

| Completer | Purpose | Usage |
|-----------|---------|-------|
| `FieldsFromFlag("FILE")` | Complete field names from a data file referenced by another flag | `-if` field arg, `-sum` field arg, etc. |
| `FieldValuesFrom("FILE", "field")` | Complete field values by sampling data from the file | `-if` value arg, `-set` value arg |
| `FileCompleter{Pattern: "*.csv"}` | Complete filenames, auto-emit field_cache for data files | `from csv FILE`, `join FILE` |
| `StaticCompleter{Options: [...]}` | Complete from fixed list | `-if` operator arg (`eq`, `ne`, `gt`, ...) |
| `NoCompleter{Hint: "<text>"}` | Show hint, no completion | Result field names (user-chosen, not derivable) |
| `CompletionFunc(fn)` | Custom completion logic | SSH host/path, catalog values |

**RULE:** Never use `NoCompleter` when field names can be derived from a data file. Use `FieldsFromFlag` or `FieldValuesFrom` instead.

## SSH Completion

`from ssh` uses custom completers for both HOST and PATH:

**HOST completer (`completeSSHHost`):**
- Reads `~/.ssh/config` for `Host` entries (skips wildcards)
- When narrowed to single match, backgrounds `ssh -N -f host` to warm connection multiplexing
- Subsequent PATH completion uses the warm socket (~50ms vs ~2s)

**PATH completer (`completeSSHPath`):**
- SSHs to remote: `/usr/bin/head -1 <path>` (absolute path per security rules)
- Parses CSV header, emits `field_cache` directive
- Uses `-o ConnectTimeout=2 -o BatchMode=yes` for non-interactive, fast-fail behavior
- Falls back gracefully if SSH fails (returns path as-is, no error)

## Catalog Completion

`from catalog` has three custom completers:

**FILE completer (`completeCatalogFile`):**
- Wraps `FileCompleter{Pattern: "*.csv"}` for file completion
- Reads catalog headers and emits `field_cache` with metadata columns
- Range columns collapsed: `date_from`/`date_to` → `date`
- `fields` column excluded (schema hint, not a pruning target)
- `host`, `path`, `format` included (valid pruning targets)

**`-if` field arg:**
- Uses `FieldsFromFlag("FILE")` but falls back to `AUTOCLI_FIELDS` cache
- The cache has the collapsed/filtered columns from `completeCatalogFile`

**`-if` value arg (`completeCatalogFilterValue`):**
- Reads catalog CSV, samples unique values from the selected column
- For range fields (`date`), looks up `date_from` column so users see the value format
- Falls back to scanning `ctx.Args` for catalog file path (positional flags may not be in `GlobalFlags` during completion)

## Debugging Completion

**The `-complete N` flag** runs the completion engine and prints results to stdout. `N` is the 0-based position of the word being completed.

```bash
# What does position 3 complete to?
./ssql -complete 3 from catalog test-data/test-catalog.csv

# Test -if field completion (position 5)
./ssql -complete 5 from catalog test-data/test-catalog.csv -if ""

# Test -if value completion (needs AUTOCLI_FIELDS set, as it would be in a real session)
AUTOCLI_FIELDS="host,path,format,date" ./ssql -complete 7 from catalog test-data/test-catalog.csv -if host eq ""

# Test SSH path completion (will actually SSH to the host)
./ssql -complete 4 from ssh ssql-node1 /data/events/2025-01.csv
```

**Tips:**
- Position counting: `ssql`=0, `from`=1, `catalog`=2, `file.csv`=3, `-if`=4, `host`=5, `eq`=6, `<value>`=7
- Add an empty string `""` as the final arg to simulate pressing TAB with no partial input
- Set `AUTOCLI_CACHE_FILE` manually when testing downstream VALUE completion (normally set by the bash completion script from the `field_cache` directive's `filepath`)
- JSON `field_cache` directives appear as the first line of output — the bash script strips these before showing to the user

## Ctrl-O field completion & process substitution

Field *names* across a pipe come from **Ctrl-O** (`ssql -field-keybinding`), not
Tab (see the cache-removal note up top). The Ctrl-O `bind -x` function reads the
whole line via `READLINE_LINE` and asks the binary which command's schema feeds
the cursor — it does **not** split the line itself. Two protocol flags do the
paren-aware parsing in Go (`cmd/ssql/cursor_context.go`, unit-tested in
`cursor_context_test.go`):

- `ssql -complete-source "<line-up-to-cursor>"` → the shell command whose
  `SSQL_MODE=schema` output should drive completion at the cursor.
- `ssql -cursor-stage "<line-up-to-cursor>"` → the current pipeline stage,
  paren-aware (used by the Alt-h help binding).

Why a Go helper and not bash: the old bindings split on the last `|` with
`${line%|*}` / `${line##*|}`, which is **not** paren-aware — a `|` *inside* a
process substitution `<(ssql … | ssql …)` was mistaken for a top-level pipe and
produced a malformed upstream. `-complete-source` handles, in order:

1. **Cursor inside a `<(…)`** → that procsub's own internal upstream
   (`<(ssql from k.csv | ssql group-by ▮)` completes from `ssql from k.csv`).
2. **A join's right-side field** — the 2nd arg of `-on` (`-on <left> <RIGHT>`)
   or the 1st arg of `-as` (`-as <RIGHT> <new>`) — completes from the join's
   `<(…)>` source, not the upstream pipeline (`join <(ssql from k.csv) -on
   a_kind ▮` completes `kind`/`kind_name` from `k.csv`). Clause separators
   (`+`/`-`) reset the per-clause slot tracking.
3. **Otherwise** → the upstream pipeline feeding the current stage.

Real-pty coverage: `TestFieldProcsubPTY` (Ctrl-O right-field from a procsub,
emacs + vi). Parsing is exhaustively unit-tested in `cursor_context_test.go`.

## Known Limitations

- **Positional flags not in GlobalFlags during completion:** The autocli completion parser doesn't always populate positional flag values into `ctx.GlobalFlags`. Workaround: scan `ctx.Args` directly (e.g. `completeCatalogFilterValue`).
- **Remote (`from ssh`) field names need Ctrl-O:** there is no remote field-name cache (removed in v4.50.0). Field names downstream of an SSH source come from Ctrl-O, which runs the pipeline under `SSQL_MODE=schema` — including the remote read — to get the live schema.
- **Connection warmup requires tab on HOST:** The background SSH connection starts when HOST is tab-completed. If typed manually, no warmup occurs (PATH completion still works, just slower on first use).
- **Right-field completion is join-specific:** `-complete-source` recognizes the right-side slots of `join`'s `-on`/`-as`. Other commands that take a `<(…)>` source would need the same slot rule added.
