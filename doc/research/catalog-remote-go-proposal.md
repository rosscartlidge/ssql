# Catalog Remote-Go Proposal

Reference: DFC094
Created: 2026-05-06
Last modified: 2026-05-06

[Back to Index](./README.md)

**Status:** Proposal (rev 2, 2026-05-06)
**Date:** 2026-05-06 (orig); 2026-05-06 (rev 2: drop concurrency cap, completion-order default, fail-fast default, auto-emit require header)
**ssql version target:** v4.43
**Prerequisites:** Phase A planner (v4.39), Phase B mixed mode (v4.40), `-script PATH` flag (v4.41), codegen-symmetric ssh pushdown (v4.42)
**Related:** `distributed-shard-catalog.md` (shipped v4.27.0), `remote-go-execution-proposal.md` (shipped v4.42)

## TL;DR

Extend v4.42's codegen-symmetric ssh pushdown to `from catalog`.
Each shard host runs the same mode the local pipeline runs in:

```
ssql from catalog SHARDS.csv -- STAGES                                      → CLI chain on each shard (v4.27 baseline)
(SSQLGO=record; ssql from catalog SHARDS.csv -- STAGES) | generate go -run  → record-mode Go on each shard
(SSQLGO=typed;  ssql from catalog SHARDS.csv -- STAGES) | generate go -run  → typed-parallel Go on each shard
```

The local generated Go embeds the per-shard `.ssql` template as a
`const`, iterates the catalog at runtime to substitute the shard
path, and ships+runs Go on each shard. Optionally in parallel
(new — today's `ProcessCatalogShards` is serial).

The result: one self-contained Go binary that orchestrates
distributed typed-parallel execution across N shard hosts with
nothing but `ssh` and a deployed `ssql` on each shard. Stock
infrastructure, no daemons, no service mesh.

Estimated effort: **~2 days** (~1 day for the codegen change,
~1 day for the parallel orchestrator + tests). Catalog-pruning,
partition-filter, and shard-field-enrichment plumbing all
inherit from v4.27 — no rework needed.

## Motivation

Today's `from catalog` already does ssh push-down to each shard
(`distributed-shard-catalog.md`). The remote runs `ssql ... |
ssql ...` (record-mode CLI chain). For the canonical user
workload (14.6M rows × N shards), this is bottle-necked by:

- **Per-row JSONL serialisation on each shard.** Same problem
  v4.42 fixed for the single-host case.
- **No typed-parallel runtime on the remote.** The 215× speedup
  the v4.40 planner delivers locally doesn't reach the catalog
  hosts.
- **Sequential per-shard execution.** `ProcessCatalogShards`
  loops shards one at a time. On a 4-shard catalog with
  comparable per-shard cost, you wait 4× longer than a
  parallel implementation would.

With the catalog extension to remote-Go, all three improve:
- Each shard runs typed-parallel Go (or record-mode Go,
  depending on local SSQLGO)
- Shards execute in parallel (configurable cap)
- Result aggregation is the same K-way merge the existing
  `merge -catalog` plumbing uses

For a 4-shard, 14.6M-rows-each workload that takes 60s today
under `--` pushdown, expected wall under codegen-symmetric
catalog with parallelism:
- Per-shard typed-parallel speedup: ~10-100× (depends on
  pipeline shape — same as v4.40 numbers)
- Parallel-shard-fanout: up to 4× (limited by slowest shard +
  network overhead)
- Combined: routinely **40-100× faster** for filter+aggregate
  workloads, comparable to a real distributed query engine
  but with no infrastructure beyond ssh

## Mechanism

The local generator emits Go that:

1. Reads the catalog at runtime (`ssql.ReadCatalog`)
2. Prunes entries via embedded `CatalogFilter` slice
   (already happens today — v4.27 mechanism unchanged)
3. For each remaining entry, builds a per-shard `.ssql` script
   from the embedded template (substituting `entry.Path`)
4. Ships+runs each script on its respective host, **all in
   parallel by default** (no concurrency cap — each shard runs
   on a different host, and the local orchestrator just spawns
   `ssh` subprocesses with negligible CPU cost). Opt-in
   `-shard-concurrency N` for pathological catalogs (hundreds
   of shards) or fleets where many shards colocate on one host
   and `MaxSessions` matters.
5. Reads each shard's JSONL output, optionally enriches with
   `shardField`, merges to local stdout in **completion order**
   by default (whichever shard finishes first writes first).
   Opt-in `-shard-order catalog` for deterministic output with
   per-shard buffering (peak memory ~total output).

### Per-shard .ssql template

The script template is a Go raw-string literal with `{{.Path}}`
or similar substitution placeholder. At runtime, the orchestrator
iterates `entries` and renders a script per shard:

```go
const remoteSSQLTemplate = `ssql from %s
| ssql where -if status ge 500
| ssql group-by service -count n`

const remoteSSQLMode = "typed"

// per shard:
script := fmt.Sprintf(remoteSSQLTemplate, ssql.ShellQuote(entry.Path))
runRemote(entry.Host, script, remoteSSQLMode, w)
```

The template captures the local-side intent (the user's `--`
pushdown stages). The path is the only per-shard variable.

### Parallel orchestrator (completion-order default)

```go
func runCatalogShards(entries []ssql.CatalogEntry, mode string, concurrency int, w io.Writer) error {
    var sem chan struct{}
    if concurrency > 0 {
        sem = make(chan struct{}, concurrency)
    }
    var wg sync.WaitGroup
    var mu sync.Mutex // serialises writes to w (and the schema-header-once latch)
    var schemaWritten bool
    var firstErr error
    var firstErrOnce sync.Once

    for _, entry := range entries {
        wg.Add(1)
        if sem != nil { sem <- struct{}{} }
        go func(e ssql.CatalogEntry) {
            defer wg.Done()
            if sem != nil { defer func() { <-sem }() }

            script := fmt.Sprintf(remoteSSQLTemplate, ssql.ShellQuote(e.Path))
            stdout, errCh := runRemoteStreaming(e.Host, script, mode)

            // Stream this shard's output line-by-line under the
            // mutex so multiple shards' lines don't interleave.
            scanner := bufio.NewScanner(stdout)
            for scanner.Scan() {
                line := scanner.Bytes()
                if isSchemaHeader(line) {
                    // Emit the schema header exactly once (from
                    // whichever shard arrives first); other
                    // shards' headers are dropped.
                    mu.Lock()
                    if !schemaWritten { w.Write(line); w.Write([]byte{'\n'}); schemaWritten = true }
                    mu.Unlock()
                    continue
                }
                mu.Lock()
                w.Write(enrichWithShardField(line, e, shardField))
                w.Write([]byte{'\n'})
                mu.Unlock()
            }
            if err := <-errCh; err != nil {
                firstErrOnce.Do(func() { firstErr = fmt.Errorf("shard %s:%s: %w", e.Host, e.Path, err) })
                if failFast { /* signal cancellation to other shards */ }
            }
        }(entry)
    }
    wg.Wait()
    return firstErr
}
```

Default behaviour:

- **No concurrency cap** (`concurrency == 0` → `sem == nil`,
  uncapped fanout). Each shard runs on its own host; the local
  orchestrator is essentially I/O multiplexing.
- **Completion order** — shards write under the mutex as data
  arrives. First shard to produce data emits the schema header;
  subsequent shards' schema headers are dropped (they should
  match anyway).
- **Fail-fast on first error** — `firstErrOnce.Do` captures the
  earliest shard failure; remaining shards are signalled to
  cancel via the `failFast` path. `-keep-going` opt-out skips
  the cancellation, lets remaining shards finish, returns the
  collected errors.

Opt-in `-shard-order catalog` switches to per-shard buffering:
each shard accumulates into its own buffer, the orchestrator
flushes them in catalog order once all shards complete. Trade:
peak memory ~total output, deterministic output for callers
that need it.

### Auto-emitted `# require: vX.Y.Z` header

Catalog has the highest version-skew risk: multiple hosts, often
heterogeneous deployment. A v4.40 shard processing a v4.42-feature
script produces hard-to-diagnose cascade errors:

```
shard node3: bash: line 3: count: command not found
shard node3: pipeline failed: exit status 127
Error: shard node3: exit status 1
```

The `require:` directive at the top of the .ssql script gives the
remote ssql a chance to pre-flight check before running anything.
Mismatch → single clear error:

```
shard node3: ssql v4.40.0 cannot run this script (requires v4.42.1+) — upgrade ssql on node3
```

**Two ways the directive arrives:**

1. **Auto-emitted by the local generator** at codegen time.
   Every `.ssql` script shipped via `ssql generate go` gets a
   `# require: $localVersion` header automatically. Zero user
   effort; biggest mismatch class caught (the catalog case
   where the user can't easily know each shard's version).
2. **User-authored** in hand-written `.ssql` files. Pin a
   specific minimum for scripts that genuinely need a
   particular feature.

**Implementation:**

- `ssql generate go -script` preprocessor parses leading
  `# require: vX.Y.Z` lines (multiple allowed; takes the
  highest minimum). Compares against the running binary's
  `version.Version`. Mismatch → error before the pipeline runs.
- Local-side codegen path emits `# require: $localVersion` as
  the first line of any .ssql script written into the
  generated Go const.

Format chosen to match the `require` keyword from `go.mod` and
`Cargo.toml` — recognisable pattern, one-word directive, the
version is the *minimum*.

### Result format compatibility

Per shard, the remote emits the same wire format the v4.42
single-host path produces (`{"_schema":...}` + JSONL). The
orchestrator emits ONE combined `_schema` header (read from the
first shard's stream), then concatenates record bodies from all
shards. Downstream local stages see clean schema-aware JSONL
identical to v4.27's CLI baseline.

Schema mismatch across shards → fail loudly with "shard X
returned schema {...}, expected {...}". Defensive but matches
v4.27 behaviour.

## Implementation sketch

### Phase A — codegen-symmetric for catalog (~1 day)

1. Update `generateFromCatalogCode` to emit ship-and-run Go
   instead of the existing `ProcessCatalogShards` invocation
   (which uses CLI chain remote)
2. Embed per-shard `.ssql` template as `const`
3. Bake mode (record/typed) from local SSQLGO at codegen time
4. Per-shard orchestrator inlined into the generated Go (no
   import on internal helpers — same pattern as v4.42)

### Phase B — parallel-per-shard orchestration (~1 day)

5. Add `-shard-concurrency N` flag on `from catalog`
   (default min(GOMAXPROCS, 8); 1 → sequential, like today)
6. Update the orchestrator to use semaphore + waitgroup
7. Stable result ordering via per-shard buffer (write-shard-N
   only after shard-1..N-1 complete) — like v4.40's parallel
   write-everything sinks. Trade: peak memory ~num_shards × per-shard-result-size.
   Worth it for the parallelism win.
8. Tests: 3-shard LXD setup, sequential vs parallel timing,
   identical output

### Phase C — interactive baseline gets parallelism too (~half day)

9. Update `ssql.ProcessCatalogShards` (the v4.27 helper used
   by interactive `from catalog`) to also run shards in
   parallel under the same concurrency cap. No codegen
   involved — just upgrades today's CLI baseline.
10. The catalog extension is then symmetric across all three
    modes (CLI / record-Go / typed-Go) AND all three get
    parallel-per-shard execution.

### Out of scope for v4.43 first ship

- **GPU per-shard** — `from catalog -gpu` already wires
  `ssql_gpu` for the CLI baseline. Adding it to the codegen
  path is a small follow-up; defer to keep the v4.43 patch
  focused.
- **Catalog-aware K-way merge** — the existing `merge -catalog`
  already does ordered K-way merge. Composing it with the new
  codegen path is straightforward but requires schema-ordered
  output which the unordered fan-in doesn't naturally produce.
  v4.43.x patch.
- **Cross-shard joins** — `from ssh A | join from ssh B`
  shipped both sides remotely. Real cross-shard joins (`from
  catalog SHARDS.csv | self-join …`) need cross-node
  coordination. Defer indefinitely; users can today run two
  catalog reads and join locally.

## Composition with existing features

### Partition pruning (v4.27)

`from catalog SHARDS.csv -if date ge 2025-03-01` already filters
shards before ssh. The codegen path already embeds the filter
via `CatalogFilter` slice — unchanged. Pruned shards never get
ship-and-run.

### Shard-field enrichment (v4.27)

`-shard-field source` adds `host:path` to each record. The
codegen orchestrator does this in the per-shard loop after
reading JSONL (same place today's `enrichCatalogRecord` runs).

### Optimiser (`-optimise`)

`(SSQLGO=typed; ssql from catalog … -- where … + group-by …) |
ssql generate go -optimise -run` — the optimiser sees the
local pipeline (with `from catalog -- pushdown` form) and
applies the existing rewrites (predicate-push-into-pushdown,
sort+limit→top, Parquet column projection on the remote,
etc.). The OUTPUT of the optimiser is a different `ssql ...
generate go` invocation that we then route through the v4.43
codegen path. Same pre-existing optimiser interface.

### `merge -catalog`

For ordered-output workloads, users today combine
`merge -catalog SHARDS.csv -by ts -- pushdown`. That command's
codegen path can adopt the same ship-and-run mechanism in a
follow-up patch. The fix is mostly mechanical: same
template-and-orchestrate plumbing, different per-shard wire
shape (sorted runs instead of unordered).

## Failure modes

### A shard host has no ssql installed

Error from remote: `bash: line 1: ssql: command not found`.
Per-shard error captured, propagated as `shard $HOST:$PATH:
remote ssql missing — install ssql to enable codegen-symmetric
catalog`. Other shards continue (the orchestrator only fails
on first error after all in-flight shards complete; this could
be configurable via `-fail-fast`).

### Shard hosts running mismatched ssql versions

Generated Go calls `ssql generate go -script -mode $mode -run`
on each shard. If the shard has an old ssql that doesn't
support `-script` (pre-v4.41), the remote errors. Surface:
`shard X: ssql version too old (need v4.41+) — upgrade or
fall back to non-codegen catalog reads`.

The optional `# require: vX.Y.Z` directive at the top of the
.ssql template (proposed in remote-go rev 4) gives a cleaner
pre-flight check. Could land here.

### Network partition mid-shard

The shard's `runRemote` returns an SSH error. Today's
behaviour (`continue` past the failed shard, log to stderr) is
arguably wrong — it produces silently-incomplete results.
The codegen path inherits this. For v4.43 we should add
`-fail-fast` (default true: any shard failure aborts the whole
run) with an explicit `-keep-going` opt-out.

### Disk pressure on shard /tmp

Each shard temporarily creates a `/tmp/ssql-remote-X.ssql`
(auto-cleaned via trap). Pathological pipelines could build
a large intermediate /tmp on the shard during the actual run.
Same as v4.42 single-host case. Document `-shard-workdir DIR`
override.

## Effort breakdown

- Phase A (catalog codegen): **~1 day**
  - Update `generateFromCatalogCode` (~80 LOC)
  - Inline orchestrator template (~50 LOC of generated Go)
  - Tests against 3-shard LXD container set (~30 LOC)
- Phase B (parallel-per-shard): **~1 day**
  - Concurrency cap, semaphore, ordered-output buffer
  - Stable-output tests vs. sequential equivalent
- Phase C (CLI baseline parallelism): **~half day**, optional

Total scope for v4.43: **~2-3 days**. Phase C is a quality-of-
life upgrade for users who don't use codegen — could ship
together or later.

## Decisions (rev 2)

The four open questions from rev 1 have been resolved:

1. **Concurrency: uncapped by default.** Each shard runs on a
   different host; the local orchestrator just spawns ssh
   subprocesses with negligible CPU cost. Fork-bombing concerns
   start at hundreds of shards, not eight. Opt-in
   `-shard-concurrency N` exists for users with pathological
   catalogs (1000+ shards) or fleets where many shards
   colocate on one host.
2. **Output ordering: completion order by default.** Cheaper
   (no per-shard buffering, peak memory ~one record). Opt-in
   `-shard-order catalog` for users who need deterministic
   output — buffers each shard, flushes in catalog order at
   the end. Trade: peak memory ~total output.
3. **Failure: fail-fast by default.** First shard error
   cancels remaining shards and returns the error. Opt-out
   `-keep-going` collects errors, lets remaining shards
   finish, returns the aggregate. Matches what users expect
   from a coherent distributed query.
4. **`# require: vX.Y.Z` directive: auto-emitted on every
   generated .ssql script** with the local ssql version.
   Pre-flight check on the remote catches version-skew before
   the pipeline runs, giving a single clear error instead of
   a cascade of symptoms. Also accepted in user-authored
   scripts. Cost: ~30 minutes of preprocessor work.

## Why this is the right scope for v4.43

- **Composes naturally with v4.42** — same ship-and-run
  mechanism, same wire-format plumbing, same `_schema`
  header. Just extends across N shards.
- **Unblocks the "distributed Go pipelines with stock SSH"
  thesis.** Single self-contained Go binary that orchestrates
  N-shard typed-parallel execution. No infrastructure beyond
  ssh and a deployed ssql on each shard.
- **Respects the existing v4.27 catalog UX.** No new flags
  for users who don't want them; partition pruning,
  shard-field, gpu, etc. all keep working unchanged.
- **Effort scoped to ~2 days.** Phase C is optional; could
  defer if focus is needed elsewhere.

## See also

- `distributed-shard-catalog.md` — original catalog proposal (shipped v4.27.0)
- `remote-go-execution-proposal.md` — Phase B codegen-symmetric (shipped v4.42)
- `codegen-wrapper-proposal.md` — `-script PATH` (shipped v4.41)
- `typed-auto-parallel-proposal.md` — Phase A planner (shipped v4.39)
- `ssh-test-environment.md` — LXD containers for SSH testing
