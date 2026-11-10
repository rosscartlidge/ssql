# Editing a Pipeline in the Editor: Pretty on the Way In, a Language Server Inside

Reference: DFC167
Created: 2026-11-10
Last modified: 2026-11-10

[Back to Index](./README.md)

## Summary

Bash can hand the command line to an editor: `v` in vi command mode, Ctrl-X Ctrl-E in
emacs mode. The editor gets the line as typed: one long line. Ross (2026-11-10):

> in bash you can go into editor mode where vi is used to edit the line — How cool would it
> be if the line was in pretty format AND (this is hard) it had completion inside vi

There are two parts:

1. **Pretty on the way in.** The editor binding lays the pipeline out with `generate pretty`
   (v4.144.0) before the editor opens: one stage to a line, within 80 columns. Bash runs the
   file when the editor exits, as it does now. This is one more row in the keybinding table.
2. **`ssql lsp`, a language server.** Ross uses Neovim's built-in LSP client, the same one
   that serves gopls and clangd. The server gives:
   - completion: commands, flags, and field names and values from the upstream pipeline;
   - hover: the help Alt-h shows;
   - diagnostics: the error the pipeline's own commands raise;
   - formatting: `generate pretty`, so `gq` re-lays a pipeline after an edit.

   It works in any `.sh` file that holds ssql pipelines, not only the line bash hands over.

Both parts follow DFC115 ("commands are the authority"). The server knows no command
grammar. Every fact comes from a protocol the commands already serve to the bash bindings,
`serve` and the playground: `-complete`, `-help-at`, `-complete-source`, `-value-sample` and
`generate json`. The one new piece of logic is **position mapping**: from a (line,
character) position in multi-line text to an offset in the joined one-line pipeline that
those protocols take.

## 1. What exists

| Piece | Where | Used by |
|---|---|---|
| `generate pretty`, the layout | `commands/generate_pretty.go` | v4.144.0 |
| `-pipeline` / `-script` read pretty text back, continuations included | `preprocessScript`, `continuesLine` (`generate_go.go`) | the round-trip test in `generate_pretty_test.go` |
| the paren- and quote-aware splits: `splitTopLevelPipes`, `SplitPipeline`, `CursorTopLevelStage`, `tokenizeStageOpen` | `commands/cursor_context.go` | Ctrl-O, Alt-h, Alt-g, Alt-r |
| the cursor protocol: `-complete-source`, `-value-sample`, `-cursor-stage`, `-help-at`, `-help-at-cursor`, `-split-pipeline` | `commands/cursor_protocol.go` | the bash bindings, `serve`, the playground |
| autocli completion, `-complete N ARGS…` | autocli | Tab |
| the keybinding table and its bind lines in every keymap | `commands/keybindings.go` (`KeyBindings`, `KeyBindLines`) | every binding |
| the in-place line rewrite | `commands/optimise_keybinding.go` (Ctrl-T) | the model for part 1 |
| pty tests of every binding | `cmd/ssql/*_keybinding_pty_test.go` | the gate for part 1 |

The completion that Ctrl-O does today is assembled in bash (`_ssql_complete_field` in
`field_keybinding.go`). It works in three steps:
1. `-complete-source` names the upstream.
2. The upstream runs under `SSQL_MODE=schema` and its output goes through `generate schema`,
   giving the field names.
3. For values, `-value-sample` gives a sample file, and `-complete` runs again with
   `AUTOCLI_CACHE_FILE` pointing at it.

The server does the same steps in Go (§3.3).

## 2. Part 1: pretty on the way in

### 2.1 The binding

Bash's own `edit-and-execute-command` (vi: `vi-edit-and-execute-command`) does the hard parts
well:
- it adds the line to history;
- it opens `$VISUAL`, else `$EDITOR`, on a temp file named `bash-fc-…`;
- it runs the file when the editor exits.

Vim's and Neovim's filetype rules already make `bash-fc*` a bash buffer, so syntax
highlighting and the server (§3.5) attach with no extra configuration. The binding only
needs to rewrite the line first, so the key becomes a two-step macro:

```bash
bind -m vi-command -x '"\C-x\C-p": _ssql_pretty_line'   # rewrite READLINE_LINE
bind -m vi-command '"\C-x\C-q": vi-edit-and-execute-command'
bind -m vi-command '"v": "\C-x\C-p\C-x\C-q"'
# emacs: Ctrl-X Ctrl-E → the same pair, with edit-and-execute-command
```

`_ssql_pretty_line` works like Ctrl-T's function:
1. `-split-pipeline` separates the ssql run from any shell prefix and suffix
   (`cat x | … | less`).
2. `ssql generate pretty -pipeline` lays out the run.
3. The prefix, the laid-out run and the suffix are put back together.

When the line is not an ssql pipeline, or `pretty` refuses it (a half-typed stage), the line
is left alone. The key then behaves exactly as bash's own.

`v` and Ctrl-X Ctrl-E keep their meaning. The binding changes only what the editor opens
with. An opt-out is the same as for every binding: don't source it.

### 2.2 Settled: what history records

Bash adds the line to history before `fc` opens it, and after this binding that line has
newlines in it. The question was whether history keeps the `\`-newline (safe) or drops
the newline and keeps the backslash (which would join two words).

**Checked in a pty, 2026-11-10, bash 5.2.21:** with `cmdhist` on and `lithist` off,
history stores the pipeline joined onto one line with the backslash-newline gone
(`echo one   two |   tr a-z A-Z`), and Up then Enter runs it to the same output. The macro
is the binding; the fallback (running the editor from the `bind -x` function) was not
needed. `TestEditKeybindingPTY` keeps the answer.

### 2.3 Tests

- **A pty test**, in emacs and vi mode, following `field_keybinding_pty_test.go`.
  - `VISUAL` is a script that copies its file out. The test asserts the file is the
    pretty text, and that bash's output from running it equals the one-line pipeline's.
  - Then the test presses Up, Enter, and checks the same output again (§2.2).
- **The fall-backs:** a non-ssql line (`ls -l`) and a half-typed stage reach the editor
  unchanged.
- **Watched to fail:** with the rewrite step removed from the macro, the copied file is the
  one-liner.

## 3. Part 2: `ssql lsp`

### 3.1 Shape

`ssql lsp` speaks LSP over stdio in Go. It serves only what is needed:
- `initialize`, `shutdown` and `exit`;
- `textDocument/didOpen` and `didChange` (full-text sync; pipelines are short);
- `completion`, `hover`, `formatting` and `rangeFormatting`;
- `publishDiagnostics`.

It is hand-rolled JSON-RPC, a few hundred lines with no new module dependency, like `serve`'s
HTTP protocol.

`ssql lsp` is a top-level command, so it is registered in both `main.go` and the playground's
`main.go` (the drift test requires it). In a slim build it refuses loudly ("no language
server in the browser build"), as `-wasm` does.

Neovim (0.11+) configuration:

```lua
vim.lsp.config('ssql', {
  cmd = { 'ssql', 'lsp' },
  filetypes = { 'sh', 'bash' },
  root_dir = function(_, on_dir) on_dir(vim.fn.getcwd()) end,
})
vim.lsp.enable('ssql')
```

`ssql lsp -nvim-config` prints this, as `-field-keybinding` prints its script. The server
answers nothing outside an ssql pipeline, so it can sit alongside bash-language-server on
the same buffer.

### 3.2 Position mapping: the one new piece

A document is shell text. The server's unit is the **logical command** around the cursor. It
starts at a line that does not continue the previous one and runs on while a line:
- ends in an unquoted `\` (the rule `continuesLine` already implements for `-script`);
- ends in a `|`;
- leaves a `<(…)` or a quote open.

The server builds the joined line with an offset map (`lspJoin(text) (joined string,
toDoc, toJoined []int)`):
- a `\`-newline is dropped;
- a newline after `|` becomes a space;
- a newline inside quotes stays, because it is part of the argument;
- the indentation `pretty` adds becomes the single space that already separates the words.

`before = joined[:toJoined[cursor]]` is then exactly the string the bash bindings pass to the
cursor protocol, and everything after this step is existing code.

This is a mapping of shell **layout**, not of command grammar. It knows `\`, `|`, `(`, `)` and
quotes, which are also what `splitTopLevelPipes` and `continuesLine` know. It lives next to
them in `cursor_context.go` and shares their quote rules, so there is not a third copy.

**The oracle for it is strong and cheap.** At every word boundary of the pretty layout, the
completion and hover answers must equal the one-line pipeline's at the corresponding
offset. The pipelines are `TestGeneratePretty`'s five, at widths 40, 60 and 80. The
breaks, the procsub alignment and the trailing `|` are exactly where a mapping goes wrong.

### 3.3 Completion

The server receives `before` and does what the binding does:
- **Command and flag names, and enumerated values:** autocli's `-complete`, in process, with
  the stage's words from `tokenizeStageOpen`.
- **Field names:** when the slot takes fields (autocli answers with the `Use-Ctrl-O`
  directive):
  1. `CompleteSource(before)` names the upstream;
  2. the server runs it under `SSQL_MODE=schema` (`bash -c`, in the server's working
     directory, 2 s timeout);
  3. `generate schema` turns the output into names.

  Ctrl-O's rule, "commands that write nothing only", holds, because `CompleteSource`
  already enforces it.
- **Values:** when autocli answers `Values-Use-Ctrl-O`, `ValueSample(before)` gives the
  sample file, and the completion runs again with `AUTOCLI_CACHE_FILE` set (10 k records,
  2 s, else the source file).
- **Expression functions** inside `-if-expr` and `-set-expr` come from
  `exprFunctionCandidates`, which Alt-h already uses.

**What it cannot see:**
- **Shell variables.** The upstream runs in a fresh `bash -c` with the editor's environment,
  so a variable not exported in the shell (`f=data.csv; ssql from csv $f`) is empty there.
  The completion then comes back empty. It never comes back wrong: a missing source is an
  error, and the server reports it in `completion`'s message rather than a guess.
- **The working directory.** It is Neovim's `getcwd()`. Bash's `v` starts the editor in the
  shell's directory, so relative file names resolve as they would on the command line.

Completion items carry the help line autocli gives each flag, shown as `detail`.

### 3.4 Hover, diagnostics and formatting

- **Hover:** `-help-at-cursor before`, the Alt-h text.
- **Diagnostics:** on open, and on change after 500 ms idle, each logical command whose
  `-split-pipeline` finds ssql stages is checked with `generate json -pipeline`. This runs
  each stage in fragment mode and reads only headers, as Ctrl-T does.
  - A refusal is published as an error over the range of the stage that raised it (the
    `ssql <command>` prefix in its message names it).
  - Word-level ranges need the commands to say which argument failed. That is a new protocol
    surface, if wanted: a structured error from the autocli parse. It is not in the first
    cut.
- **Formatting:**
  - `formatting` lays out every logical ssql command in the document;
  - `rangeFormatting` lays out the commands that intersect the range;
  - both use `generate pretty`.

  The width is the LSP client's `textwidth` when it sends one in `initializationOptions`,
  else 80. Text that is not ssql is never touched. Neovim's `gq` uses `rangeFormatting`
  through `formatexpr`, which the built-in client sets.

### 3.5 What this gives Ross's workflow

1. Type a long pipeline at the prompt.
2. Press Esc, then `v`. Neovim opens it laid out, one stage to a line.
3. Edit a stage, with Tab or Ctrl-X Ctrl-O completing the flags and the fields flowing in
   from upstream. `K` shows the flag's help.
4. A bad field name is underlined before you leave the editor.
5. `gq` re-lays the stage that grew.
6. `:wq` runs it.

The same server works on a saved pipeline script.

## 4. Tests

| What | How | Watched to fail by |
|---|---|---|
| the position map | unit table: continuation, trailing pipe, procsub over lines, a quote holding a newline, CRLF | an off-by-one in `toJoined` |
| map against the protocol | at every word boundary of the pretty layout, completion and hover equal the one-line answer at the mapped offset (`TestGeneratePretty`'s pipelines × widths) | dropping the newline-after-`|` rule |
| JSON-RPC | a Go test drives `ssql lsp` over pipes: initialize, didOpen, completion of a flag, a field name after a `join`, a value, hover, a diagnostic, formatting, shutdown | a field completed from the wrong stage's upstream |
| Neovim end to end | `nvim --headless` with a minimal `init.lua` using `vim.lsp.enable`, `vim.lsp.buf_request_sync` for completion, and `vim.lsp.buf.format`; skipped when `nvim` is absent | the server attached to the wrong filetype |
| the binding | the pty test in §2.3 | §2.3 |

**Neovim:** the development machine has 0.12.6 from the release tarball, in
`~/.local/nvim-linux-x86_64` (Ubuntu 24.04's apt has 0.9.5, older than the 0.11 the
configuration needs). The end-to-end test skips without `nvim`.

## 5. Order of work

1. **Part 1, the binding.** Settle §2.2 in a pty first, then build it. It is useful on its
   own.
2. **The position map** and its oracle against the protocol, before any LSP code.
3. **`ssql lsp`:** completion and hover first, then formatting, then diagnostics, each with
   its JSON-RPC case.
4. **The Neovim end-to-end test and `-nvim-config`.**
5. **Docs:**
   - `doc/cli-shell.md`: a section on the editor;
   - the codelab: a short step;
   - `KeyBindingsHelp`: the new row;
   - CHANGELOG.

## 6. Decided and open

- **Decided (Ross, 2026-11-10): Neovim's built-in LSP client** is the target. No vim-only
  omnifunc: LSP reaches Neovim, VS Code, helix and emacs with the same server.
- **Decided (Ross, 2026-11-10, in plan mode, each as recommended):**
  - **The editor key** takes over `v` and Ctrl-X Ctrl-E; a line that is not an ssql
    pipeline opens unchanged.
  - **The width** is a fixed 80; Neovim's `textwidth`, sent as `initializationOptions`,
    overrides it for formatting.
  - **Remote sources:** nothing that reaches another host runs while you type; the stage
    gets a hint instead.
  - **The order:** the binding, then the position map and the server.
- **Later:**
  - word-level diagnostic ranges (a structured parse error from autocli);
  - `textDocument/signatureHelp` for a flag's arguments;
  - `codeAction` for "optimise this pipeline" (Ctrl-T) and "show the Go" (Alt-g).

## 6a. Built (2026-11-10)

Both parts, with these departures from §2–§3:

- **The rewrite is a protocol flag, `-pretty-line LINE`** (`PrettyLine`,
  `generate_pretty.go`), not shell in the binding. Laying a pipeline out runs it in fragment
  mode under bash, so a `$(…)` on the line would run when `v` is pressed, which bash's own key
  never does. `PrettyLine` lays out only what `pipelineSafeToDescribe` (`value_source.go`)
  accepts: ssql commands alone, no substitution, redirection or second command. A sink
  naming a file is fine, since fragment mode writes nothing (`to csv`, `tee`, `to parquet`
  and `to xlsx` each checked to leave no file). The language server's formatting calls the
  same function.
- **That guard found a hole in Ctrl-O's.** `upstreamSafeToRun` skipped everything inside
  quotes, but bash runs `$(…)` and backticks inside double quotes, and an unquoted `\'`
  opened a "quote" bash never sees. Both are fixed in the shared `shellCommandTexts`, with
  test rows watched to fail.
- **Diagnostics run no shell.** §3.4 proposed `generate json -pipeline`, which runs the text
  under bash and reports the first error without saying which stage raised it. Instead
  `docStagesFromText` (`lsp_text.go`) turns the text into a pipeline document: stages from
  `splitTopLevelPipes`, words from `tokenizeStage`, values from `shellWordValue`, whose
  oracle is bash's own `printf`. A word whose value depends on the shell (`$x`, a glob, a
  backtick) leaves the pipeline unchecked. The document is then checked the way
  `ssql run -check` checks one:
  - autocli's `Check` on each stage's argv (`checkPipelineDoc`), which runs nothing;
  - `checkPipelineFields`, which runs the source under `SSQL_MODE=schema` and each later
    command on that header alone, with no rows.

  Every error names its stage, so the mark lands on the stage that refuses. A pipeline fed
  from stdin (a `cat x |` before it, or `-`) gets the syntax check only, since there is no
  schema to check its fields against.
- **What reaches another host is a table the commands package owns** (`remoteReach`,
  `remote_reach.go`): `from ssh`, `from catalog`, `merge -catalog`, and any argument that
  is a URL. `TestRemoteReachCoversSSHCallers` fails on a file that builds a remote command or
  runs ssh without a row; it found `from.go`'s local pushdown on its first run, which is
  listed with its reason.
- **What fragment mode reaches, traced:** `from ssh` in fragment mode runs no ssh and opens no
  connection; a URL source fetches its header. Formatting and the editor key, which the
  user asks for, describe both; diagnostics and completion, which happen while typing,
  describe neither.
- **The oracle for the map** is the server itself: `TestLSPMatchesOneLine` asks for
  completion at the end and hover at the start of every word of `TestGeneratePretty`'s
  pipelines, laid out at widths 40 and 80, and requires the same answers as at the same
  word of the one-liner. With the newline-after-`|` rule removed it failed 36 times.
- **`ssql` where a stage starts.** Ross pressed Ctrl-X Ctrl-O at the end of a line ending in
  ` |` and got "Pattern not found", which was accurate, since the next stage had not begun.
  Completion now offers `ssql` there (with the blank it needs), after a `<(`, and on a word
  `ssql` begins with.
- **Tests:**
  - `TestEditKeybindingPTY`, in emacs and vi;
  - `TestLogicalCommands` and `TestShellWordValue`, with bash as the oracle;
  - `TestLSP`, over pipes;
  - `TestLSPInNeovim`, with Neovim 0.12.6 headless, on a `bash-fc-…` file.

  Each was watched to fail:
  - the rewrite dropped from the macro;
  - the safety check removed, after which the `$(…)` ran and the editor got `name eq Ann`;
  - the map broken;
  - the upstream taken one stage early;
  - the configuration's filetype changed.

## 7. References

- [DFC115](./dfc115_commands_are_the_authority.md): why the server asks the commands.
- `doc/research/bash-pipeline-completion-options.md`: the bash completion design, and why the
  pty is the only honest test of a binding.
- [DFC095](./autocli-shell-proposal.md): autocli's own shell, emacs-only since shell v0.2.0;
  this is the other route to vi.
