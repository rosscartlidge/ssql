# Commands Are the Authority on Themselves

Reference: DFC115
Created: 2026-08-25
Last modified: 2026-08-29

[Back to Index](./README.md)

**Status:** Principle — adopted (Ross, 2026-08-25). Recorded so the
reasoning survives; the CLAUDE.md Development Principles carry the
operative rule.

## The principle (Ross's formulation)

> "The commands that manipulate data are the best place to get
> information on the data — not by parsing the command externally or
> trying to guess what it does. The command knows what it does —
> which is why it is so good at producing equivalent Go code."

A command owns its flag grammar, its format knowledge, and its
semantics. Any consumer that needs facts about what a command reads,
produces, or means should ASK THE COMMAND through a protocol, never
re-derive those facts by parsing its arguments elsewhere. An external
parse is a second implementation of the command's grammar, and second
implementations drift — that is the entire lesson of the
one-semantics-many-backends doctrine, applied to metadata.

## The evidence

The pattern was proven positively and negatively in the same week:

**Protocol answers (the pattern working):**

| Question | Protocol | Owner |
|---|---|---|
| "What completes here?" | `-complete N words…` | the command's own completers |
| "What does this flag mean?" | `-help-at` | the command's own help |
| "What fields does this pipeline produce?" | `SSQL_MODE=schema` + `generate schema` | every command's schemaOp |
| "What Go code is this pipeline?" | `-generate` fragments | every command's codegen |
| "How many records would this read?" | `from … -records` | from's format knowledge |

**External parsing (the anti-pattern failing):** serve's throughput
display first re-parsed `from`-args with its own little grammar to
estimate input rows. Three drift bugs in one week — the `parquet`
subcommand word taken as a filename, `-columns` values taken as extra
files, `-sample` handled separately from the command's own rules —
each one a place where the copy disagreed with the owner. The fix
was not a better copy; it was deleting the copy: serve now execs
`stage0 + ["-records"]` and the from command answers with its own
grammar. The bug class died structurally.

## Why this is the same insight as codegen

`-generate` works because each command emits the Go for ITS OWN
semantics — nobody externally infers what `group-by -cube` means.
`-records` is the identical move for a smaller question. The general
form: **when a consumer needs a new fact about a command, grow the
command a protocol surface for that fact** (a flag, an env mode, a
fragment field) rather than teaching the consumer to read the
command's mind.

## Known tension, stated honestly

`generate sql` parses fragment `Command` strings externally — the SQL
assembler re-derives per-command meaning from argv text, and has had
its own drift bugs (the stale `top` translation; the `-sample` value
read as a filename). This predates the principle and is tracked in
[codegen-ir-evolution.md](./codegen-ir-evolution.md) ("the Command
string is the IR the optimiser wishes it had"): the eventual cure is
commands emitting structured facts in their fragments instead of the
assembler parsing strings. New consumers must not copy the SQL
assembler's approach.

## Operative rule (mirrored in CLAUDE.md)

Before writing code that parses another command's arguments or infers
its behavior: stop, and add a protocol answer to the command instead.
If the fact is about a pipeline rather than one command, thread it
through the existing pipeline protocols (schema mode, fragments).

## The principle, matured (2026-08-29): commands own the CONFIG too

The original formulation covered grammar and semantics — consumers
ASK, never re-parse. The DFC117→DFC119 arc taught the second half,
articulated by Ross: **the ssql commands should own all the logic
AND config.** Any state that determines what the user sees or gets
must be expressible as command text, because command text is the
only state that is:

- **durable** — it rides share links, Copy CLI, files, and history;
- **inspectable** — you can read it, diff it, and paste it in a bug
  report;
- **single-parsed** — one grammar, one tokenizer, one meaning.

State held anywhere else is a second store, and second stores drift
exactly like second parsers. The evidence accumulated fast:

| State outside command text | What happened | Cured by |
|---|---|---|
| Widget-builder step state | third grammar surface, one-way sync | DFC117 (deleted) |
| Grid filter/sort ops | invisible segment; ran on stale data; Copy CLI lied | DFC118 Phase 2 (clicks write the bar) |
| Chart X/Y/type picks | three stale-closure clobbers; unshareable | DFC119 (config becomes a `to chart` stage) |

**Corollary: a UI is a VIEW and an EDITOR of command text — never an
owner of state.** The workspace's job is to render the pipeline and
to write gestures back INTO it (grid clicks, axis dropdowns, future
palette/forms); the moment it keeps its own copy of anything the
pipeline could express, the copy is a bug that hasn't fired yet.
Growing a command's grammar to hold new state (DFC119's `to chart`
extensions) is the correct move, not a workaround.
