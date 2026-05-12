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

- [x] **`ssql from <tab>` should also complete filenames** — fixed in v4.3.7. Returns both subcommand names and file completions when a command has both.

## Embedded Drivers (see autocli-shell-proposal.md)

- [x] **Phase A — engine split** (v4.5.0). `Command.Complete(args, pos) ([]string, error)` and `Command.ExecuteWith(args, base *Context) error` exposed as pure functions. `Context.Stdin/Stdout/Stderr/Ctx` accessors + `Context.State any` pass-through. Zero-value Context still defaults to `os.*` — bash CLI path unchanged.
- [x] **autocli/shell sub-module** (shell/v0.1.1). `chzyer/readline` loop driving an autocli Command tree. Built-in `:exit`/`:help`/`:set` commands. Sub-module so the readline dep stays opt-in.
- [x] **autocli/ssh sub-module** (ssh/v0.1.0). `crypto/ssh` server wrapping `autocli/shell`. ed25519 host-key load-or-generate, OpenSSH `authorized_keys` parsing, refuse-to-start safety, ConnMeta audit hooks, graceful shutdown.
- [ ] **Upstream `chzyer/readline.SetVimMode` race fix** — runtime mode switch races with the library's own input goroutine. autocli/shell defers the switch until next session as a workaround. Send upstream PR (~1-2 hrs investigation + patch + test); fork if rejected.
- [ ] **SSH window-size propagation** — `autocli/ssh` accepts `window-change` SSH requests but doesn't surface them to readline's `SetSize`. Lines wrap at 80 cols regardless of operator's terminal width.
- [ ] **Pipes in autocli/shell** — Position 2 (`io.Pipe()` between stages, opt-in via `Options.EnablePipes`). ~100 LOC. Required so consumers like `ssql serve` can offer pipeline UX at the prompt.
- [ ] **`let $name = pipeline` variables** — REPL-friendly replacement for bash process substitution. Documented as v2-of-shell scope; grammar already reserved.
- [ ] **Position 3 (in-process composition)** — composable handlers returning `iter.Seq[T]` / `Filter[T,U]`. Big lift, perf-motivated; defer until a concrete consumer needs it.
