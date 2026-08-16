# Explore on the Playground Engine (retire the TinyGo mini-engine)

Reference: DFC107
Created: 2026-08-16
Last modified: 2026-08-16

[Back to Index](./README.md)

**Status:** Approved (Ross, 2026-08-16); implementing

## Problem

`ssql to explore -wasm` embeds a **TinyGo mini-engine** (`cmd/ssql-wasm`,
284KB, 3,771 lines: `expr.go`, `operators.go`, `compare.go`, `dataset.go`)
— a hand-written THIRD implementation of ssql filter/expression semantics,
outside the equivalence harness. That is exactly the "one semantics, many
backends" hazard class (the `top` saga): it will drift from exec, silently.

Meanwhile the playground runs the REAL binary as WASM (slim build, 15MB):
full semantics, 49 harness scenarios, and this week's interactive stack
(as-you-type completion, help-at-cursor, value sampling, files bar).

## Decision

Remove the mini-engine. `to explore -wasm` uses the slim playground WASM —
the same artifact, the same `ssqlExec` bridge. One engine everywhere.

## Design

1. **Embed, gzipped.** `cmd/ssql/wasm/` carries
   `ssql-playground.wasm.gz` (~5MB; Go WASM gzips ~3×) plus the standard
   `wasm_exec.js` and `fs-polyfill.js`, committed and refreshed at release
   time (like the debs). The explore HTML inlines the gz as base64 and
   decompresses in-browser via `DecompressionStream('gzip')`.
2. **Build-tag gating.** The embed lives behind `//go:build !slim`; slim
   builds (playground itself, WebVM 386) get a stub and `to explore -wasm`
   errors loudly ("requires the full build"). This also breaks the
   wasm-embedding-itself recursion by construction.
3. **The JS port is a shim, not a rewrite.** The template's mini-engine
   surface is only `ssqlWasm.initFromBytes(buf)` +
   `ssqlWasm.pipeline(DATA, ops)` (ops = `{op:'where', field, operator,
   value}` lists, two call sites). Replacement: loader = fs-polyfill +
   wasm_exec + gunzip + instantiate; `ssqlPipeline(data, ops)` serializes
   data to JSONL, builds `['where', '-if', f, op, v, ...]` argv, calls
   `ssqlExec(args, jsonl)`, parses JSONL out. No virtual-FS dance — data
   rides stdin per call.
4. **Makefile:** `make explore-wasm` = slim js/wasm build + `gzip -9` into
   `cmd/ssql/wasm/`. Release checklist gains "refresh explore wasm".
   The TinyGo `make wasm` target is deleted; TinyGo leaves the toolchain.
5. **Deprecation (Phase 2):** delete `cmd/ssql-wasm/` entirely, its
   Makefile target, and rewrite the WASM docs (CLAUDE.md WASM section,
   `claude/wasm.md`, `claude/playground.md`).

## Costs, accepted

- Explore HTML with `-wasm`: ~1MB → ~6MB (local artifact; fine).
- Full-build binary: +~5MB (embed); slim unaffected (stubbed).
- Repo carries a ~5MB blob refreshed per release (precedent: debs).

## Testing

- Headless-Chrome e2e for the generated explore HTML (the playground
  harness pattern): filter interaction → `ssqlExec` → table updates.
- The mini-engine's untested-drift problem disappears with the engine.

## Future (DONE same day)

The pipeline bar shipped hours later: the interactive layer was extracted
into a shared `ssql-ui.js` (playground includes it; explore embeds it),
and explore gained the bar — completion/help against the page's own data
(`data.jsonl` in the virtual FS), Run→grid, Reset. One interactive stack.
