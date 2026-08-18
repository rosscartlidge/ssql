# Split Pipelines: Server Head, Browser Tail

Reference: DFC108
Created: 2026-08-18
Last modified: 2026-08-18

[Back to Index](./README.md)

**Status:** Discussion — for comment (Ross + Claude, 2026-08-18)

Builds on: [DFC079](./ssql-serve-proposal.md) (ssql serve),
[DFC107](./dfc107_explore_on_playground_engine.md) (explore on the real
engine).

## The idea

DFC079 imagined a browser UI in front of a native backend: the server
runs the whole pipeline, the browser renders results. DFC107 changed
the balance of power — the browser now carries a **real ssql engine**
(the slim wasm, same semantics, same completion/help stack, exercised
by the explore/playground harnesses). The browser is no longer just a
renderer; it is a legitimate execution backend.

So revisit DFC079's goal with a stronger shape: **one pipeline, split
across tiers.** The head runs where the data lives — a served native
process, which may itself fan out over SSH to other hosts — and the
tail runs in the browser wasm, against the streamed intermediate:

```
 ssh hosts            server (ssql serve)          browser (wasm)
┌──────────┐   ┌──────────────────────────┐   ┌─────────────────────┐
│ raw data │──▶│ from ssh/catalog/files   │──▶│ data.jsonl (vFS)    │
│ (pushed- │   │ where … | group-by …     │   │ sort | limit | to   │
│  down    │   │ (big, data-proximate)    │   │ chart/explore/table │
│  stages) │   └──────────────────────────┘   │ (small, interactive)│
└──────────┘                                  └─────────────────────┘
```

This is not a new mechanism — it is the **third occurrence of a split
we already do twice**:

1. `from ssh host /path -- where …` pushes stages down to the remote
   host (the `--` separator, `+` for multi-stage).
2. `from catalog … -- …` prunes shards and pushes stages to each.

Both splits answer "run this stage where the data is cheaper to
reduce." The server/browser boundary is the same question one tier up:
run the reducing stages on the server, ship the (small) intermediate,
run the interactive stages where re-running them is free.

## Why the browser tail is worth having

The payoff is **interactivity, not offload**. Once the intermediate
lands in the browser's vFS as `data.jsonl`, the explore page's whole
machinery applies to it *as shipped last release*: the pipeline bar
already assumes its input is `data.jsonl`, the builder generates bar
text against it, completion and value sampling run against it locally.
Tweaking the tail — re-sorting, changing a limit, adjusting a chart,
adding a `where` — is a local wasm re-run with zero server round-trips
and zero load on the data hosts. Only edits to the *head* require
going back to the server.

That division matches how exploration actually feels: you settle the
expensive reduction once ("errors by service, last 7 days, across the
fleet") and then iterate rapidly on the view of it.

## Design sketch

### The cut point

Where does the pipeline split? Three positions, probably in this order
of adoption:

1. **Explicit marker (start here).** Reuse the pushdown vocabulary the
   CLI already has — the pipeline bar in a served explore page shows
   the whole pipeline, with a visible split marker. Left of the marker
   executes via the server; right of it executes in wasm. Strawman
   syntax mirroring `--`: the server boundary is where `from serve`
   (or the page's implicit data source) ends and local stages begin —
   in the bar this could be as simple as
   `ssql from serve 'from catalog events -- where -if status ge 400 + group-by service -count n' | ssql sort n -desc | ssql to chart`,
   i.e. the head rides inside the source stage exactly like ssh
   pushdown rides inside `from ssh`.
2. **Optimizer-decided.** The optimizer already reasons about pushdown
   (shard pruning, `-- where` placement). A cost rule — "cut after the
   last cardinality-reducing stage (group-by, distinct, aggregating
   window); never ship more than N rows to the browser" — could place
   the marker automatically, with the bar showing where it landed
   (same spirit as `ssql optimize` showing its rewrite).
3. **Dynamic re-cut.** When the user edits a stage left of the marker,
   re-run the head; right of it, re-run only the tail. The unified bar
   (last release) makes this tractable: the bar is the single source
   of truth, and the marker partitions it.

### Transport and format

DFC079 Phase 2's plumbing is the right substrate: `ssql serve` gains
`-listen-http`, a WebSocket/REST endpoint, `cli.ExecuteWith` with a
buffered writer. The wire format is **JSONL with the `_schema` header**
— the same self-describing contract every ssql pipe boundary uses, and
exactly what the explore page's vFS `data.jsonl` already expects.
Nothing new to invent: the server end is `ssql tee`-shaped, the
browser end is `_fsWriteFile('data.jsonl', …)` — both exist.

Streaming vs snapshot: start with snapshot (head runs to completion,
intermediate lands as one file, tail runs). Streaming re-runs of the
tail over a growing file is a later refinement (and `limit`/sort make
it semantically fiddly).

### Completion and help across the split

The cursor protocol (`-cursor-stage` / `-help-at` / `-complete`) runs
in-browser for tail stages — already works, it's the explore bar. For
head stages the same protocol must run **server-side** (the server
knows its files, its catalog, its ssh hosts) — which is DFC079's
`cli.Complete`-over-WebSocket, unchanged. The bar routes completion
requests by which side of the marker the cursor is on. One protocol,
two executors — same pattern as the CLI vs playground sharing
`HandleCursorProtocol`.

### Delivery vehicle

Two candidates, not exclusive:

- **Served explore**: `ssql serve` hosts an explore-style page whose
  `data.jsonl` is refreshed by head re-runs. Feels like "explore, but
  the file behind it is live and can be huge upstream."
- **Static explore + remote source**: a `to explore` artifact that
  carries a server URL and refetches its head on demand. More fragile
  (CORS, auth, server lifetime) — probably not the first target.

The served page is the natural one: the server embeds the same
HTML/wasm assets it already embeds for `to explore`.

## Semantics discipline (the part we must not fumble)

This proposal creates **cut-point invariance** as a new correctness
property: for any pipeline P and any legal cut, `head-on-server |
tail-in-browser` must equal running P in one place. The browser lane
is the same binary, so per-stage semantics are identical by
construction — the risks are at the seam:

- schema fidelity across the wire (`_schema` must survive; field order
  and int/float normalization — the equivalence harness's exact
  territory),
- ordering guarantees when the head ends unordered but the tail
  assumes order (same rules as parallel-lane shard order),
- value truncation/row caps silently applied at the boundary (must be
  loud, per the no-silent-caps rule).

The gate is a new differential test in the `TestPipelineEquivalence`
mold: run the corpus pipelines uncut and cut at every legal boundary
(server side simulated by the native binary, browser side by the same
binary reading the shipped intermediate), assert byte-identical
normalized output. Sabotage it once to watch it fail before trusting
it. The browser side of the seam is already covered by the explore
harness pattern (headless Chrome driving the real wasm).

## Security posture

Everything in DFC079's list, sharpened by the fact that the browser
now *submits* head pipelines rather than just viewing results:

- Server executes only ssql stages, never shell; `from command` must
  be disabled or allowlisted under serve (`-readonly` should also gate
  `to` writers).
- SSH fan-out from the server uses the existing rules (absolute remote
  paths, `ShellQuote`, no shell interpretation) — the browser never
  composes ssh commands, it composes ssql stages the server validates.
- Bind localhost by default; `-token` for remote; the served page gets
  the token baked in.
- The intermediate shipped to the browser is data leaving the server —
  worth a `-max-ship-rows`/`-max-ship-bytes` guard that fails loudly.

## What already exists vs what's new

| Piece | Status |
| --- | --- |
| Browser engine, vFS, `data.jsonl` contract | ✅ DFC107 + v4.64.0 |
| Pipeline bar as single source of truth, `from data.jsonl` head | ✅ v4.64.0 |
| Cursor protocol shared CLI/browser | ✅ |
| Pushdown split grammar (`--`, `+`) at ssh/catalog tier | ✅ |
| serveState + `cli.ExecuteWith` + `cli.Complete` | ✅ Phase 1 (v4.44.0) |
| HTTP/WS listener on serve | ⏳ DFC079 Phase 2, unbuilt |
| Server-side cursor protocol over WS | new |
| Split marker in the bar + routed execution | new |
| Cut-point equivalence gate | new |
| Ship-size guards, `from command` policy under serve | new |

The striking thing about this table: the hard platform work is done.
What remains is one transport (DFC079 Phase 2, already designed) and
one UI concept (the marker) — plus the testing discipline.

## Open questions (the discussion)

1. **Syntax for the cut.** Head-inside-source (`from serve '…'`,
   mirroring `from ssh … -- …`) vs an explicit pipeline-level marker
   (e.g. a `-- browser` separator)? Head-inside-source composes with
   the existing grammar and completion; a marker reads better in the
   bar. Leaning head-inside-source for mechanics with the bar
   *rendering* it as a visual divider.
2. **Who owns the page** — is this `ssql serve -explore` (server
   serves the workspace) or `to explore -remote URL` (artifact dials
   home)? Leaning serve-owned.
3. **Optimizer involvement now or later?** Explicit-only first release
   seems right; the cost rule needs real usage data.
4. **Session model.** DFC079 Phase 1 loads one dataset at startup;
   split pipelines want per-request heads over the server's whole
   `-dir` (plus catalog/ssh). Does serveState stay (as a cache) or
   does serve become stateless-per-request?
5. **Does the SSH-CLI serve variant benefit?** Pipe support in
   autocli/shell (DFC079's open question, Position 2 of autocli-shell)
   would give the terminal the same head-execution the browser gets —
   one dispatch path for both protocols.
6. **WebVM's role.** The WebVM terminal could be a third client of the
   same serve protocol (curl against REST) — worth keeping in mind,
   not worth designing for yet.
