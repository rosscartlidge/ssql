# Interactive Help at the Cursor — Design Exploration

**Status:** Exploration / proposal. Written 2026-06-20. Idea: show help for
the **command / flag / argument under the cursor** while you're editing the
line — not just completion candidates. "What does `-sum` take here?" answered
in place, optionally in a **tmux popup**. Proposed to live in **autocli** (general),
with ssql as the first consumer.

Related: [bash-pipeline-completion-options.md](bash-pipeline-completion-options.md)
(the `bind -x`/`READLINE_LINE` action-binding pattern this builds on), TODO #97
(multi-key bindings — `Shift-Tab` for help was sketched there).

## 1. The idea

While typing
```
ssql group-by dept -sum salary ▮
```
you'd like to see, without leaving the line:
- what `group-by` does,
- that `-sum` takes `<field> <result-name>` and means "sum field values",
- maybe an example.

Tab gives you *candidates*; this gives you *explanation*. Completion answers
"what can go here"; help answers "what is this and what does it expect".

## 2. Why this belongs in autocli, not ssql

autocli already owns everything needed:

- **The content.** The command tree carries `Description` (commands), `Help`
  (flags), `ArgNames`/`ArgTypes` (per flag), and `Example`s — and ssql's house
  rule already requires 2–3 examples per command. `GenerateHelp()` /
  `GenerateHelpEmbedded()` / `Subcommand.GenerateHelp(parentName)` render whole-
  command help today.
- **The cursor analysis.** `(*Command).analyzeCompletionContext(args, pos)`
  *already* locates the cursor: it resolves the subcommand path, the current
  flag (`FlagName`), which argument of it (`ArgIndex`), the current clause, and
  whether you're on a positional. Completion uses this to pick a completer.

So **help-at-point is the completion context rendered as help instead of
candidates.** That's the key insight: it's not a new analysis, it's a second
*renderer* on an analysis autocli already does. Any autocli-built CLI gets it
for free; ssql is just the first consumer (and the keybinding emitter).

## 3. The core primitive: `Command.HelpAt(args, pos)`

Mirror `Complete(args, pos)`:

```go
// HelpResult is structured so callers can render it inline, in a popup,
// or in a pane — and so it degrades (Flag may be nil → command-level help).
type HelpResult struct {
    CommandPath []string  // ["ssql", "group-by"] — resolved subcommand path
    CommandDesc string    // group-by's one-line Description
    Flag        *FlagHelp // the flag under the cursor, if any
    Positional  *ArgHelp  // the positional under the cursor, if any
    Examples    []Example // the command's examples (for the popup/pane)
}
type FlagHelp struct {
    Names    []string // ["-sum"]
    Help     string   // "Sum field values (field name, result name)"
    Args     []ArgHelp
    ArgIndex int      // which arg the cursor is on (-1 = the flag name itself)
}
type ArgHelp struct{ Name, Type, Help string }

func (cmd *Command) HelpAt(args []string, pos int) (*HelpResult, error)
```

Implementation is small: run `analyzeCompletionContext(args, pos)` (already
exists), then read the resolved `FlagName`/`ArgIndex`/clause out of the context
and assemble the relevant slice of the tree. Pure and side-effect-free, exactly
like `Complete`. Falls back to command-level help when the cursor isn't on a
specific flag/arg.

A bash-facing protocol flag parallels `-complete N`:
```
ssql -help-at "<line>" <point>     # prints rendered help for the cursor
```
(emits text; the keybinding/popup consumes it.)

## 4. Triggering it

### 4a. bash — a `bind -x` key (reads `READLINE_LINE`)

We established that a bash `complete -F` function can't see the whole line
(`COMP_LINE` is scoped to the current command), but a `bind -x` binding reads
`READLINE_LINE` (the entire line) — the same proven pattern as `Ctrl-O`
(field completion) and `Ctrl-T` (optimise). So:

```bash
_ssql_help_at() {
    local line="$READLINE_LINE" point="$READLINE_POINT"
    # slice to the current pipeline stage, then ask the binary
    local help; help=$(command ssql -help-at "$line" "$point" 2>/dev/null)
    [[ -n "$help" ]] && _ssql_show_help "$help"   # display adapter — §5
}
bind -m emacs     -x '"\eh": _ssql_help_at'   # Alt-h, say
bind -m vi-insert -x '"\eh": _ssql_help_at'
```

Single key (no chord → no keyseq-timeout fragility, per the Ctrl-O/Ctrl-T
lesson). `Shift-Tab` (`"\e[Z"`) is the intuitive "opposite of Tab," but its
escape sequence varies across terminals — worth offering as an alternative,
default to something robust.

### 4b. autocli-shell — `AutoCompleteCallback(line, pos, key rune)`

The in-process shell is the *richest* target: `AutoCompleteCallback` already
receives the pressed key, so we just branch on a help key, call `HelpAt`, and
render — full terminal control, no subprocess, and (crucially) it sees **every
keystroke**, which enables a *live* help pane (§5c) that updates as you type.

### 4c. "Multiple completion types" (readline)

readline exposes several completion actions you can bind to different keys —
`complete` (Tab), `menu-complete` (cycle), `possible-completions` (`M-?`, list),
`insert-completions`, `glob-list-expansions`. Help is best as its **own** action
on its own key rather than overloading completion, so this design treats it as a
sibling keybinding, not a completion variant. (Native *descriptions inside*
completion — what zsh/fish do — bash doesn't support; that's exactly why a
dedicated help key/pane is the right move for bash.)

## 5. Displaying it — the design space (graceful degradation)

The interesting problem is showing help **without clobbering the line you're
editing**. Four levels, picked by environment:

### 5a. Inline print (works everywhere, the floor)
The `bind -x` function prints the help below the prompt; readline redraws the
prompt+line underneath (same mechanism as completion listing). Simple and
universal; it scrolls a few lines of history. Fine for a compact help blurb.

### 5b. tmux popup — `tmux display-popup` (the slick one)
If `$TMUX` is set, render help in an overlay that doesn't touch the command line
at all:
```bash
_ssql_show_help() {   # $1 = help text
    if [[ -n "$TMUX" ]]; then
        tmux display-popup -w 80 -h 24 -E "printf '%s' \"$1\" | \${PAGER:-less -R}"
    else
        printf '\n%s\n' "$1"   # 5a fallback
    fi
}
```
Press the key → popup appears with help for whatever's under the cursor →
`q`/`Esc` dismisses → your line is untouched. Make it **context-aware**: show
the resolved flag, its arg names+types, and a matching example. This is the
fzf-preview experience for help.

### 5c. tmux split pane — live help that follows your editing
`tmux split-window` a small pane that shows help and **refreshes** as you move
through the line:
- In the **autocli-shell**, trivial: the keystroke callback already fires per
  key, so on each keystroke it can push the current `HelpAt` to the pane (write
  to the pane's tty, or `tmux send-keys`/a fifo). A genuinely live "what am I
  typing" panel.
- In **bash**, it can only refresh on the help key (bind -x is on-demand), so
  bash gets "pinned pane, refresh on `Alt-h`", the shell gets "live pane".

### 5d. In-shell overlay (autocli-shell only, future)
The shell owns the terminal, so it could draw a help box inline (à la fzf's
preview) without tmux — manual escape codes or a small TUI lib. Heavier; only
worth it if we want the experience without requiring tmux.

**Selection logic:** autocli-shell overlay (if implemented) → tmux popup/pane
(if `$TMUX`) → inline print. Always degrades to something.

## 6. The tmux angle, in depth (the fun part)

`tmux display-popup` and `split-window` turn the terminal into a little IDE:

- **Context popup** (`display-popup`): transient, on a key, shows help for the
  token under the cursor. Best default when in tmux — zero disruption.
- **Pinned help pane** (`split-window`): a side/bottom pane that tracks what
  you're building. The autocli-shell feeds it live; bash refreshes on demand.
- **Tutor mode**: take it further — the pane shows the command's *examples* and
  the resulting *output schema* (`SSQL_MODE=schema | generate schema`, which we
  already have) as you assemble the pipeline. "Here's what this stage produces."
- **Detection + degradation**: `[[ -n "$TMUX" ]]` gates the rich path; outside
  tmux it's inline text. No hard dependency on tmux.

This is the user-facing "wow": build a pipeline with a live help/preview pane,
without leaving bash.

## 7. Where the content comes from (grounded)

Already in the tree (verified): `FlagSpec.Description`, `FlagSpec.ArgNames`,
`FlagSpec.ArgTypes`, command `Description`, and `Example`s; `GenerateHelp*`
render whole commands. `HelpAt` assembles the cursor-relevant slice from the
same data — so the help is as good as the command's existing help text (which,
for ssql, is required to be decent).

## 8. Proposed shape

- **autocli core:** `Command.HelpAt(args, pos) (*HelpResult, error)` (reuses
  `analyzeCompletionContext`) + a `-help-at "<line>" <point>` protocol flag
  (parallel to `-complete N`) that renders `HelpResult` to text. *This is the
  general piece the idea is really about.*
- **autocli-shell:** a help-key branch in `AutoCompleteCallback` + a renderer,
  with the live-pane option.
- **A small display helper** (autocli or a tiny package): adapters
  (inline / tmux-popup / tmux-pane) selected by `$TMUX`.
- **ssql (consumer):** `ssql -help-keybinding` emits the bind + the tmux-aware
  display wrapper — same emit/install pattern as `-field-keybinding` /
  `-optimise-keybinding`, listed in the bare-`ssql` hint, pty-tested per the
  CLAUDE.md rule.

## 9. Open questions

- **Key.** `Alt-h` (robust, mnemonic) vs `Shift-Tab` (`"\e[Z"`, intuitive but
  terminal-variant) vs a chord (avoid — keyseq-timeout). Make it rebindable;
  pick a robust default.
- **Granularity.** Flag-under-cursor vs whole-command vs "next expected token."
  Probably: flag/arg if the cursor is on one, else command-level.
- **Live pane mechanics.** Feeding a tmux pane per keystroke from the
  autocli-shell — tty write vs fifo vs `send-keys`; which is least racy.
- **Popup vs pane default in tmux.** Popup (transient, on a key) is the safer
  default; pinned pane is opt-in.
- **Rendering.** Plain vs colour; width-aware wrapping; how much (one-liner vs
  full flag table vs examples).
- **Non-tmux rich path.** Worth a 5d in-shell overlay, or is inline + tmux
  enough?

## 10. Prior art

- **fzf** — `--preview` in a tmux popup/split is the model for "context panel
  while editing."
- **zsh / fish** — native descriptions inside completion; the thing bash can't
  do, which justifies a dedicated help key here.
- **`cmd -h` muscle memory** — this is "help without leaving the line."

## 11. Why it's a natural next step

It's the third member of the `READLINE_LINE` / `(line,pos,key)` action family
already shipped — `Ctrl-O` (complete from upstream schema), `Ctrl-T` (optimise
in place), and now `Alt-h` (explain under the cursor). Same trigger pattern,
same emit/install, same pty-test discipline. The new work is one autocli
primitive (`HelpAt`, mostly a renderer over existing context analysis) and the
display adapters — with tmux making it genuinely delightful.
