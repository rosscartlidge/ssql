# A Terminal UI for ssql? (Bubble Tea)

Reference: DFC127
Created: 2026-09-12
Last modified: 2026-09-12

[Back to Index](./README.md)

Status: **discussion — nothing built, nothing scheduled.** Ross asked
(2026-09-12, mid codelab review) "what do you think of creating a
bubbletea based TUI?" — "just wanted to see whether it makes any
sense". This records the answer so the question is not re-derived.

## 1. What ssql's interactive surfaces are today

ssql has two interactive front ends, and both are *views of pipeline
text* (DFC115: the pipeline is the state; a UI edits it, never owns it):

1. **The shell.** `eval "$(ssql -shell-init)"` decorates the bash
   line you are already typing: Tab completes commands, flags and
   files; Ctrl-O completes field names and values across the whole
   pipeline; Alt-h explains the flag or function under the cursor;
   Alt-g shows the typed Go; Alt-r compiles the ssql stages and runs
   them *inside the rest of your shell line* (a `| less` or `> file`
   after them stays yours — DFC-less, W37). Answers appear as tmux
   popups, or inline when not in tmux. Protocol calls behind it:
   `-complete`, `-complete-source`, `-cursor-stage`, `-help-at`,
   `-split-pipeline`, `SSQL_MODE=schema`.
2. **The browser workspace** (`ssql serve -listen-http`, and `to
   explore`): a grid, a pipeline bar with the same completion, charts,
   the engine in the page (DFC108/117/118/119). Same protocol calls,
   over HTTP.

There is also a third, line-oriented one: the **SSH operator console**
(`ssql serve` over SSH, `autocli/shell`, DFC-less proposal
`autocli-shell-proposal.md`), which deliberately kept "colour / TUI
panels" out of scope for v1.

## 2. The case against a TUI for the pipeline

The bet of surface 1 is that an ssql pipeline **is** a bash pipeline.
Everything the shell already gives is available for free and stays
composable: history, aliases, `| less`, `| head`, `> file`, `tee`,
process substitution, job control, ssh. The Alt-r fix (compile only the
ssql stages, leave `| less` alone) is that bet made explicit.

A Bubble Tea application takes over the terminal. The moment the user
enters it they have *left* the shell, and the TUI has to re-provide the
shell's parts one at a time: a line editor with history, a pager,
redirection, a way to run the pipeline "for real" afterwards. Each of
those already exists, better, outside. It would also be a **third**
front end to keep in step with the bash bindings and the browser
workspace — and the workspace has already taught us what UI state that
is not pipeline text costs (DFC115 §matured: widget steps, grid ops and
chart axis picks each drifted until they became pipeline text).

So: **no** to a TUI as a way to build pipelines. The shell is the
pipeline builder, and a TUI would be a worse shell.

## 3. Where reviewers actually hurt — and the cheap fix

The friction in review was **tmux**, not the shell: installing it, the
mouse, reattaching (now `doc/tmux-for-ssql.md`). tmux is only needed for
the *popups*. When not in tmux, Alt-h/Alt-g print inline below the
prompt, which is noisier but works.

There is a fix inside the current model that needs no framework: when
`$TMUX` is unset and stdout is a terminal, send long help/code to
`${PAGER:-less}`. `less` uses the alternate screen, so the help appears
full-screen, `q` restores the line exactly as it was — it *looks* like
a popup and needs nothing installed. `interactive-help-at-cursor.md`
§5d sketched a heavier "in-shell overlay"; the pager is the light
version. This is an hour's work in `ssqlPopupFunc` and would remove
tmux from the codelab's critical path (tmux would remain the nicer
experience, not the required one). **This is the thing to do first if
the tmux friction persists.**

## 4. The case *for* a TUI: the console over SSH

Where a Bubble Tea program would earn its place is as the **terminal
twin of the browser workspace**, for people who have a terminal and SSH
to the box but no browser to it — the operator-console audience of
`ssql serve`:

- a grid of the loaded dataset (paged, sortable by clicking a header —
  which is just `sort` on the pipeline);
- the pipeline bar, with the same Tab/Ctrl-O/Alt-h behaviour driven by
  the same protocol calls the browser uses (`-complete`, `-help-at`,
  schema mode) — no second completion implementation;
- a help pane, the status line (rows, wall time, engine: `ssql_gpu`
  or not);
- charts as sixel/kitty graphics where the terminal supports them,
  else a "written to FILE" line.

Every piece of state is the pipeline text in the bar, so share links,
`generate go`, and the browser workspace all see the same thing. The
backend is the existing `serve` HTTP/console protocol; the TUI is one
more client. Bubble Tea is a good fit for that: mature, mouse support,
alternate screen, and Charm has spent years on exactly this shape of
program (Bubbles for the text input and table, Lip Gloss for layout).

What it is **not**: a replacement for the shell integration. Users on
their own machine with their own data keep the shell; the TUI is for
the box you SSH into.

## 5. Costs and timing

- The console TUI is a GopherCon-scale unit (weeks, not days): a new
  binary or subcommand (`ssql tui HOST`? or served over the SSH console
  itself), the grid/table widget, the bar wired to the protocol, the
  help pane, tests through a pty (the rule for every key we bind).
- It adds a dependency tree (bubbletea, bubbles, lipgloss) to a project
  that has kept its CLI dependencies small; `slim` builds would need to
  exclude it as they exclude arrow/parquet.
- The demo story for GopherCon 2027 is currently shell + Alt keys +
  browser workspace. A TUI is a third screen in a talk that already
  has two; it would need to earn a slot by being the SSH-only story.

## 6. Position

1. **Not now.** Nothing here is blocking the codelab review or the
   next releases.
2. **Do the `less` fallback** (§3) if tmux keeps tripping readers — it
   is the cheap, framework-free answer to the actual complaint.
3. **Revisit the console TUI** (§4) at GopherCon planning, as the
   "workspace over SSH without a browser" story, built as a client of
   the existing serve protocol and nothing else. If built, it is a
   view of pipeline text like the other two, or it is not built.

## 7. References

- `dfc115_commands_are_the_authority.md` — UIs are views/editors of
  the pipeline, never owners of state.
- `dfc108_split_pipelines_server_browser.md`, `dfc117`–`dfc119` — the
  browser workspace and the protocol it drives.
- `autocli-shell-proposal.md` — the SSH operator console; TUI panels
  out of scope for v1.
- `interactive-help-at-cursor.md` §5 — the popup/inline/overlay
  options for Alt-h.
- `doc/tmux-for-ssql.md` — what the review friction actually was.
