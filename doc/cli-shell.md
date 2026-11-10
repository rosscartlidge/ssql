# The Shell Experience

ssql is built to be typed live at the prompt: one line in `~/.bashrc` turns bash into an ssql-aware editor. The CLI codelab walks through it; this page is the reference.

[Back to Documentation](README.md)

Field and value completion plus a family of single-key actions that work
**on the pipeline you're editing**, all from one line in `~/.bashrc`:

```bash
eval "$(ssql -shell-init)"     # completion + every key binding, in one eval
```

Then, while editing any `ssql … | ssql …` line:

| Key | What it does |
|---|---|
| **Ctrl-O** | Complete a **field name or value** from the upstream pipeline — position-aware: names come from the live schema, values are sampled from the pipeline's own source file (spaced values arrive quoted). Works across process substitution and a `join`'s right-hand side |
| **Alt-h** · vi: **Ctrl-X h** | **Help for the flag/command under the cursor** — and on an expression argument, the full function reference so you're not writing expressions blind |
| **Alt-g** · vi: **Ctrl-X g** | Show the **typed Go** this pipeline compiles to (in a popup under tmux, otherwise your pager) — without running it |
| **Alt-r** · vi: **Ctrl-X r** | **Compile the pipeline as typed Go and run it** — the fast path, one keystroke away. Only the `ssql` stages compile; a producer before them, or a `| less` / `> file` after them, stays where you typed it |
| **Ctrl-T** | **Optimise** the pipeline on the line, in place — push filters into SSH, collapse sort+limit to `top`, prune columns |
| **Ctrl-X Ctrl-E** · vi: **v** (command mode) | **Edit the line in your editor**, the pipeline laid out one stage to a line by `generate pretty`; bash runs it when you save and quit. Other lines open unchanged |
| **Alt-H** · vi: **Ctrl-X H** | List these key bindings |

**In vi mode (`set -o vi`) the Alt keys are not bound**: Alt sends Esc, and
in vi Esc is the key that leaves insert mode, so a binding on Esc h would
steal Esc h (cursor left) whenever you type it quickly. Vi users press
Ctrl-X and then the letter; the Ctrl-X keys work in emacs mode too.

Why key bindings rather than plain Tab-completion? Because a value like a field
name depends on what the *upstream* stages produce, and bash's completion can't
see past the last `|`. The Ctrl-O family reads the **whole line**, so completion
and help are honest about the real pipeline — including fields that only exist
because an earlier `rename`, `group-by`, or `join` created them. Errors (a
pipeline that won't generate or compile) surface in a popup subwindow instead of
silently doing nothing.

Two in-binary references back it up — discoverable from the same prompt:

```bash
ssql functions       # every expression function, by category (also shown by Alt-h on an expr arg)
ssql conventions     # cross-cutting semantics: update SET, _schema headers, SSQL_MODE, …
```

> Popups use `tmux display-popup` inside tmux. Outside tmux a long answer
> opens in `$PAGER` (less: `q` restores the line) and a short one prints
> inline; `SSQL_POPUP=inline|pager` overrides the choice. Bindings are installed in the emacs, vi-insert and
> vi-command keymaps, so they work whichever editing mode you prefer.

## Editing a pipeline in your editor

Bash's own editor key (Ctrl-X Ctrl-E, or `v` in vi command mode) opens the
line in `$VISUAL` and runs the file when you save and quit. With
`-shell-init` (or `eval "$(ssql -edit-keybinding)"` on its own) the editor
opens with the ssql pipeline laid out, one stage to a line within 80
columns:

```bash
ssql from csv emp.csv |
  ssql where -if salary gt 95 |
  ssql update -set-expr band 'salary >= 110 ? "high" : "mid"' |
  ssql to csv
```

Text around the pipeline (`cat x |`, `| less`, `> out.txt`) stays where it
was. A line that is not an ssql pipeline, is half typed, or holds a `$(…)`
opens exactly as bash would open it: laying a pipeline out means asking its
commands to describe themselves, and a command substitution must not run
because a key was pressed. History keeps the pipeline on one line, so Up
and Enter run it again.

### Completion, help and checks inside the editor

`ssql lsp` is a language server: an editor starts it and asks it about the
ssql pipelines in any shell file, the editor key's temp file included. It
offers:

- **completion** of commands, flags, and the fields and values flowing in
  from upstream (Ctrl-O's answers), and of fields and functions inside an
  expression;
- **hover** (`K` in Neovim): the flag's or command's help, as Alt-h shows it;
- **checks** while you type: a stage that would be refused (an unknown flag,
  a missing argument, a field that is not there) is marked, with the
  stage's own message;
- **formatting** (`gq`): `generate pretty`'s layout.

For Neovim 0.11 or later, with its built-in client:

```bash
# codelab: skip — editor configuration (run once by hand)
mkdir -p ~/.config/nvim && ssql lsp -nvim-config >> ~/.config/nvim/init.lua
export VISUAL=nvim
```

The server runs a pipeline only to answer you: an upstream's schema for a
field, a sample for a value, each command on a header alone for a check.
It never runs one holding a `$(…)`, a redirection or a command other than
ssql, and never one that reaches another host (`from ssh`, a catalog, a
URL) while you type: such a stage gets a note saying its fields are not
checked. Relative file names are read from Neovim's working directory,
which bash's editor key sets to the shell's. A shell variable that is not
exported is not visible to the server, so a pipeline using one completes
without fields rather than with wrong ones.

[**Interactive shell walkthrough →**](cli-codelab.md#1-setup)
