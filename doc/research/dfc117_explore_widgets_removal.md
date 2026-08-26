# Explore Slims to Head + Grid + Bar: Widget Builder and JS Fallback Removed

Reference: DFC117
Created: 2026-08-26
Last modified: 2026-08-27

[Back to Index](./README.md)

**Status:** Agreed (Ross + Claude, 2026-08-26) — "I love the head and
the grid — lets give them a couple of lines of text box to work with."
Builds on [DFC107](./dfc107_wasm_explore_engine.md) (real engine in
explore; TinyGo third-semantics removed) and
[DFC116](./dfc116_authority_survey.md) (grammar copies drift).

## The observation (Ross)

The menu-driven step widgets in the explore workspace go unused: real
sessions use the HEAD input and the GRID. The widgets have no
completion and cover a fraction of the commands — "a creator of tech
debt."

## What the code showed

1. **The widget builder is a third grammar surface.** Eight React
   step types (where/sort/group-by/distinct/limit/compute/pivot/
   window) each carrying hand-copied vocabulary: the where operator
   list, agg-func menus, window-func list. No completion; ~8 of 30+
   commands; drifts silently whenever a command grows.
2. **The widgets are an inferior keyboard.** With wasm present,
   `runPipeline` serializes the steps into the BAR text and calls
   `exploreRunBar()` — every widget click is a worse way to type into
   the bar.
3. **The "no-wasm fallback" is a semantics impostor.** `jsAggregation`
   handles single group-by only (alert otherwise) with
   `parseFloat(x) || 0` coercion — strings silently become 0, which
   is NOT ssql semantics. Exactly the class DFC107 killed. Reachable
   via `-light` and on wasm load failure.

## The decision

Explore is **head + grid + bar, all backed by the real engine — or
plain grid browsing.** Nothing in between.

1. **Delete the step-builder widgets** (React step components, step
   state, steps↔text sync). The pipeline is authored as TEXT in the
   bar — the same grammar the terminal uses, with the same engine
   underneath.
2. **Grow the bar into a small multi-line text box** (~3 rows,
   wrapping, monospace): room to read a real pipeline. Enter runs;
   Shift+Enter inserts a newline; newlines are whitespace to the
   engine (a pipeline may be broken at pipes for readability). Share
   links, Copy CLI, ⚙ Optimize, completion — unchanged, they already
   speak bar text.
3. **The grid keeps its click→ops→bar flow** — column filter/sort
   gestures become real stages written INTO the bar and run by the
   real engine. That is UI mapping, not grammar copying; it stays.
4. **Delete `jsAggregation` and the fallback branch.** `-light` means
   what its help already says: grid browsing only (AG-Grid's own
   local sort/filter, no ssql semantics claimed); the bar area shows
   an honest "pipelines need the embedded engine" notice. Wasm load
   failure shows the same notice — never a semantics impostor.

## Why not keep the widgets for newcomers

The audience argument (DFC116 discussion): ssql's users are terminal
people; the browser's job is to RUN, SHOW, TUNE and SHARE — the
authoring investment (completion, Ctrl-O, Alt-h, cursor protocol)
lives in the CLI and the bar inherits it via the engine. A visual
builder that can't complete and can't cover the grammar teaches a
worse dialect. The grid is the newcomer path: click a filter, watch
the bar spell it in real ssql — the GUI teaching the CLI instead of
replacing it.

## Consequences

- Template JS shrinks by the whole builder + fallback (several
  hundred lines); one grammar surface and one semantics surface gone.
- Harness scenarios that drove the widgets are rewritten to drive the
  bar (same operations, real grammar) — coverage preserved, surface
  honest.
- `-light` pages get smaller and their story is finally true.
