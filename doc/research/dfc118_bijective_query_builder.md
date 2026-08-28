# A Widget Query Builder Done Right: Spec-Driven and Bijective

Reference: DFC118
Created: 2026-08-27
Last modified: 2026-08-28

[Back to Index](./README.md)

**Status:** Phases 1+2 SHIPPED 2026-08-28. Phase 1: autocli v4.16.0
`-spec-json` + the model layer with round-trip law gates. Phase 2:
grid gestures are model edits — clicks parse the bar, replace their
own trailing stages, print, and rerun through the ONE run path;
the parallel ops path, the grid: truth line, and the Copy CLI
side-channel are deleted (the bar IS the truth). Phase 3 (the
widget forms) unscheduled. Ross: "we do need to think
about a widget based query builder — but we need to do it right and
follow our principles — and it needs to be bijective."

Builds on: [DFC117](./dfc117_explore_widgets_removal.md) (why the
old builder died), [DFC116](./dfc116_authority_survey.md) /
[DFC115](./dfc115_commands_are_the_authority.md) (commands are the
authority; the cursor-derivation machinery), DFC108 (the split
workspace the builder would live in).

## Terms (the explore workspace, for new readers)

`ssql … | ssql to explore out.html` produces a self-contained
browser workspace (also what `ssql serve` serves). Its surfaces:

- **The grid** — the data table (AG-Grid). Clicking its column
  filters/sorts emits real ssql stages.
- **The bar** — the multi-line text box above the grid holding the
  pipeline as ordinary ssql text ("ssql from data.jsonl | ssql
  where …"). It has full completion (suggestions as you type, Tab,
  Ctrl-O-style field/value lookup, Alt-h help) and runs in the
  page's embedded wasm build of the REAL ssql engine — "Run local
  pipeline".
- **The head** — in served mode only, a second text box above the
  bar whose pipeline runs on the `ssql serve` host ("Run server
  pipeline"); its result feeds the bar's pipeline as data.jsonl.

A proposed widget builder would be a fourth surface for AUTHORING
what the bar holds — which is why every requirement below is stated
relative to the bar.

## Why revisit at all

DFC117 removed the widget builder because of how it was BUILT, not
because visual construction is worthless. A builder that actually
worked would serve: newcomers who don't know the grammar yet (the
GopherCon audience seeing ssql for the first time), discoverability
(what CAN this stage do? — a rendered form answers at a glance), and
touch devices where typing pipelines is genuinely painful. The grid
already proves the pattern in miniature: click a column filter, watch
the bar spell it in real ssql — the GUI teaching the CLI. A right
builder is that, generalized.

## Post-mortem constraints (what "right" means)

The dead builder failed four ways; each failure becomes a hard
requirement:

1. **It hand-copied grammar** (operator lists, agg menus, window
   funcs — a third grammar surface that drifted; its `valueFlags`
   map had csv flags on parquet). → **R1: the builder must contain
   ZERO command knowledge.** Every form, menu, and vocabulary item is
   DERIVED from the commands' own self-descriptions at render time.
2. **It covered ~8 of 30+ commands** — partial coverage taught a
   dialect. → **R2: coverage is total by construction** — anything
   registered renders; a new command appears in the builder with no
   builder change.
3. **It was one-directional** (steps → text; bar edits never parsed
   back; "last writer wins"). → **R3: bijective.** Text and widgets
   are two VIEWS of one model, editable in either, always in sync.
4. **It had no completion** while the bar had full completion. →
   **R4: widget inputs get the same completion the bar gets — by
   asking the same protocols.** Concretely: when you Tab in the bar
   today, the completion binding calls the engine's protocol
   surfaces — `-complete N <argv>` ("what can go at this cursor
   position?" — where's operator enum answers from where's own
   StaticCompleter), `SSQL_MODE=schema` ("what fields exist at this
   point in the pipeline?" — correct even after rename/group-by),
   the value-sampling path ("what values does this field hold?"),
   and `-help-at` ("what does this flag mean?"). R4 says a widget is
   just a different RENDERING of those same answers: the operator
   dropdown is the `-complete` result at the operator's arg position
   shown as a select instead of a Tab popup; the field picker is the
   schema-mode result; the value suggester is the sampling call.
   Same questions, same answerer (the command, via the engine),
   different widget on the front. This is what ENFORCES R1: if
   widgets may only display protocol answers, there is nowhere for a
   hand-copied vocabulary to live — the dead builder's
   `['eq','ne','gt',…]` array becomes structurally impossible, and a
   command that gains an operator updates the widget with zero
   builder changes.

## The design in one sentence

The builder is a FORM RENDERER for the command tree's own
declarations, whose model is the parsed pipeline itself (argv per
stage, parsed by the real parser), and whose every dynamic vocabulary
(fields, operators, values) comes from the completion/schema
protocols the bar already uses.

### R1+R2: spec-driven rendering (commands are the authority)

The machinery mostly exists after the DFC116 arc:

- autocli builders already declare everything a form needs: flag
  names/aliases, arg names, arg types, Required/Accumulate/Bool,
  `Expression()` arg marks, `FieldsFromFlag` (a field slot!),
  StaticCompleter (an enum — where's operators live HERE, in where's
  own declaration), FileCompleter (a file slot), help text, examples.
- What's missing is one protocol surface: a machine-readable dump of
  the FlagSpec tree — `ssql -spec-json [command]` (autocli feature,
  same registry help/man generation reads). The builder renders forms
  from THAT. Growing the command a protocol surface instead of
  parsing it elsewhere is the DFC115 playbook verbatim.
- Dynamic vocabularies stay live, not dumped: field names via
  `SSQL_MODE=schema` at the stage's boundary (the Ctrl-O path — so
  widgets see post-rename/group-by schemas correctly, which the dead
  builder never did), values via the sampling path, enums via
  `-complete` at the arg's position. The embedded engine answers all
  three in-page today.

A form field is therefore: label = arg name, control = derived from
the declaration (checkbox for Bool, select fed by -complete for
enums/fields, text with expression help for Expression args, repeat
button for Accumulate), tooltip = the flag's own Help text. Nothing
authored per command, ever.

### R3: bijectivity, stated precisely

The model M is the parsed pipeline: a list of stages, each an argv
(plus stdin/procsub structure), produced by the REAL parser (the
engine's parse exposed through the wasm shim — never a JS re-parse).
Two functions:

- `parse : Text → M` (the engine's own tokenizer/splitter)
- `print : M → Text` (a canonical serializer: one space between
  tokens, shell-quoting exactly where needed, ` | ` between stages)

The laws we enforce (true bijection over raw strings is impossible —
whitespace variants exist — so we state the achievable pair):

1. `parse(print(m)) == m` for every model — print loses nothing.
2. `print(parse(t)) == canonicalize(t)` — parsing then printing is
   pure normalization; running canonicalize twice is identity.
3. **Totality by escape hatch**: any construct the form renderer
   can't represent (procsub, comments, a flag the spec dump doesn't
   know, a malformed stage mid-edit) round-trips as an OPAQUE TEXT
   stage widget — shown as text, edited as text, never dropped or
   "repaired". Bijectivity must never force partial coverage back in
   through the side door.

Editing either view mutates M; the other view re-renders from M.
There is no "sync" code with ordering bugs because there are not two
states — the dead builder's "last writer wins" comment was the
autopsy of exactly that mistake.

### R4 and the unification prize

With M in place, the THREE gesture surfaces collapse into one
architecture: bar edits = parse into M; widget edits = M mutations;
grid gestures (filter/sort clicks) = M mutations too — today they run
a parallel ops path that merges awkwardly with bar state. One model,
three views, one run path (print(M) through the engine). The
row-count/status story also unifies.

## Testing (gates before features, per the house rules)

- **Round-trip corpus gate**: laws 1–2 property-tested over the
  pipeline corpus we already maintain (corpus_test pipelines + the
  harness pipelines + shared-link pipelines), in the browser harness
  against the real wasm parse. A gate we watch fail: mutate the
  serializer's quoting and the corpus must scream.
- **Total-coverage gate**: for every registered command, the spec
  dump renders a form (no exceptions list allowed to exist).
- **No-vocabulary gate**: grep the builder source for any literal
  operator/function name — the dead builder's smell, mechanically
  banned.

## What this is NOT

- Not scheduled. The bar+grid workspace is the shipping story;
  this doc exists so the next "should we have widgets?" conversation
  starts from requirements instead of nostalgia.
- Not a visual programming language — no dataflow canvas. Stages in
  a pipe, forms for one stage at a time.
- Not a JS parser. If the model can't come from the real engine's
  parse, the design is dead on arrival (that's R1 again).

## Open questions

1. ~~`-spec-json` shape~~ — RESOLVED (Phase 1): whole tree, one
   call. Live check: 32 commands dump instantly; where's operator
   enum came through with `regex` in it — an operator the dead
   builder's hand-copied list never had. The drift argument, proven
   by its first output.
2. Mid-edit invalid states: forms want field-by-field editing but M
   is argv — likely each stage widget holds a draft argv that only
   commits to M when it parses. Does the draft break law 3 UX?
3. Clause grammar (`+` separators, repeated -if groups) — the forms
   must render clause structure, which the spec dump needs to carry
   (autocli knows it; the dump must not flatten it).
4. ~~Grid migration timing~~ — RESOLVED (Phase 2, next session
   after Phase 1): the ownership rule that made it safe is "the grid
   only removes stages IT appended, only from the tail, only if
   unedited — otherwise everything is user-authored base and new
   stages append". User edits are never touched (gated).
5. Mobile: is the builder the primary mobile surface (typing is the
   pain point there), and does that pull the schedule forward?
