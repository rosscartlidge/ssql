# Remote Go Execution Proposal

**Status:** Proposal (rev 2, 2026-05-04)
**Date:** 2026-05-03 (orig); 2026-05-04 (rev: .ssql-script-as-unit)
**ssql version target:** v4.41+
**Prerequisites:** Phase A planner (v4.39), Phase B mixed mode (v4.40), `-script PATH` flag (codegen-wrapper-proposal.md)
**Related:** `distributed-ssh-processing.md`, `distributed-shard-catalog.md`, `codegen-wrapper-proposal.md`

## TL;DR

`ssql from ssh HOST FILE | <pipeline>` currently streams the entire
remote file over SSH then processes locally. If `ssql` is installed
on the remote (we already require this for `--` pushdown), we can
ship a tiny `.ssql` *script file* (a few hundred bytes) describing
the pipeline, run `ssql generate go -script /tmp/X.ssql -run` on
the remote, and stream just the (typically much smaller) result
back. This unlocks the typed-parallel runtime — including the
workstation 215× speedup — for remote data without copying the
source file.

The .ssql script (from the codegen-wrapper proposal) is the natural
unit of remote work: human-readable, KB-sized, no `go.mod`
plumbing, no `go get` over the internet, the remote's deployed
ssql version controls behavior.

Estimated effort: **~1 day** for v1 (Mode A — ssql-on-remote, the
ssh-already-works path). Heavier modes (Go-only remotes,
binary-only remotes) layer on top as fallbacks for less common
deployment shapes.

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
#         (local-typed-parallel: parses CSV in parallel, aggregates,
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

This sidesteps every complication the original proposal worried
about:

- No `go.mod` to manage
- No `go get` over the internet
- No first-time module-download lag
- No version pinning across local and remote
- Payload is ~500 B (script) instead of ~6 KB (Go) plus
  ~50 MB of module cache fetched on first run
- The script is human-readable — `ssh node1 cat /tmp/ssql-X.ssql`
  shows exactly what's running

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
- Optional `-require-version vX.Y.Z` directive in the .ssql header
  for scripts that need a specific feature

Both fixes are cheap; the upside in simplicity is large.

## Three execution modes (reframed)

The .ssql-as-payload framing makes Mode A genuinely simple. Modes
B and C remain as fallbacks for less common deployment shapes.

### Mode A — ssql-on-remote (default; ~1 day to ship)

Requires: ssql installed on the remote (already required for `--`
pushdown).

```
local:  scp pipeline.ssql node1:/tmp/ssql-X.ssql
remote: ssql generate go -script /tmp/ssql-X.ssql -run
local:  ssh streams stdout back into the local pipeline
```

That's it. No go.mod, no module download, no cross-compile, no
Go-on-remote requirement. The remote's existing ssql binary
already knows how to do typed-parallel codegen + go-run; we just
hand it a script.

### Mode B — Go-on-remote (fallback; +1 day)

For hosts that have Go but not ssql. Local generates the .go file
and ships it along with a `go.mod`. Remote runs `go run`.

```
local:  ssql generate go -script pipeline.ssql > /tmp/main.go
        echo 'module ssqlgen\ngo 1.23\nrequire github.com/rosscartlidge/ssql/v4 vX.Y.Z' > /tmp/go.mod
        scp /tmp/main.go /tmp/go.mod node1:/tmp/ssql-X/
remote: cd /tmp/ssql-X && go mod tidy && go run .
```

Pays the ~10 s first-run cost for `go get`. Subsequent runs hit
the remote's Go module cache.

### Mode C — cross-compile binary (fallback-fallback; +1-2 days)

For hosts with neither ssql nor Go. Local cross-compiles the
generated program, scps the binary, executes.

```
local:  GOOS=$REMOTE_OS GOARCH=$REMOTE_ARCH go build -tags slim \
          -o /tmp/ssql-X-bin
        scp /tmp/ssql-X-bin node1:/tmp/ssql-X-bin
remote: chmod +x && /tmp/ssql-X-bin
```

Useful for embedded / locked-down fleets. ARM target needs local
cross-compile toolchain (Go has it built in for pure-Go builds).

### Auto-selection

Capability probe (see below) decides per host:
- Has `ssql` (compatible version)? → Mode A
- Else has `go` (1.23+)? → Mode B
- Else cross-compile fallback → Mode C
- Else fall back to current SSH-streams-bytes path

## Capability discovery

Probe each remote host once per session, cache locally.

The probe runs over SSH:
```
ssql version 2>/dev/null; \
go version 2>/dev/null; \
uname -s; uname -m
```

Parses the output into:

```go
type HostCaps struct {
    HasSSQL    bool
    SSQLVer    string  // e.g. "v4.40.0"
    HasGo      bool
    GoVer      string  // e.g. "go1.23.4"
    GOOS       string  // "linux", "darwin", …
    GOARCH     string  // "amd64", "arm64", "386"
    ProbedAt   time.Time
}
```

Cache: `~/.ssql/host-caps.json` keyed by SSH host. Default TTL:
24 h. Force refresh with `-rediscover`. Invalidate on observed
mismatch (e.g., remote ssql errors with "unknown flag" → cached
version is stale, rerun the probe).

For the simple case (all hosts have ssql installed), the probe is
~50 ms over SSH and the cache hit makes subsequent invocations
free.

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
fragment is easy to detect at codegen time (`final` fragment
Type, plus a small allowlist).

## Catalog extension — heterogeneous fleets

`from catalog shards.csv` opens N parallel SSH connections, one
per shard. Each shard host gets its own capability probe (in
parallel during the dispatch phase). Per-host execution mode
selection naturally falls out:

- node1 (amd64, ssql v4.40 installed) → Mode A
- node2 (ARM64, Go installed but no ssql) → Mode B
- node3 (no Go, no ssql) → Mode C with ARM cross-compile

For homogeneous fleets the binary (Mode C) or .go shipping (Mode
B) caches once and reuses across shards.

The N-way K-merge of result streams happens locally (already does
in the existing catalog code). Each shard returns aggregated rows
of size O(num_groups) instead of O(num_input_rows) — the win
compounds with shard count.

## Error surfacing

Three error categories, with different surface treatments:

1. **Script syntax errors** caught locally before shipping:
   "pipeline.ssql:3 — unknown command `wherq`; did you mean
   `where`?". The .ssql preprocessor (codegen-wrapper) emits these.
2. **Remote codegen errors** (Mode A — remote ssql says no): the
   remote ssql's existing error messages flow back via SSH stderr.
   Wrap with "on node1: ".
3. **Remote runtime errors** (Mode B/C — generated program
   failed): exit code + stderr captured. For OOM (exit 137):
   "node1 ran out of memory; the pipeline's working set
   exceeded available RAM."

For Mode B, compile errors are reverse-mapped to pipeline stages
via the `Generated by` comment that codegen already emits — so
the user sees "stage 3: ssql group-by relationship -X foo" rather
than "main.go:42:18 — undefined: typed.WhateverOp".

## Trust model

`--` pushdown today executes the user's locally-installed `ssql`
binary remotely (well-defined surface). Remote-Go execution
extends this in a controlled way:

- **Mode A** ships a .ssql script, executed by the remote's
  `ssql` (same binary the user already invokes for `--` pushdown).
  No new trust surface beyond what already exists.
- **Mode B/C** ships generated Go, executed by `go run` or
  directly. Same trust boundary as SSH itself: if the user can
  SSH to the host they can already run arbitrary code there.

A `-show-remote-source` (or `-dry-run`) flag prints what would be
sent without sending it — useful for review and for
copy-paste-into-CI workflows.

## Implementation sketch

### Phase A — Mode A only, single host (~1 day)

1. Add `lib/sshgo/`:
   - `Probe(host) (HostCaps, error)` — runs the version-and-uname
     probe over SSH.
   - `RunRemote(host, scriptBytes []byte, mode string) (stdout, error)`
     — scps the .ssql, runs `ssql generate go -script -run`,
     streams stdout.
2. Add `-remote` flag to `ssql from ssh` (or auto-detect when
   the source is a remote file and the rest of the pipeline can
   ship there):
   - Build a .ssql script for the rest-of-pipeline.
   - Path-rewrite: the `from ssh HOST PATH` becomes `from PATH`
     in the script (the file is local on the remote).
   - Call `sshgo.RunRemote(host, script, mode)`.
   - Pipe result into the local pipeline.
3. Tests using LXD containers (per `ssh-test-environment.md`):
   one container with ssql v4.40 installed; verify the same
   pipeline produces the same output via Mode A vs. the existing
   non-remote path.

### Phase B — capability cache + Go-on-remote (+1 day)

4. `~/.ssql/host-caps.json` cache; `-rediscover` flag.
5. Auto-detect: probe; if ssql installed → Mode A; if only Go →
   Mode B (generate .go locally, scp, `go mod tidy && go run`).
6. `-no-remote` opt-out for users who want the old behaviour.

### Phase C — catalog + cross-compile fallback (+1-2 days)

7. `from catalog` extension — per-shard capability probe in
   parallel, per-shard execution mode selection.
8. Mode C (cross-compile) for hosts with neither ssql nor Go.
9. Heterogeneous-fleet test: 3 LXD containers (one ARM via qemu,
   one without Go, one fully equipped).

### Out of scope for v1

- **Module proxy mirror for airgapped fleets** — defer until
  someone has the use case. Mode A doesn't need it; Mode B does
  but the current target environments all have internet.
- **Persistent remote daemon** — overkill; per-invocation `go
  run` is fast enough after the first warm-up, and Mode A skips
  go-run entirely.
- **GPU on remote** — `from ssh -gpu` already handles this for
  the current Record-mode path; folding into typed-Go remote is a
  v2 question (need typed.Stream + GPU helpers first).
- **Distributed join** — a `from ssh A | join from ssh B` needs
  cross-node coordination. Phase A executes A remote, ships its
  result back, joins locally. v2 can explore.

## Failure modes

### Version skew (Mode A)

Remote ssql is older than local; doesn't recognise a flag or
command in the script. Detection: the probe captures the remote
version; we compare against the script's required version (either
inferred or declared in a `# require: vX.Y.Z` header). Surface:
"node1 has ssql v4.39; this script uses `count` (v4.40+)."

### Go version too old on remote (Mode B)

Probe catches it. Emit clear error: `node1 has Go 1.21; ssql
remote-Go execution needs 1.23+`. Skip Mode B; try Mode C.

### Remote `/tmp` is noexec / mounted with restrictive options

Mode A doesn't care (script is data, not executable). Mode B/C
need exec on the workdir. Expose `-remote-workdir DIR` for hosts
where `/tmp` doesn't allow exec.

### Disk pressure on remote /tmp

A pathological pipeline could fill `/tmp` during go-run with
module cache + binary + intermediate output. Surface clearly when
it happens; document `-remote-workdir`.

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
3. **Caching the cross-compiled binary (Mode C)?**
   `~/.ssql/bin-cache/$ssql_version-$goos-$goarch` — rebuild only
   when ssql version or target changes. Yes for catalog
   scenarios.
4. **Trust prompt?** First-time use against a host shows: "ssql
   will run a script on node1 — show source? [y/N]". Not a
   security boundary (SSH already lets you run anything), but a
   transparency aid. Probably skip for v1.
5. **`-require-version` in the .ssql header?** Useful for
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
- The plumbing is genuinely small for Mode A — ~1 day for v1.
  The existing ssh + pushdown + capability layers handle 90% of
  it; remote-Go just hands `ssql generate go -script -run` over
  SSH instead of running ssql commands directly.
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
