# Remote Go Execution Proposal

**Status:** Proposal (rev 3, 2026-05-04)
**Date:** 2026-05-03 (orig); 2026-05-04 (rev 2: .ssql-as-unit; rev 3: drop fallback modes — ssql-on-remote required)
**ssql version target:** v4.41+
**Prerequisites:** Phase A planner (v4.39), Phase B mixed mode (v4.40), `-script PATH` flag (codegen-wrapper-proposal.md)
**Related:** `distributed-ssh-processing.md`, `distributed-shard-catalog.md`, `codegen-wrapper-proposal.md`

## TL;DR

`ssql from ssh HOST FILE | <pipeline>` currently streams the entire
remote file over SSH then processes locally. **If ssql is installed
on the remote** (already a requirement for `--` pushdown), we can
ship a tiny `.ssql` script (a few hundred bytes) describing the
pipeline, run `ssql generate go -script /tmp/X.ssql -run` on the
remote, and stream just the (typically much smaller) result back.
This unlocks the typed-parallel runtime — including the workstation
215× speedup — for remote data without copying the source file.

The .ssql script (from the codegen-wrapper proposal) is the natural
unit of remote work: human-readable, KB-sized, no `go.mod`
plumbing, no `go get` over the internet, the remote's deployed
ssql version controls behavior.

**Single mode, single requirement: ssql installed on the remote.**
We considered fallback modes for hosts that have only Go or
neither, and rejected them — see "Considered and rejected" below.

Estimated effort: **~1 day for Phase A** (single host), **+1 day
for Phase B** (catalog extension). Total ~2 days for the whole
feature.

## Motivation

The current model in v4.40:

```bash
# Pipeline runs locally; SSH ships the entire 1.4 GB file
ssql from ssh node1 /data/logs.csv \
  | ssql where -if status ge 500 \
  | ssql group-by service -count n
```

Even with `--` pushdown (which runs the remote `ssql` binary
against the file before streaming JSONL back), we pay:

- Per-row JSONL serialisation on the remote
- JSONL parsing on the local side
- Whatever the remote ssql binary's internal cost is —
  Record-mode for everything in the pushdown, no Stream[T]

The v4.40 typed-parallel codegen achieves ~215× speedup vs the
interactive CLI on the user's 14.6M-row workstation file. None of
that speedup is available to the SSH path today.

**With remote-Go execution:**

```bash
# Same syntax; remote does typed-parallel codegen + run
ssql from ssh node1 /data/logs.csv \
  | ssql where -if status ge 500 \
  | ssql group-by service -count n
# Local:  build a .ssql script for the whole pipeline (~500 B)
# SSH:    scp script to /tmp/ssql-X.ssql on node1
# Remote: ssql generate go -script /tmp/ssql-X.ssql -run
#         (typed-parallel: parses CSV in parallel, aggregates,
#          prints just the result rows)
# Stream: aggregated rows return over SSH
```

For the canonical workstation file, the pipeline drops from "SSH
the whole 1.4 GB then aggregate" to "ssh-and-go-run, return
~hundreds of bytes". The remote does the typed-parallel CSV parse
+ group-by; the local pipeline gets the aggregated rows.

## The .ssql script as the unit of remote work

The codegen-wrapper proposal (`codegen-wrapper-proposal.md`)
introduces `ssql generate go -script PATH`, which reads a pipeline
from a file or `<(heredoc)`. That file format is exactly what
remote-Go execution needs to ship.

A pipeline that today looks like:

```bash
(export SSQLGO=record; ssql from logs.csv \
  | ssql where -if status ge 500 \
  | ssql group-by service -count n \
  | ssql to csv) | ssql generate go -run
```

…becomes a self-contained `pipeline.ssql`:

```
ssql from logs.csv
| ssql where -if status ge 500
| ssql group-by service -count n
| ssql to csv
```

…runnable as `ssql generate go -script pipeline.ssql -run`.

For remote execution, **the .ssql file is the payload.** Local
side replaces the remote source path (`logs.csv` becomes whatever
the remote sees) and ships the file. Remote side runs `ssql
generate go -script -run`. Done.

Sidesteps every complication that arises from shipping Go:

- No `go.mod` to manage
- No `go get` over the internet
- No first-time module-download lag
- No version pinning across local and remote
- Payload is ~500 B (script) instead of MB-scale (Go + module cache)
- The script is human-readable — `ssh node1 cat /tmp/ssql-X.ssql`
  shows exactly what's running
- Remote's deployed ssql version controls behavior

## Why generate on the remote (not on the local)

We considered both directions: generate Go locally and ship .go,
or ship .ssql and generate on the remote. Picking remote-side
generation:

| Aspect | Ship .ssql (remote codegen) | Ship .go (local codegen) |
|---|---|---|
| Payload size | ~500 B | ~6 KB + 50 MB module cache on first run |
| Internet on remote | No | Yes (`go get`) |
| First-run cost | None | ~10 s for module download |
| `go.mod` plumbing | None | Local manages it |
| Debuggability | `cat` the script | Opaque generated Go |
| Reproducibility from logs | Just the .ssql | Need exact local ssql version too |
| Trust surface | Pipeline DSL | Arbitrary Go |
| Failure-mode visibility | "ssql says X" | "go run failed with…" |

The architectural argument: **the remote's deployed ssql version
should govern remote behavior.** If someone has updated `node1` to
v4.41 with a new feature, they presumably want `node1` to use
that version — not whatever happens to be on the laptop running
the pipeline. Local-side codegen accidentally bypasses the
remote's deployment.

The one real downside is **version skew**: if your local CLI
accepts a flag the remote's older ssql doesn't, the script fails
on the remote with a confusing error. Mitigations:
- Capability probe caches the remote's `ssql version` and warns
  at the time of the call (not at exec time)
- Optional `# require: vX.Y.Z` directive in the .ssql script for
  scripts that need a specific feature

Both fixes are cheap; the upside in simplicity is large.

## Mechanism

```
local:  scp pipeline.ssql node1:/tmp/ssql-X.ssql
remote: ssql generate go -script /tmp/ssql-X.ssql -run
local:  ssh streams stdout back into the local pipeline
```

That's the entire mechanism. No go.mod, no module download, no
cross-compile, no Go-on-remote requirement. The remote's existing
ssql binary already knows how to do typed-parallel codegen +
go-run; we hand it a script.

## Considered and rejected: fallback modes

The earlier draft included two fallback modes for hosts that
*don't* have ssql installed:

- **Mode B** — local generates `.go`, ships source + `go.mod`,
  remote runs `go run`. For hosts that have Go but not ssql.
- **Mode C** — local cross-compiles a binary, ships, executes.
  For hosts with neither.

Dropped from v1 (and arguably from v2+). Reasons:

1. **`--` pushdown already requires ssql on the remote.** Anyone
   using `from ssh` for non-trivial work has already deployed
   ssql to the targets. Mode A inherits that requirement
   transparently. B/C add modes for personas that don't really
   exist in the user base.

2. **Trust surface.** Mode A ships a declarative pipeline DSL.
   B/C ship arbitrary Go or executable binaries. Users running
   ssql in production environments may have a different posture
   toward "run this Go I generated for you" vs. "run this
   declarative script with your already-trusted ssql binary."

3. **Version coherence.** Only Mode A respects the remote's
   deployed ssql version. B/C bypass it — laptop's local version
   silently overrides whatever was deliberately deployed. This
   makes B/C a deployment surprise.

4. **Implementation surface.** B/C adds cross-compile toolchain
   concerns, go.mod plumbing, module-cache management, Go
   version probes, MB-scale payloads. Cutting them roughly halves
   total effort and removes whole categories of failure mode.

5. **Architectural value of "prepared hosts".** Every clustered
   system worth using assumes nodes have been deliberately
   prepared (Kubernetes joins, database replicas, etc.). Modes
   B/C invite the opposite — accidental compute on random hosts
   — which is rarely what the user actually wants.

If a future user really has the "Go but not ssql" use case, they
can install ssql (it's a single static binary, one `go install`
away). The cost is small and the coordination value is large.

## Capability discovery

The probe shrinks to a single question: **does the remote have
ssql, and what version?**

```
ssh node1 'ssql version 2>/dev/null || echo no-ssql'
```

Parses the output into:

```go
type HostCaps struct {
    HasSSQL  bool
    SSQLVer  string  // e.g. "v4.40.0"
    ProbedAt time.Time
}
```

Cache: `~/.ssql/host-caps.json` keyed by SSH host. Default TTL:
24 h. Force refresh with `-rediscover`. Invalidate on observed
mismatch (e.g., remote ssql errors with "unknown flag" → cached
version is stale, rerun the probe).

Probe is ~50 ms over SSH. Cache hit makes subsequent invocations
free.

If the remote doesn't have ssql, we **fall back to the existing
SSH-streams-bytes path** (the v4.27 behavior). No remote-Go
acceleration, but the pipeline still works. Surface a one-line
note: "node1 has no ssql installed; falling back to standard SSH
streaming (install ssql on node1 to enable remote-Go acceleration)."

## Pipeline boundary — what gets sent remote?

Three natural cuts; the choice is per-pipeline:

- **All of it** — entire pipeline runs on the remote, only the
  final output streams back. Best when the pipeline ends in a
  text sink (`to csv`, `to table`, `count`, JSONL fallback).
- **Up to the last reduction** — sink runs locally so the user
  can pipe the result into other commands or render charts/etc.
  Detected: if the sink is `to chart` / `to explore` / `to
  animate`, the sink stays local.
- **User-marked boundary** — `--remote-here` flag if someone
  needs explicit control.

Default to "all of it" with the chart/explore exception. The sink
fragment is easy to detect at codegen time (`final` fragment Type,
plus a small allowlist).

## Catalog extension — homogeneous fleets, parallel per-shard

`from catalog shards.csv` opens N parallel SSH connections, one
per shard. Each shard host gets its own capability probe (in
parallel during the dispatch phase).

Since we now require ssql on every shard target, the catalog case
is straightforwardly per-shard execution of the same Mode A flow:

- For each shard host: probe (cached); ship the per-shard .ssql
  script (path-rewritten for that shard's data); exec `ssql
  generate go -script -run`; stream stdout.
- Local side does the K-way merge of result streams (already
  does in the existing catalog code).

Each shard returns aggregated rows of size O(num_groups) instead
of O(num_input_rows) — the win compounds with shard count.

If any shard host doesn't have ssql, that shard falls back to the
SSH-streams-bytes path; other shards still get acceleration. Mixed
mode within a single catalog query.

## Error surfacing

Three error categories, with different surface treatments:

1. **Script syntax errors** caught locally before shipping:
   "pipeline.ssql:3 — unknown command `wherq`; did you mean
   `where`?". The .ssql preprocessor (codegen-wrapper) emits these.
2. **Remote codegen errors** (remote ssql says no): the remote
   ssql's existing error messages flow back via SSH stderr. Wrap
   with "on node1: ".
3. **Remote runtime errors** (the generated program failed):
   exit code + stderr captured. For OOM (exit 137):
   "node1 ran out of memory; the pipeline's working set exceeded
   available RAM."

## Trust model

`--` pushdown today executes the user's locally-installed `ssql`
binary remotely with arguments derived from the user's CLI input.
Remote-Go execution extends this in a controlled way: same `ssql`
binary on the remote, executing a `.ssql` script (declarative
pipeline DSL).

**No new trust surface beyond what already exists for `--`
pushdown.** A `-show-remote-source` (or `-dry-run`) flag prints
what would be sent without sending it — useful for review and
for copy-paste-into-CI workflows.

## Implementation sketch

### Phase A — single host (~1 day)

1. Add `lib/sshgo/`:
   - `Probe(host) (HostCaps, error)` — `ssh HOST 'ssql version'`,
     parses output. Cache in `~/.ssql/host-caps.json`.
   - `RunRemote(host, scriptBytes []byte) (stdout, error)` —
     scps the .ssql, runs `ssql generate go -script -run`, streams
     stdout.
2. Add `-remote` flag to `ssql from ssh` (or auto-detect when
   the source is a remote file and the rest of the pipeline can
   ship there):
   - Build a .ssql script for the rest-of-pipeline.
   - Path-rewrite: `from ssh HOST PATH` becomes `from PATH` in
     the script (the file is local on the remote).
   - Call `sshgo.RunRemote(host, script)`.
   - Pipe result into the local pipeline.
3. Tests using LXD containers (per `ssh-test-environment.md`):
   one container with ssql v4.40 installed, one without ssql.
   Verify identical output between Mode A and the standard SSH
   path; verify graceful fallback when ssql isn't installed.

### Phase B — catalog extension (+1 day)

4. `from catalog` — per-shard probe in parallel during dispatch.
5. Each shard runs Mode A independently; results stream into
   the existing K-way merge.
6. Heterogeneous-fleet test: 3 LXD containers, mix of ssql-yes
   and ssql-no, verify partial acceleration works.

### Out of scope

- **Go-on-remote (old Mode B)** and **cross-compile binary (old
  Mode C)** — see "Considered and rejected" above. Not blocked;
  not planned.
- **Persistent remote daemon** — overkill; `ssql generate go
  -script -run` is fast enough per-invocation.
- **GPU on remote** — `from ssh -gpu` already handles this for
  the current Record-mode path; folding into typed-Go remote is
  a v2 question (need typed.Stream + GPU helpers first).
- **Distributed join** — `from ssh A | join from ssh B` needs
  cross-node coordination. Phase A executes A remote, ships
  result back, joins locally. v2 can explore.

## Failure modes

### Version skew

Remote ssql is older than local; doesn't recognise a flag or
command in the script. Detection: the probe captures the remote
version; we compare against the script's required version
(inferred from feature usage, or declared in a `# require: vX.Y.Z`
header). Surface: "node1 has ssql v4.39; this script uses
`count` (v4.40+) — upgrade ssql on node1 or drop -remote."

### Remote ssql not installed

Probe catches it. Fall back to existing SSH-streams-bytes path
with a clear note: "node1 has no ssql installed; falling back to
standard SSH streaming (install ssql on node1 to enable remote-Go
acceleration)."

### Disk pressure on remote /tmp

A pathological pipeline could fill `/tmp` during go-run with
intermediate output + module cache (the remote ssql's `go run`
uses *its* module cache, but generated programs may produce
output to /tmp). Surface clearly when it happens; document
`-remote-workdir` for hosts where /tmp is constrained.

### Quota / OOM on remote

Remote process now does the full data pass. If the host has tight
memory limits, it gets OOM-killed; SSH exit code 137. Surface:
"node1 OOM-killed (exit 137); the pipeline's working set
exceeded available RAM."

### `from ssh` source path that doesn't exist on the remote

Remote ssql errors at script-execution time:
"/data/logs.csv: no such file". Wrap with "on node1: ".

## Open questions

1. **Auto-enable when probe shows ssql installed?** Or always
   require `-remote` opt-in for v1, then flip the default in v2
   once well-tested? Lean: **opt-in for v1**, default-on for v2.
2. **What about local-side stages downstream of `from ssh`?**
   E.g. `from ssh A | join from-ssh-B | to chart`. Phase A runs
   A remotely, ships result back, joins locally. v2 can explore
   distributed join.
3. **Trust prompt?** First-time use against a host shows: "ssql
   will run a script on node1 — show source? [y/N]". Not a
   security boundary (SSH already lets you run anything), but a
   transparency aid. Probably skip for v1.
4. **`# require: vX.Y.Z` in the .ssql header?** Useful for
   scripts shared between teams with different deployed
   versions. Cheap to add (preprocessor reads the directive,
   compares against probed version, errors early). Could be in
   v1.

## Why this is worth doing now

- Phase A + B (v4.39, v4.40) gave us the typed-parallel runtime.
  Remote-Go is what makes the speedups available to the
  multi-host workflows ssql is designed for.
- The `-script` flag from the codegen-wrapper proposal is
  exactly the unit of remote work we need. Doing both proposals
  together is cheaper than either alone.
- The plumbing is genuinely small for v1 — ~1 day for single
  host, +1 day for catalog. The existing ssh + pushdown +
  capability layers handle 90% of it; remote-Go just hands `ssql
  generate go -script -run` over SSH instead of running ssql
  commands directly.
- The user value is large and easy to demo: same pipeline, same
  syntax, ~100× faster aggregations against a remote big-CSV /
  big-Parquet / big-Arrow file.
- Aligns with the project's "any pipeline becomes the optimal Go
  program" thesis. Today the optimisation stops at the SSH
  boundary; this extends it across the boundary.

## See also

- `codegen-wrapper-proposal.md` — `-script PATH` and `-shell-helpers` (prerequisite)
- `distributed-ssh-processing.md` — original SSH proposal (shipped v4.27.0)
- `distributed-shard-catalog.md` — multi-shard catalog (shipped v4.27.0)
- `typed-auto-parallel-proposal.md` — Phase A planner (shipped v4.39.0)
- `mixed-mode-pipelines-proposal.md` — Phase B mixed mode (shipped v4.40.0)
- `ssh-test-environment.md` — LXD containers for SSH testing
