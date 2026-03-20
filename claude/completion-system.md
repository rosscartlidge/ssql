# Completion System Architecture

ssql uses autocli's completion system to provide context-aware tab completion across pipelines, including remote data sources.

## Core Mechanism: Field Cache

The field cache bridges completion across pipe boundaries. When a `from` command completes a filename, it extracts field names and passes them to downstream commands via environment variable.

**Flow:**
1. `from data.csv<TAB>` → `FileCompleter` reads CSV header, emits `field_cache` JSON directive
2. Bash completion script parses directive, sets `AUTOCLI_FIELDS=name,age,salary`
3. `| ssql where -if <TAB>` → `FieldCompleter` reads `AUTOCLI_FIELDS`, shows field names

**The directive format:**
```json
{"type":"field_cache","fields":["name","age","salary"]}
```

Emitted as the first completion result. The bash completion script (autocli) strips it from visible completions and exports the env var.

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
- Set `AUTOCLI_FIELDS` manually when testing downstream completion (normally set by bash completion script)
- JSON `field_cache` directives appear as the first line of output — the bash script strips these before showing to the user

## Known Limitations

- **Positional flags not in GlobalFlags during completion:** The autocli completion parser doesn't always populate positional flag values into `ctx.GlobalFlags`. Workaround: scan `ctx.Args` directly.
- **SSH field discovery requires tab on PATH:** The field_cache is only emitted when the PATH completer fires. If the user types the full path without tabbing, downstream commands won't have field names.
- **Connection warmup requires tab on HOST:** The background SSH connection starts when HOST is tab-completed. If typed manually, no warmup occurs (PATH completion still works, just slower on first use).
