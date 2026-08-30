# The Display-Sink Protocol: Chart, Animate, and Every Visual After Them

Reference: DFC119
Created: 2026-08-29
Last modified: 2026-08-30

[Back to Index](./README.md)

**Status:** Phase A SHIPPED 2026-08-30 (autocli v4.17.0
`.DisplaySink()` in -spec-json; the generic spec-driven decoder; a
trailing `to chart` stage strips from execution and drives the
panel; controls edit the stage via the model, appending on first
touch; undecodable stages execute loudly; the renderer REGISTRY ships in
Phase A too — dispatch by sink key, unregistered keys fail loudly —
so Phase B's acceptance test is meaningful from day one). Phase B
(animate: one declaration + one renderer registration, NO dispatch
edits — scheduled post-release) and C (one renderer, both worlds)
pending. Trigger: head reruns
lost X/Y chart picks (a stale-closure bug, fixed) and Ross's
observation that the chart config should be a PIPELINE STAGE — plus
"we want a nice extensible architecture — there are other graphical
output formats like `ssql animate`; we need a unified architecture
for these and any more we do."

Builds on: [DFC118](./dfc118_bijective_query_builder.md) (the model:
one pipeline, gestures as model edits), [DFC115](./dfc115_commands_are_the_authority.md)
(commands own their grammar), DFC107/108 (one engine, split
workspace).

## The problem, generalized

The workspace has visual state that is not in the pipeline: chart
type, X/Y/Z picks, log axes, color field — and tomorrow animate's
frame field and speed. State outside the model is exactly what
DFC118 killed for grid ops: it can't ride share links, Copy CLI
can't reproduce it, and it gets lost (the axis-clobber bug was only
the symptom — ANY state outside the bar is one stale closure away
from vanishing).

Meanwhile the terminal already HAS the grammar for this state:
`to chart -x dept -y total -type bar -log-y`, `to animate -frame ts
-x … -type …`. Two visual commands today; more later. Without an
architecture, each new one grows ad-hoc workspace state and a
second config surface — N commands × 2 implementations.

## The architecture in one sentence

A **display sink** is a terminal pipeline stage whose argv IS the
visualization config: the terminal executes it (self-contained HTML
artifact out), the workspace RECOGNIZES it (data pipeline runs up to
it, one shared renderer draws it live), and the workspace's controls
are EDITORS of that stage via the DFC118 model — so visual state
lives in the bar, shareable, copyable, and unloseable by
construction.

## The pieces

### 1. Declaring a sink (commands are the authority)

autocli grows one bit of metadata: `.DisplaySink("chart")` on the
subcommand builder, carried into `-spec-json` (`"displaySink":
"chart"`). The workspace asks the spec — never keeps a list of which
commands are visual. A new visual command declares itself and the
workspace's dispatch finds it with zero page changes.

### 2. One decoder, N renderers

The page has ONE generic argv→config decoder, driven by `-spec-json`
(flag names → config keys, arity/bool/accumulate from the spec — no
renderer ever hand-parses argv; that would be N little grammar
copies, the DFC117 disease). Renderers are JS modules registered by
sink key: `chart` → the Plotly panel, `animate` → the frame player.
Adding a visual = write the command + register one renderer function
`render(rows, config, mount)`.

### 3. Workspace execution semantics

If the bar's LAST stage is a display sink (per spec), the runner
splits: data stages execute through the engine as today; the sink
stage is decoded, not executed, and its renderer draws the result
rows. No sink stage → today's behavior (grid + auto-picked chart).
Sinks are terminal-only (mid-pipeline sink = normal loud error from
the engine, unchanged).

### 4. Controls are stage editors (Phase 2's ownership, reused)

Changing the X dropdown rewrites `-x` in the trailing sink stage via
the model (parse → edit flag → print → rerun), exactly like grid
clicks. If there is NO sink stage yet, the first control touch
APPENDS `| ssql to chart -x … -y …` — the controls teach the
grammar the same way the grid does. The stage is user-visible text;
hand-edits win by the same rules as Phase 2.

### 5. One renderer, both worlds (the drift killer)

Today `to chart`'s standalone HTML and the workspace's chart panel
are two implementations of "draw these rows". The end state: the
terminal artifact embeds the SAME renderer module (an
`ssql-ui-viz.js` shared layer, like ssql-ui.js) so a chart looks
identical from `ssql … | ssql to chart out.html` and from the
workspace. One semantics, applied to pixels. (Until then, the two
coexist — but no NEW visual should ship two renderers.)

## What this replaces

- The workspace's free-floating chartType/xField/yField React state
  (kept only as the no-sink fallback, fed BY the sink stage when
  present).
- The temptation for `animate` (and successors) to grow their own
  workspace panels and state.
- Nothing about terminal behavior: `to chart`/`to animate` keep
  emitting standalone artifacts; the protocol only ADDS the live
  rendering path.

## Phases

- **A (the pattern, on chart):** autocli `.DisplaySink()` +
  spec-json field; the generic decoder; chart renderer keyed in;
  controls edit the stage; axis state derived from the bar. Gates:
  sink round-trips the model laws; controls-edit gates mirror the
  Phase 2 five; share/Copy CLI reproduce the chart by construction.
- **B (animate):** register the second renderer — the test of
  extensibility is that B touches no dispatch code.
- **C (one renderer, both worlds):** extract ssql-ui-viz.js; the
  standalone artifacts embed it.

## Open questions

1. Does the workspace RESPECT `to chart FILE` args (writing into the
   vfs) or treat the file arg as terminal-only and ignore it live?
   Leaning: decode and ignore FILE in the workspace, loudly noted in
   the status.
2. Multi-Y (`-y` accumulate) and the resizable-frame settings —
   grammar extensions to `to chart` that the workspace needs; do
   them in Phase A so the stage can express what the panel can show.
3. `to explore` itself is a sink that produces the workspace — it
   stays OUT of the protocol (a workspace inside a workspace is
   recursion nobody asked for).
4. ~~Implicit vs always-written~~ — RESOLVED (Ross, 2026-08-30):
   ALWAYS WRITTEN ("I don't like implicit stuff"). Every successful
   run without a sink stage writes one reflecting the panel's final
   state (text-only, no rerun); the bar always spells the chart
   being shown. Consequence handled: grid stages insert BEFORE the
   trailing sink (sort-after-chart would be a broken pipeline).
