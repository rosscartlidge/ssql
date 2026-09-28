# The Shell Experience

ssql is built to be typed live at the prompt: one line in `~/.bashrc` turns bash into an ssql-aware editor. The CLI codelab walks through it; this page is the reference.

[Back to Documentation](README.md)

ssql is built to be *typed live at the prompt*. One line in your `~/.bashrc`
turns bash into an ssql-aware editor — field/value completion plus a family of
single-key actions that work **on the pipeline you're editing**:

```bash
eval "$(ssql -shell-init)"     # completion + every key binding, in one eval
```

Then, while editing any `ssql … | ssql …` line:

| Key | What it does |
|---|---|
| **Ctrl-O** | Complete a **field name or value** from the upstream pipeline — position-aware: names come from the live schema, values are sampled from the pipeline's own source file (spaced values arrive quoted). Works across process substitution and a `join`'s right-hand side |
| **Alt-h** | **Help for the flag/command under the cursor** — and on an expression argument, the full function reference so you're not writing expressions blind |
| **Alt-g** | Show the **typed Go** this pipeline compiles to (in a `tmux` popup) — without running it |
| **Alt-r** | **Compile the pipeline as typed Go and run it** — the fast path, one keystroke away. Only the `ssql` stages compile; a producer before them, or a `| less` / `> file` after them, stays where you typed it |
| **Ctrl-T** | **Optimise** the pipeline on the line, in place — push filters into SSH, collapse sort+limit to `top`, prune columns |
| **Alt-H** | List these key bindings |

Why key bindings rather than plain Tab-completion? Because a value like a field
name depends on what the *upstream* stages produce, and bash's completion can't
see past the last `|`. The Ctrl-O family reads the **whole line**, so completion
and help are honest about the real pipeline — including fields that only exist
because an earlier `rename`, `group-by`, or `join` created them. Errors (a
pipeline that won't generate or compile) surface in a popup subwindow instead of
silently doing nothing.

Two in-binary references back it up — discoverable from the same prompt:

```bash
ssql functions       # ~80 expression functions (also shown by Alt-h on an expr arg)
ssql conventions     # cross-cutting semantics: update SET, _schema headers, SSQL_MODE, …
```

> Popups use `tmux display-popup` when you're in tmux; outside tmux a long
> answer opens in `$PAGER` (less: `q` restores the line) and a short one
> prints inline (`SSQL_POPUP=inline|pager` overrides), falling back to inline
> output otherwise. Bindings are installed in the emacs, vi-insert and
> vi-command keymaps, so they work whichever editing mode you prefer.

[**Interactive shell walkthrough →**](cli-codelab.md#1-setup)
