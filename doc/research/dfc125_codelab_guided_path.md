# The Codelab as a Guided Path: Confidence Before Sophistication

Reference: DFC125
Created: 2026-09-04
Last modified: 2026-09-04

[Back to Index](./README.md)

**Status:** SHIPPED 2026-09-04 (Ross's brief; arc agreed). Records the
pedagogy so it cannot drift back, and gates the codelab so it cannot
silently break again. Result: 455 lines (was 2200), 45 blocks — 39 run,
6 skip with stated reasons, 0 fail; sabotage-verified. Found on the way:
`cast` was broken in plain exec (schema-unaware reader; fixed + gated)
and `Limit`'s godoc had been displaced.

## 1. The finding

Nobody had ever *done* `doc/cli-codelab.md`. It could not be done:

| | |
|---|---|
| bash blocks | 113 |
| data files the examples assume | ~30, with no setup section saying where any come from |
| validation touching the codelab | none (L1 doc-check compiles Go snippets only) |
| `employees.csv` schemas assumed | several — `dept` in one section, `department` in another |

The examples were written one at a time over months and never run
together. "Does it work" was *no*; "is it useful" could not be judged.

Ross's editorial brief on top of that: code generation arrives too
early and through the low-level `-generate` flag instead of `ssql
generate go -pipeline '…'`; multi-file pushdown confuses too early;
the tmux experience (popup completion and help) is the best way in and
is buried at the end; overall the document must build confidence doing
useful things easily before the sophisticated and optimising features.

## 2. Decisions

1. **One fixture world, checked in.** `doc/codelab-data/` holds small
   files with ONE coherent schema each (`employees.csv` reuses the
   corpus fixture's shape; `customers.csv`/`orders.csv` join;
   `sales_wide.csv`/`sheet.csv`/`app.log`/`sensor.csv`/`signal.csv`
   carry the unpivot, fill, extract, resample, and DSP sections). Every
   example runs against them, from the setup section's `cd`.
2. **The codelab is gated.** `scripts/codelab-run.sh` extracts every
   ```` ```bash ```` block, runs it in the fixture directory with
   `set -o pipefail` against a freshly built binary, and fails on a
   non-zero exit or empty stdout. `TestCodelabRuns` wraps it into the
   normal `go test` battery, and `make doc-test` calls it. A block that
   genuinely cannot run (a remote host, a URL, an interactive key) is
   SKIPPED ONLY BY AN EXPLICIT MARKER — `# codelab: skip — <reason>` as
   its first line — so skipping is a decision the reader can see, not
   a default. Sabotage-verified on landing (rename a fixture column →
   the runner must fail).
3. **The arc — confidence first, sophistication last:**
   - *Part 1, ten minutes to useful:* Setup (install, fixtures, and
     `eval "$(ssql -shell-init)"` inside tmux as the SECOND thing the
     reader does — Tab/Ctrl-O/Alt-h popups are how ssql is discovered);
     Look at a file (`from | to table`, `describe`, `limit`, `where`,
     `sort`, `include`); Answer questions (`group-by`, `top`,
     `distinct`, `count`, `join`, `pivot`/`unpivot`, `fill`,
     `extract` + `from lines`); Save and share (`to`, `tee`, `to
     chart`, the workspace).
   - *Part 2, going further — each section opens with WHY:* Time
     series & signals (`resample`, `bucket()`, windows, DSP pointer);
     Make it fast (`-records`, `-sample`, `from -last`, parquet
     columns, ⚡ typed heads); Generate code — entered ONLY through
     `ssql generate go -run -pipeline '…'`, then `generate sql -run`,
     the optimiser, Alt-g/Alt-r (the `-generate` flag and `SSQL_MODE`
     export dance become an appendix, "how the fragments flow");
     Distributed LAST (`from ssh`, catalog, multi-file pushdown framed
     as "the optimiser reaching over SSH" — an easy idea only after
     the reader has watched the optimiser rewrite a local pipeline);
     Reference.
4. **Two rules for every future edit:** every block runs under the
   runner or carries a visible skip reason; and no section introduces
   a mechanism before the reader has felt the problem it solves.
5. **The human read is part of the deliverable.** After the mechanical
   gate passes, the codelab is walked start to finish as a newcomer and
   the findings reported — ordering, stale claims, whether each section
   teaches or merely lists.

## 3. Why the runner matters more than the rewrite

The rewrite fixes today's document; the runner fixes the process that
let it rot. Every command added this week changed the codelab by hand
and none of those edits were ever executed. The rule that made the
equivalence gate and the scale gate valuable applies verbatim: *a doc
example that is never run is a fixture-invisible bug waiting for a
reader to find it.*

## Prior art / related
- [DFC122](./dfc122_capability_gap_survey.md) — the verbs Part 1 now
  teaches together.
- [DFC120](./dfc120_system_design_lessons.md) §"if it's not tested" —
  the principle this applies to documentation.
- `doc/VALIDATION.md` — the L1/L2/L3 tiers; the codelab runner is the
  L2 the codelab never had.
