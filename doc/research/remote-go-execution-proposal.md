# Remote Go Execution Proposal

**Status:** Proposal
**Date:** 2026-05-03
**ssql version target:** v4.41+
**Prerequisites:** Phase A planner (v4.39), Phase B mixed mode (v4.40)
**Related:** `distributed-ssh-processing.md`, `distributed-shard-catalog.md`

## TL;DR

`ssql from ssh HOST FILE | <pipeline>` currently streams the entire
remote file over SSH then processes locally. If Go is installed on
the remote host, we can ship a generated Go program (a few KB)
instead, run it on the remote, and stream just the (typically
much smaller) result back. This unlocks the typed-parallel
runtime — including the workstation 215× speedup — for remote
data without copying the source file.

This is mostly plumbing on top of the v4.40 codegen pipeline.
Estimated effort: ~2-3 days for v1 (single-host source-shipping
path), ~1 week with the catalog extension and a cross-compile
fallback for hosts without Go.

## Motivation

The current model in v4.40:

```bash
# Pipeline runs locally; SSH ships the entire 1.4 GB file
ssql from ssh node1 /data/logs.csv \
  | ssql where -if status ge 500 \
  | ssql group-by service -count n
```

Even with the existing `--` pushdown (which runs the remote
`ssql` binary against the file before streaming JSONL back), we
pay:

- Per-row JSONL serialization on the remote
- JSONL parsing on the local side
- Whatever the remote ssql binary's internal cost is —
  Record-mode for everything in the pushdown, no Stream[T]

The v4.40 typed-parallel codegen achieves ~215× speedup vs the
interactive CLI on the user's 14.6M-row workstation file. None
of that speedup is available to the SSH path today.

**With remote Go execution:**

```bash
# Same pipeline; codegen + ship + run remotely
ssql from ssh node1 /data/logs.csv \
  | ssql where -if status ge 500 \
  | ssql group-by service -count n
# Local: generate Go program for the whole pipeline
# SSH:   ship ~6 KB source to /tmp/ssql-gen-XXX/ on node1
# Remote: go run, reading /data/logs.csv directly (no SSH transit)
# Stream: only the aggregated result rows return over SSH
```

For the canonical workstation file, the pipeline drops from
"SSH the whole 1.4 GB then aggregate" to "ssh-and-go-run, return
~hundreds of bytes". The remote does the typed-parallel CSV
parse + group-by; the local pipeline gets the aggregated rows.

## Why this is mostly plumbing

The hard parts are already shipped:

1. **Codegen exists** — `ssql generate go` (and `-build` /
   `-run`) produces self-contained Go that runs the entire
   pipeline. Phase A (v4.39) made it parallel-by-default; Phase
   B (v4.40) made it mixed-mode.
2. **SSH execution exists** — `ssql.ExecSSH` and the `from ssh`
   command already handle authentication, command escaping
   (`ShellQuote`), and result streaming.
3. **`--` pushdown exists** — the framework for "send this
   command remote, run, stream output back" is established.
   Remote-Go is a natural extension: instead of running
   `ssql ...` remotely, run the generated Go program.
4. **Standalone binary path exists** — `generate go -build PATH`
   already compiles a standalone binary. Cross-compile is a
   matter of `GOOS=...$REMOTE_OS GOARCH=...$REMOTE_ARCH`.

What's new:

- Capability probe (does the remote have Go? what version?)
- Source-shipping protocol (or cross-compiled-binary shipping)
- A new flag/mode to opt in (or auto-detect)
- Error surfacing for compile failures on remote

## Design space

### 1. Capability discovery

Probe each remote host once and cache. Three things to check:

- `go version` (need 1.23+ for iter.Seq2)
- Internet access (`curl -sf https://proxy.golang.org/ -o /dev/null`)
  — needed for the ship-source-and-go-get path
- GOOS / GOARCH (`uname -s && uname -m`) — needed for the
  cross-compile-and-ship-binary path

Cache: `~/.ssql/host-caps.json` keyed by SSH host. Invalidate on
mismatch (e.g., user updates Go on the remote). One-line
`-rediscover` flag forces refresh.

### 2. Three execution modes

The proposal supports three remote-execution flavours. Auto-pick
based on capability probe; flags override.

#### Mode A: Ship source + `go run` (default)

Requires: Go 1.23+ on remote, internet (or local module proxy).

```
local:  generate go > /tmp/ssql-gen-X.go
        scp /tmp/ssql-gen-X.go node1:/tmp/ssql-gen-X/main.go
remote: cd /tmp/ssql-gen-X && go mod init ssqlgen \
          && go get github.com/rosscartlidge/ssql/v4@v4.40.0 \
          && go run . > /tmp/ssql-gen-X.out
local:  cat back via ssh, plumb into local pipeline
```

First run on a fresh host pays ~10s for `go get`. Subsequent
runs hit the local Go module cache on the remote. Subsequent
ssql invocations against the same host can skip the get.

#### Mode B: Cross-compile + ship binary

Requires: `uname` probe succeeded, no Go required on remote.

```
local:  GOOS=$REMOTE_OS GOARCH=$REMOTE_ARCH go build \
          -tags slim -o /tmp/ssql-gen-X-bin
        scp /tmp/ssql-gen-X-bin node1:/tmp/ssql-gen-X-bin
remote: chmod +x /tmp/ssql-gen-X-bin && /tmp/ssql-gen-X-bin
```

No remote Go install needed. Slim build keeps the binary small
(~10MB stripped). Trade: local CGO complications if the
generated program needs Parquet/Arrow (slim handles this) —
fall back to Mode A.

#### Mode C: Module proxy mirror

Requires: Go on remote, GOPROXY env points to a local mirror.

For airgapped / locked-down fleets where Mode A fails (no
internet) but Mode B's cross-compile is undesirable. Out of
scope for v1.

### 3. Pipeline boundary — what gets sent remote?

Three natural cuts:

- **All of it** — the entire pipeline runs on the remote, only
  the final output streams back. Best when the pipeline ends in
  a sink (`to csv`, `to table`, etc.) that's text-out.
- **Up to the last reduction** — sink runs locally so the user
  can pipe the result into other commands or render charts/etc.
- **User-marked boundary** — `--remote-here` flag or similar.

Default to "all of it" and document the alternative for chart
generation. The sink fragment is easy to detect at codegen time
(`final` fragment Type).

### 4. Catalog extension — heterogeneous fleets

`from catalog shards.csv` opens N parallel SSH connections, one
per shard. Each shard host probably has its own GOOS/GOARCH.
Per-host capability probe handles this: shard 1 (amd64 with Go)
gets Mode A, shard 2 (ARM64 without Go) gets Mode B with a
cross-compiled ARM binary. Local Go does the cross-compile per
host.

For homogeneous fleets the binary is built once and cached for
the catalog run.

### 5. Error surfacing

Compile errors on the remote currently surface as opaque SSH
stderr. Need to capture them and emit them locally with file:line
pointing at the user's *local* pipeline source, not the remote
generated Go file.

Approach: keep the generated Go file locally (say `/tmp/ssql-gen-
X.go`), reverse-map line numbers to pipeline stages via the
`Generated by` comment that codegen already emits, and print
something like:

```
remote compile error on node1:
  /tmp/ssql-gen-X/main.go:42:18 — undefined: typed.WhateverOp
  → corresponds to: ssql group-by relationship -X foo
```

### 6. Trust model

`--` pushdown today executes the user's locally-installed `ssql`
binary (well-defined surface). Remote Go execution executes
arbitrary code we generate.

Same trust boundary as SSH itself — if the user can SSH to the
host, they can already run arbitrary code there. But we should
document explicitly what gets shipped + executed and provide a
`-show-remote-source` flag that prints what would be sent
without sending it (dry-run).

## Implementation sketch

### Phase A — Mode A only, single host (~2 days)

1. Add `lib/sshgo/`:
   - `Probe(host) (Caps, error)` — runs `go version && uname -s
     && uname -m` over SSH, parses, returns. No caching yet.
   - `RunRemote(host, goSrc) (stdout, error)` — scp source, go
     run, stream stdout.
2. Add `-go` flag to `ssql from ssh` (or `-go-remote`):
   - When set: run codegen for the rest-of-pipeline (using
     existing assembleTypedFragments), call sshgo.RunRemote,
     pipe result into local pipeline.
3. Tests using LXD containers (per `ssh-test-environment.md`):
   one container with Go installed, one without. Verify
   correct fallback to non-`-go` SSH path when remote Go is
   missing.

### Phase B — capability cache + auto-detect (~1 day)

4. `~/.ssql/host-caps.json` — JSON cache, expires after 24h or
   on `-rediscover`.
5. Default behaviour (no `-go` flag): probe; if remote has Go,
   use Mode A. Otherwise fall back to current SSH-streams-bytes.
6. `-no-remote` opt-out for users who want the old behaviour.

### Phase C — catalog + cross-compile fallback (~3 days)

7. `from catalog` extension — per-shard capability probe in
   parallel, per-shard execution mode selection.
8. Mode B (cross-compile) for hosts without Go — `GOOS`/`GOARCH`
   from probe, `go build -tags slim -o`, scp binary.
9. Heterogeneous-fleet test: 3 LXD containers, one ARM (via
   qemu), one without Go, one fully equipped.

### Out of scope for v1

- Mode C (module proxy mirror) — defer until someone has the
  airgapped use case.
- Persistent remote daemon — overkill; per-invocation `go run`
  is fast enough after the first warm-up.
- GPU on remote — `from ssh -gpu` already handles this for the
  current Record-mode path; folding into typed-Go remote is a
  v2 question (need typed.Stream + GPU helpers first).

## Failure modes

### Module download taking forever on first run

Add a per-host warmup step: `ssql ssh-warmup HOST` that
pre-pulls the ssql module to the host's Go module cache.
Run once after adding a host.

### Go version too old on remote

Probe catches it. Emit clear error:
`node1 has Go 1.21; ssql remote-go needs 1.23+`.
Fall back to non-`-go` path if not opted-in.

### Wire-format drift between local and remote ssql versions

The generated Go pulls in `github.com/rosscartlidge/ssql/v4` at
a specific version (the current local version). The remote
fetches that exact version. So local and remote are always in
sync per invocation. But: if the user's local ssql is unreleased
(dev build), `go get` on the remote will fail. Mitigation:
detect dev-build and either fall back or document the workaround
(e.g., push the local source to a temporary git ref).

### Disk pressure on /tmp

A pathological pipeline with a massive Parquet read could fill
the remote /tmp during go-run with module-cache + binary +
output. Default to /tmp; expose `-remote-workdir` for users
with constrained /tmp.

### Quota / kill-by-OOM on remote

The remote process now does the full data pass. If the host has
tight memory limits, it gets OOM-killed and we see SSH exit code
137. Surface clearly: `node1 OOM-killed (exit 137); the
pipeline's working set exceeded available RAM`.

## Open questions

1. **Auto-enable when probed?** Or always require `-go` opt-in
   for v1, then flip the default in v2 once it's well-tested?
   Lean: opt-in for v1, default-on for v2.
2. **What about local-side stages downstream of `from ssh`?**
   E.g. `from ssh A | join from-ssh-B | to chart`. The `join`
   could run on either A or B (or locally). Phase A runs the
   left source (A) remote, ships result back, joins locally.
   Phase D could explore distributed join.
3. **Caching the cross-compiled binary?** `~/.ssql/bin-cache/$ssql_version-$goos-$goarch`
   — rebuild only when ssql version or target changes. Probably
   yes for catalog scenarios.
4. **Trust prompt?** First-time use against a host could show:
   "ssql will run go-generated code on node1 — show source? [y/N]".
   Not a security boundary (SSH already lets you run anything),
   but a transparency aid. Probably skip for v1.

## Why this is worth doing now

- Phase A + B (v4.39, v4.40) gave us the typed-parallel runtime.
  Remote-Go is what makes the speedups available to the
  multi-host workflows ssql is designed for.
- The plumbing is genuinely small — the existing ssh, codegen,
  pushdown, and capability layers handle 90% of it.
- The user value is large and easy to demo: same pipeline,
  same syntax, ~100× faster aggregations against a remote
  big-CSV / big-Parquet / big-Arrow file.
- Aligns with the project's "any pipeline becomes the optimal
  Go program" thesis. Today the optimisation stops at the SSH
  boundary; this extends it across the boundary.

## See also

- `distributed-ssh-processing.md` — original SSH proposal (shipped v4.27.0)
- `distributed-shard-catalog.md` — multi-shard catalog (shipped v4.27.0)
- `typed-auto-parallel-proposal.md` — Phase A planner (shipped v4.39.0)
- `mixed-mode-pipelines-proposal.md` — Phase B mixed mode (shipped v4.40.0)
- `ssh-test-environment.md` — LXD containers for SSH testing
