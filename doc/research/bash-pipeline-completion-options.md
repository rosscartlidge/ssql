# Bash Pipeline-Aware Completion — Constraints & Options

**Status:** Decision doc. Written 2026-06-17 after `SSQL_MODE=schema` shipped
(v4.46.0/.1) and bash *pipeline* completion was found not to work. The schema
engine and `ssql serve` completion are fine; this doc is only about wiring
pipeline-aware completion to a **bash** keypress. Pick an option, then implement.

Related: [schema-aware-completion.md](schema-aware-completion.md) (the feature),
TODO.md entry 95 (leftovers) + entry 97 (multi-key bindings).

## 1. What we wanted

```
ssql from data.csv | ssql rename -as name person | ssql group-by <TAB>
#   → person  dept  …      (fields flowing in from the upstream stages)
```

`ssql serve` does this already (it has the whole line and full process control).
The goal was the same at a plain bash prompt.

## 2. Why the shipped approach can't work (root cause, proven)

The `-completion-script` wrapper reconstructs the upstream from `COMP_LINE` /
`COMP_WORDS` inside a `complete -F` function. **bash does not put the upstream
there.** It scopes `COMP_LINE`/`COMP_WORDS`/`COMP_POINT`/`COMP_CWORD` to the
*current simple command* — everything after the last `|`.

Proven with a real pty (bash 5.2.21), with a control case to validate the
harness. The completion for `xtest` just dumps its `COMP_*` variables:

| Case | `COMP_LINE` | `COMP_POINT` |
|---|---|---|
| control — `xtest alpha <TAB>` (no pipe) | `xtest alpha ` | 12 |
| `echo beta \| xtest alpha <TAB>` — native bash | `xtest alpha ` | 12 |
| `echo beta \| xtest alpha <TAB>` — with bash-completion | `xtest alpha ` | 12 |

The control proves the harness is sound: for a single command, `COMP_LINE` *is*
the full command. Prepend `echo beta |` and `COMP_LINE` is **still** just
`xtest alpha ` — the upstream is gone. `COMP_POINT=12` is the clincher: 12 is the
length of `"xtest alpha "`, not of `"echo beta | xtest alpha "` (24), so
`COMP_LINE` really *is* the scoped 12-char string, not the full line truncated in
display.

So it is **not** a bug, **not** bash-completion's doing, and **not**
version-specific — it is how bash programmable completion is designed (completion
is per-command). A `complete -F` function fundamentally cannot see the pipeline
to its left. (Reproduction harness in the appendix.)

> **Common misconception.** Bash's manual calls `COMP_LINE` "the current command
> line," and most tutorials (and LLMs) read that as "the whole line you typed,
> pipes and all." It is not — it's the current *simple* command. Snippets that
> `[[ "$COMP_LINE" =~ upstream.*\|.*mytool ]]` to branch on the upstream look
> correct but never match on a real bash after a pipe. The only reliable check is
> to `printf '%s' "$COMP_LINE"` from inside a piped completion, as above.

The **only** place the full command line is available to shell code is
`READLINE_LINE` (+ `READLINE_POINT`), and bash only populates those for **`bind -x`
key bindings** — never for completion functions.

## 3. What already works (do not regress these)

- **`ssql serve` completion** — in-process pipeline walk; full feature, no bash
  limitation. The reference experience.
- **Single-command bash field completion** — `ssql from data.csv -if <TAB>`
  reads the file's header natively (autocli `FieldsFromFlag`). Works today,
  no upstream needed.
- **The `SSQL_MODE=schema` engine + `ssql generate schema`** — correct and
  tested. `(export SSQL_MODE=schema; <pipeline>) | ssql generate schema` prints
  the resulting field list near-instantly (sources read only the header). This
  is the right primitive; the *only* missing piece is a bash keypress that can
  hand it the full line.

## 4. Options

### Option A — dedicated `bind -x` chord (fzf-style) — **recommended if we want bash**

Bind a chord (e.g. `Ctrl-X Ctrl-F`) via `bind -x` to a function that:

1. reads `READLINE_LINE` (full line ✓) and `READLINE_POINT` (cursor),
2. takes the text up to the cursor, strips from the last top-level `|` → the
   upstream pipeline,
3. runs `(export SSQL_MODE=schema; <upstream>) | ssql generate schema` to get the
   fields,
4. inserts the single match (or longest common prefix) into `READLINE_LINE` /
   `READLINE_POINT`, or prints the candidate list below the prompt.

```bash
# sketch — emitted by ssql, sourced in ~/.bashrc
_ssql_field_key() {
    local line="${READLINE_LINE:0:$READLINE_POINT}"
    [[ "$line" == *"|"* ]] || return
    local upstream="${line%|*}"
    local fields
    fields=$( (export SSQL_MODE=schema; eval "$upstream") 2>/dev/null \
              | command ssql generate schema 2>/dev/null )
    # … insert common prefix into READLINE_LINE, or print the list …
}
bind -x '"\C-x\C-f": _ssql_field_key'
```

- **Pros:** works on *any* bash; **Tab stays normal**; self-contained; proven
  pattern (fzf binds `Ctrl-T`/`Ctrl-R`/`Alt-C` exactly this way and never touches
  Tab); we control the UX precisely.
- **Cons:** it's a chord to learn, not Tab; we implement the insert/list UX
  ourselves (readline doesn't do it for `bind -x`); only fires where bound.
- **UX to design:** single field → insert + trailing space; multiple → print the
  list, leave the line unchanged (readline redraws); none / undeterminable
  (`pivot`) → do nothing.
- **Effort:** ~½ day including a pty-based test harness so it's verified for real
  (the lesson from the Tab attempt: never validate completion with a hand-built
  `COMP_*` — drive a real pty).
- **Risk:** low. Worst case the chord prints nothing and the line is untouched.

### Option B — rebind `Tab` itself via `bind -x` — **not recommended**

Bind `"\t"` to a function that does schema-completion when applicable and normal
completion otherwise.

- **Blocker:** from a `bind -x` function you **cannot** fall through to readline's
  built-in completer. To preserve normal Tab you would have to reimplement *all*
  of it inside the function — command names, file/dir completion, flag
  completion, and the autocli `-complete` protocol — and keep it in sync.
- **Pros:** seamless Tab (if it worked).
- **Cons:** large, fragile, easy to regress everyday completion, terminal-quirk
  prone. The payoff (Tab vs a chord) is not worth the cost or risk.

### Option C — accept the limitation, fix the claim — **do this now regardless**

- Bash keeps single-command file completion (works); pipeline-aware completion is
  **serve-only**.
- Correct the v4.46.x CHANGELOG and `doc/cli-codelab.md` (they currently overclaim
  bash pipeline completion), and downgrade or drop the `-completion-script` schema
  wrapper (it's a harmless no-op for pipes — it always falls through — but it
  shouldn't pretend to work).
- **Pros:** honest, zero risk, no new UX surface.
- **Cons:** bash users don't get pipeline-aware completion (they have serve + the
  `generate schema` one-liner).

### Option D — sidestep bash — **noted, not proposed**

- `zsh`/`fish` have richer completion APIs that *may* expose the full line; would
  need its own investigation, and means a second completion implementation.
- A REPL avoids bash entirely — and `ssql serve` (or a future local `ssql shell`)
  already gives the full experience. If "interactive pipeline building with great
  completion" is the real goal, pointing people at the REPL may beat fighting
  bash.

## 5. Comparison

| Option | Works in bash? | UX | Effort | Risk |
| --- | --- | --- | --- | --- |
| A — `bind -x` chord | ✅ any bash | a chord (not Tab) | ~½ day | low |
| B — rebind Tab | ✅ but reimplements all completion | seamless Tab | large | high |
| C — document, serve-only | ❌ (single-cmd only) | n/a | ~1 hr | none |
| D — zsh/fish or REPL | partial / different | varies | unknown / done | varies |

## 6. Prior art

`fzf` is the canonical example of "inject completion that needs the whole line":
it binds `Ctrl-T` (paths), `Ctrl-R` (history), `Alt-C` (cd) with `bind -x`,
reading `READLINE_LINE`/`READLINE_POINT`, and **never rebinds Tab**. That this
pattern is ubiquitous and Tab is left alone is strong evidence for Option A over
B.

## 7. Recommendation

- **Option C immediately** — the docs are actively wrong; correct them and stop
  the wrapper pretending. (~1 hr, its own small release.)
- **Option A as the real feature** if bash pipeline completion is wanted — it's
  the only mechanism that *can* work, it's low-risk, and Tab stays normal.
  Prototype it and prove it in a pty before claiming anything.
- Keep selling the **REPL** (`ssql serve`) as the first-class interactive
  experience; bash is the constrained environment, not the other way around.

## Appendix — reproduction harness

Drives a real pty (no `expect` needed), registers a completion that dumps its
variables, and completes both a bare command (control) and a piped command. Run
with `python3`. `--norc --noprofile` ensures no bash-completion in the native
runs.

```python
import os, pty, time, select
def run(setup, type_line, dump="/tmp/comp_dump.txt"):
    try: os.remove(dump)
    except: pass
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp("bash", ["bash", "--norc", "--noprofile", "-i"])
    def send(s): os.write(fd, s.encode()); time.sleep(0.5)
    def drain():
        while select.select([fd],[],[],0.3)[0]:
            try: os.read(fd, 4096)
            except OSError: break
    time.sleep(0.6); drain()
    for c in setup: send(c+"\n"); drain()
    send(r'''_dbg() { printf 'LINE=[%s] POINT=[%s] WORDS=[%s]\n' "$COMP_LINE" "$COMP_POINT" "${COMP_WORDS[*]}" > ''' + dump + r'''; COMPREPLY=(); }'''+"\n"); drain()
    send("complete -F _dbg xtest\n"); drain()
    send(type_line + "\t"); time.sleep(0.8); drain()
    send("\x03\n"); time.sleep(0.2); os.close(fd)
    return open(dump).read().strip()

print("CONTROL (no pipe):", run([], "xtest alpha "))
print("PIPE native       :", run([], "echo beta | xtest alpha "))
print("PIPE bash-comp    :", run(["source /usr/share/bash-completion/bash_completion"], "echo beta | xtest alpha "))
```

Result on bash 5.2.21:
- control: `LINE=[xtest alpha ] POINT=[12] WORDS=[xtest alpha ]`
- pipe (native): `LINE=[xtest alpha ] POINT=[12] WORDS=[xtest alpha ]`  ← upstream dropped
- pipe (bash-completion): same as native.

The control's full `COMP_LINE` validates the harness; the pipe runs' identical
`POINT=12` proves the upstream is genuinely absent (not display truncation).
