# Authority Survey: Where DFC115 Thinking Can Still Improve the Code

Reference: DFC116
Created: 2026-08-26
Last modified: 2026-08-26

[Back to Index](./README.md)

**Status:** Survey — findings ranked, fixes queued as separate units.
Builds on [DFC115](./dfc115_commands_are_the_authority.md) (the
principle) and the codegen IR plan (`codegen-ir-evolution.md`, the
known-largest violation's remediation).

## Purpose and method

DFC115 says: a command owns its grammar, format knowledge, and
semantics; consumers ask through protocols, never re-implement. Every
violation so far was found by its bug (serve's from-parser took three
drift bugs; the completion round found autocli parsing files it
didn't own). This survey looks for the NEXT bug's home before it
ships.

Violations have greppable signatures — code outside a command that:

- switches on file extensions (`case ".csv"`),
- tokenizes or pattern-matches stage strings / argv of other commands,
- keeps lists of command names or flag names it doesn't own,
- reads a file format autocli/ssql already has a reader for.

The scan is minutes; the work is the judgment call per site:
*second implementation that can drift* vs *legitimate consumer of a
protocol*. Both appear below, because the compliant sites are the
templates for fixing the violations.

## Compliant patterns (leave alone; copy their shape)

- **serve's head row count** — execs `from … -records` (the DFC115
  founding fix).
- **`/api/optimize`** — execs the CLI's own optimizer; serve never
  learned rewrite rules.
- **Completion field/value hooks (v4.73.0)** — exec self under
  `SSQL_MODE=schema`; autocli asks the tool.
- **`validateReadonly`** — deliberately refuses to copy `to`'s
  grammar: any unclassifiable bare token rejects, false positives
  safe, errors say why. Honest conservatism is DFC115-compatible
  (see W2 for the eventual protocol).
- **Hint tokens (`Use-Ctrl-O`)** — a deliberate one-word protocol
  between engine, bash binding, and browser UI; single source
  (`FieldHintToken`) with a consistency test on the bash side.

## Findings, ranked by drift-risk × fix-cost

### F1. Extension→format routing exists in ~7 places (HIGH risk, LOW cost)

`from.go` (bare-form routing — the authority), `aux_input.go`
(direct-file join/merge/union), `union.go`, `from_records.go`,
`serve_http.go` (~line 680), `cursor_context.go` (~line 309),
`completion_sources.go`. Seven copies of "what does `.parquet`
mean". A new format (or teaching `.arrow` a new trick) takes seven
edits and someone will miss one — this is the registration-drift bug
shape that cost the playground six commands for months.

**Fix:** one shared routing/capability table in the commands package
— `formatForPath(path) (format, caps)` — colocated with `from`'s own
routing so the knowledge lives once. Callers keep their differing
*reactions* (route, refuse, suggest); they stop owning the *facts*.

### F2. serve encodes which formats support `-sample` (HIGH risk, folds into F1)

The `serve_http.go` switch doesn't just route — it knows `from csv
FILE -sample N` is valid but parquet needs the `sample` stage
fallback. That is `from`'s FLAG grammar living in serve, exactly the
knowledge class that produced the three drift bugs. Fold a
`sampleable` capability bit into the F1 table.

### F3. `ExprArgAtCursor` hardcodes expression-slot grammar (MED risk, MED cost)

`cursor_context.go` maps where/update/group-by flags to expression
arg indices for the Alt-h function reference. Add an expr flag
anywhere (or a new expr-bearing command) and Alt-h silently loses it
— the degraded mode looks fine, the W35 lesson. **Fix:** the flag
should declare it — autocli arg metadata (`.Expression()` on the arg
builder) surfaced through the cursor protocol; the map gets derived,
not hand-written. Pairs with the next autocli release.

### F4. Cursor slot-mapping knows from/join grammar (MED risk, MED cost)

`fromOwnFileFieldSlot` / `joinRightFile` in `cursor_context.go`
pattern-match other commands' stages to find file and field slots.
It IS the cursor protocol's implementation, but its facts (which
flags take files, which arg is the right-side field) are declared in
each command's autocli builder already — derive from the flag specs
(`FieldsFromFlag`, `FilePattern`) instead of re-stating them. Same
autocli-metadata vehicle as F3.

### F5. `capByEarlyLimit` recognizes `limit N` by token (LOW risk, LOW cost)

`serve_http.go:1079`. One command, two tokens — but it's semantic
knowledge ("this stage bounds output") stated outside `limit`. If a
`-records`-style protocol ever grows past stage 0 (row-bound
metadata flowing through `SSQL_MODE=schema` headers), fold this in;
until then it's a contained, documented copy. Cheapest correct move
today: nothing.

### F6. Browser `FIELD_HINTS` list can drift from the engine (LOW risk, TINY cost)

`ssql-ui.js` matches hint tokens `['Use-Ctrl-O', '<FIELD>', …]`.
The tokens are a protocol on purpose, but only the bash side has a
consistency test (`TestFieldHintTokenConsistent`). **Fix:** extend
that test (or a registration-drift-style one) to assert the JS
literal matches `FieldHintToken`/`ValueHintToken`.

### F7. SQL assembler parses fragment Command strings (KNOWN, scoped elsewhere)

The documented legacy exception. Remediation is the IR evolution
plan (`codegen-ir-evolution.md`) — fragments grow structured fields,
the assembler stops string-parsing. Its own project; listed here for
completeness so the survey is the one place to look.

## Protocol-growth candidates the survey suggests

- **W1 (from F1/F2):** shared format/capability table — format,
  reader, sampleable, direct-aux-readable, schema-mode-capable.
- **W2 (from validateReadonly):** commands declare "writes files /
  executes" via flag metadata; readonly asks instead of judging
  tokens. Removes the false positives without copying grammar.
- **W3 (from F3/F4):** autocli arg metadata (expression slots, file
  slots, field slots) exposed through the cursor protocol — the
  builder declarations become the single source for every cursor
  consumer (Alt-h, Ctrl-O, browser).

## Suggested order

1. F1+F2 (one unit, immediate drift-risk reduction)
2. F6 (one test, minutes)
3. F3+F4 via W3 (next autocli round)
4. W2 (when readonly's conservatism first annoys someone real)
5. F7 rides the IR evolution plan

Re-run the smell scan (grep signatures above) after each major
feature; it costs minutes and this survey is where the results go.
