# TODO — autocli

Issues and feature requests for the autocli library (`github.com/rosscartlidge/autocli`).

## Parser

- [ ] **Silently ignores extra positional args** — when a `String()` flag consumes one value, additional positional args are silently dropped. Should error on unrecognized positionals. Discovered when `ssql from a.csv b.csv` silently ignored `b.csv`. Workaround: make FILE variadic and check count in handler.

## PrefixHandler

- [x] **PrefixHandler not propagated to subcommands** — fixed in v4.3.4. `parseSubcommand` now copies root's prefixHandler to the temp command.
- [x] **Single-arg +flags bypass map path** — fixed in v4.3.5. When `hasPlus` is true, single-arg flags now fall through to the multi-arg map path so PrefixHandler can inject `_negated`.

## Help Generation

- [x] **CLAUSES section missing for non-clause commands** — fixed in v4.3.6. Now always shows CLAUSES section: descriptive text for commands with clauses, "not supported" for others.
- [ ] **`[+|- ...]` in USAGE line for non-clause commands** — slightly misleading for commands that don't support clauses. Low priority since CLAUSES section now explains.

## Completion

- [ ] **`ssql from <tab>` should also complete filenames** — currently only shows subcommands (csv, ssh, parquet, etc.). The bare form accepts filenames directly but tab completion doesn't offer them at this position.
