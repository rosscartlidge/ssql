# Record→Typed Re-entry: Typed Pipelines over SSH Sources

Reference: DFC109
Created: 2026-08-18
Last modified: 2026-08-18

[Back to Index](./README.md)

**Status:** Approved (Ross, 2026-08-18); implementing.
Revises the Phase C/D split of
[mixed-mode-pipelines-proposal.md](./mixed-mode-pipelines-proposal.md)
(that doc's Phase C `--into MyStruct` explicit-hint design is
superseded by the sampling design here; its Phase A/B content stands).

## Problem

`from ssh` (and `from catalog`) are Record-only sources. Under
`SSQL_MODE=typed` a pipeline like

```bash
ssql from ssh db1 /data/events.csv -- where -if status ge 400 \
  | ssql group-by service -count n | ssql sort n -desc | ssql to table
```

fails at assembly ("Record-mode sources are Phase C") even though the
remote side *already runs typed-parallel Go* (the v4.42 pushdown
codegen propagates the mode). The local side — often the expensive
aggregation over what the remote ships back — is locked out of the
typed/parallel runtime precisely where users fetch big data over ssh
and want high-speed local processing.

The blocker was always the struct: Records carry only runtime schema,
typed code needs a static type at generate time.

## Decisions (Ross, 2026-08-18)

1. **The struct comes from sampling at generate time.** No
   `--into MyStruct` hint flag. `generate go` contacts the source the
   same way execution would and derives the schema.
2. **Typed mode maintains typed across the boundary automatically.**
   If `SSQL_MODE=typed`, a Record-only source converts to typed at the
   source; the user changes nothing.
3. **Honest costs, loud failures.** If the schema can't be sampled
   (host unreachable at generate time, no `_schema`, untypeable
   fields), fail with a clear error naming the fix (`SSQL_MODE=record`)
   — never silently emit a slower or wrong program.

## Design

### Sampling is exact, not inferential

We do not sample *values* (the CSV path's int64→float64→…→string
inference). SSH sources speak schema-aware JSONL: the remote output's
first line is a `_schema` header with field order **and types**
(`{"_schema":{"fields":[…],"types":{age:"int",…}}}`), and exec-mode
headers stay type-exact through every stage — including pushed-down
group-bys, and even when the sampled prefix yields zero output rows.

**Implementation note (changed from the first draft):** the original
plan was to sample via `SSQL_MODE=schema` (the schema-shadow mode —
no data read at all). Measurement killed it: schema mode is
name-exact but *type-degraded* — it reports every field as `"any"`,
even for a bare `from csv` (the shadow ops don't yet propagate
types). Until schema-shadow carries types, sampling runs the real
pipeline in **exec mode with `limit 200` injected right after the
source**: cheap (200 rows through the stages, so even a pushdown
ending in an aggregation costs almost nothing), and the header types
are authoritative because they come from the same code that will
produce the real run's wire format. When the schema-shadow TODO
(types through schemaOps) lands, sampling can switch back to the
zero-data form.

```
ssh HOST "export SSQL_MODE= SSQLGO=; /usr/bin/ssql from '/data/events.csv' | /usr/bin/ssql limit 200 [| pushdown stages]"
  → {"_schema":{"fields":["service","n"],"types":{"service":"string","n":"int"}}}
```

(The `export SSQL_MODE= SSQLGO=;` prefix is load-bearing: a remote
shell rc exporting a mode var would flip the whole sampling pipeline
into codegen. Constant string, so no quoting concern.)

`lib.TypedSchemaFromHeader(header, typeName)` maps header types to Go
types (`int`→`int64`, `float`→`float64`, `bool`→`bool`,
`string`→`string`; anything else — notably `"any"` — is a loud
error) and renders the struct via the existing `RenderStructDef`.
Remote invocation follows the existing ssh security rules: absolute
remote binary path, `BuildRemoteCommand`/`ShellQuote` for the path,
`exec.Command("ssh", …)`.

The converter closure uses plain `ssql.GetOr`, whose numeric coercion
(int64↔float64) absorbs the JSONL wire's int/float ambiguity — a
whole float round-trips as an integer literal and would zero out
under a strict type assertion.

### The boundary primitives (ssql package first, per doctrine)

Two new functions in the `typed` package; generated code calls them
with an explicit, readable per-field converter closure (no
reflection):

```go
conv := func(r ssql.Record) EventRow {
    return EventRow{
        Service: ssql.GetOr(r, "service", ""),
        N:       ssql.GetOr(r, "n", int64(0)),
    }
}
// serial: lazy per-row conversion, zero materialization
records := typed.FromRecords(recordsRaw, conv)          // iter.Seq[T]
// parallel: materialize []T once, then shard — the house pattern
records := typed.FromRecordsParallel(recordsRaw, conv, runtime.GOMAXPROCS(0)) // Stream[T]
```

`FromRecordsParallel` is materialize-then-`ParallelFromSlice` — the
concurrency playbook's preferred entry into parallel execution (no
per-row channels). This deliberately collapses the old Phase C
(serial typed) and the "deferred indefinitely" Phase D (parallel):
for ssh sources the stream is post-pushdown and post-reduction, so
materializing it is the normal case, and the parallelism is where the
user's "high speed" actually lives.

### Planner integration

`from ssh` in typed mode emits the standard dual-template init
fragment (the `from_csv.go` pattern): `Code` =
ssh-exec + `FromRecordsParallel` with `Produces: ShapeStream`;
`AltCodeIfSeq` = ssh-exec + `FromRecords` with `ShapeSeqTyped`;
`OutputTypedSchema` + `StructDefs` from the sampled header. The
existing planner then does everything else unchanged — parallelism
reach decides Stream vs Seq (serial pipelines get the lazy,
zero-materialization converter), and the Phase B typed→Record
boundary still handles downstream Record-only stages.

Cost honesty per decision 3: the parallel form's materialization is
bounded by what the remote ships — which the pushdown design already
encourages users to minimize. The conversion itself is ~100 ns/row.
No silent fallbacks: sampling failure is a generate-time error, not a
quiet Record-mode downgrade (rationale: a user who set
`SSQL_MODE=typed` asked for typed; shipping them Record silently is
the "correct in mode X, silently wrong in mode Y" family of surprise).

### Scope

**v1 (this DFC):** `from ssh` — both the plain form and the pushdown
form (`-- stages`, `+` multi-stage). Both already run the remote side
correctly; only the local landing changes.

**Explicitly future:**
- `from catalog` re-entry (multi-shard: same header sampling per the
  catalog's own schema story; needs a merge-schema decision).
- Mid-pipeline re-entry (`… | pivot | typed group-by`): needs the
  Record stage's *output* schema at generate time — that's the
  schema-shadow machinery applied to interior fragments; wait for the
  schema-shadow corpus test (TODO leftover) to land first.
- Sampling cache / `-schema-from FILE` offline override, if
  generate-time ssh dialing proves annoying in practice.

## Benchmark (3M rows × 6 cols over stubbed ssh, group-by 2 keys × 3 aggs)

| Lane | Wall (best of 3) | Peak RSS |
| --- | --- | --- |
| CLI exec (interpreted, process-parallel) | 5.6s | — |
| generated record — legacy JSON reader | 22.6s | 4.9 GB |
| generated typed — legacy JSON reader | 20.4s | 0.9 GB |
| generated record — cached JSONL reader | 5.6s | 1.2 GB |
| **generated typed — cached JSONL reader** | **4.7s** | **0.8 GB** |
| remote side alone (ssh + CSV→JSONL) | 3.9s | — |

Lessons: (1) the first benchmark caught a real bug — generated code was
4× *slower* than the interpreted CLI because the plain-form landing
used `ReadJSONFromReader` (per-line `json.Unmarshal`, schema per
record; the #1 perf rule violated in shipped codegen since v4.27-era);
switching to the schema-cached `ReadJSONLFromReader` fixed both lanes.
(2) With the parse fixed, the wire dominates: typed's downstream is
~1s over the 3.9s remote floor vs record's ~1.8s, plus 30% less
memory — and the gap widens with heavier downstream work. For big
reductions, push down (`-- …`): the remote then runs typed-parallel
Go and the local landing is trivial. (3) Parallel float aggregation
(`-avg`) differs from serial at the ~13th significant digit
(shard-order summation) — counts and integer sums are exact.

## Yak shaved on the way

`typed.ParallelFromSlice` had a latent out-of-bounds panic when
round-up chunking pushed a later shard's start past the end of the
slice (50 rows / 24 shards → shard 17 starts at 51). Never hit before
because existing callers pass large slices; `FromRecordsParallel`
over a small ssh result found it immediately. Fixed with a `lo` clamp
and pinned by `TestParallelFromSliceManyShards`.

## Testing

- **Unit:** `TypedSchemaFromHeader` (type mapping, ordering, unknown
  types → loud error); `FromRecords`/`FromRecordsParallel`
  round-trips; missing-field defaults.
- **Generation:** `generation_test.go` fragment-shape assertions with
  the ssh sampler faked at the seam (the sampler takes a
  runner func so tests inject canned headers; real ssh not required).
- **Equivalence:** new `TestPipelineEquivalence` case
  `ssh → group-by → sort` gated on `SSQL_TEST_SSH_HOST` (the LXD rig),
  asserting the typed and parallel lanes byte-match exec over a
  shuffled fixture. Sabotage the converter once (swap two fields) to
  watch the gate fail before trusting it.
- **Race:** `go test -race ./typed/...` covers the new parallel
  converter (hard CI gate already).

## Generated-code shape (target)

```go
// ssql from ssh db1 /data/events.csv
remoteCmd := ssql.BuildRemoteCommand("/usr/bin/ssql", *flagPath, "", nil)
sshCmd := exec.Command("ssh", *flagHost, remoteCmd)
…
recordsRaw := ssql.ReadJSONFromReader(sshStdout)
records := typed.FromRecordsParallel(recordsRaw, func(r ssql.Record) EventRow {
    return EventRow{Service: ssql.GetOr(r, "service", ""), Status: ssql.GetOr(r, "status", int64(0)), …}
}, runtime.GOMAXPROCS(0))
// … downstream typed.GroupByParallel / Stream.Where exactly as if the
// source were a local CSV.
```

Readable, primitive-backed, and the whole downstream pipeline is the
same code a local typed source would get — which is the point.
