# The Codelab as a Guided Path: Confidence Before Sophistication

Reference: DFC125
Created: 2026-09-04
Last modified: 2026-09-05

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

## 4. Follow-through: the other codelabs (2026-09-05)

Ross, reading the rewritten CLI codelab: check the others, the CLI must
come before the Go package, give a recommended order — and "the most
important thing is to make sure they're correct and to keep it that way".

**Applying §3 to the rest of the path.** The L1 doc-check compiled Go
only from the two AI docs and README.md; no Go codelab was gated. Run
for real: the Getting Started Quick Demo did not compile (unused
import); the typed codelab was clean; the advanced tutorial had 11 of
13 programs on pre-v4 idioms; the signal-processing guide passed 20 of
46 blocks. Every one of those had been "validated" by a check that
never executed it.

**Two runners, one convention.** `scripts/codelab-run.sh [-v] [DOC]` is
now parametrised (default `doc/cli-codelab.md`) and gates the signal
guide too. `scripts/codelab-go-run.sh DOC` is its Go twin: complete
programs go into their own package in a throwaway module whose go.mod
`replace`s ssql/v4 with the checkout and are `go run`; fragments must
parse (gofmt -e, as declarations or as statements, imports split off);
bash blocks run in the same directory with the same `# codelab: skip —
reason` rule. `TestCodelabRuns` runs all four docs as parallel
subtests; `make doc-test` mirrors the list.

**What the gates taught, in order of surprise.**

1. *A block's exit status was its LAST command's.* The first sabotage
   of the signal guide (`-kernel moving-average`) passed because the
   block ended in `echo "Compare …"`. Both runners now run blocks under
   `set -e -o pipefail`. That immediately exposed a masked failure the
   old runner had passed.
2. *Exit 141 is not a failure.* A downstream `limit` closes the pipe;
   the upstream stage dies of SIGPIPE once the data outgrows the pipe
   buffer. Small fixtures never showed it; the signal guide's 2k-row
   spectrograms did. Both runners map 141 → ok.
3. *A sink that does not fail loudly hides everything upstream of it.*
   Every convolution chart in the guide asked for `-y convolved` where
   convolve emits `FIELD_convolved`; `to chart` drew an empty chart and
   exited 0, for months. `to chart` now validates its axis fields
   against the schema like every other command (`TestToChartUnknownFieldLoud`).
   Corollary for the runner: "non-empty stdout" is a weak oracle when
   the command's own success message is the output.
4. *First-row CSV typing silently corrupts.* A bc-generated `0,0` first
   row typed both columns int; every later `.0628` became `0`. Recorded
   and promoted in TODO (DFC124 (c)); the guide now generates with
   python `%.6f`, which is a workaround, not the fix.

**Content decisions.** The advanced tutorial is archived
(`doc/archive/advanced-tutorial.md`) rather than ported: it was 2370
lines that mostly duplicated the API reference. Its three teachable
parts — group-by feeding a join, count/sliding/time windows, and
stopping an infinite stream — were rewritten against v4, verified by the
runner, and folded into the Getting Started guide as three sections. The
learning path (README.md, doc/README.md, each codelab's header) is: CLI
codelab → Signal Processing (optional) → SSH console (optional) →
Getting Started (Go Record) → Typed codelab → references.

**Rule going forward.** A codelab is added to `TestCodelabRuns` the day
it is written. A doc that is not run is not documentation; it is a
guess about what the software did once.
