# Remote Go Execution Proposal

**Status:** Proposal (rev 4, 2026-05-06)
**Date:** 2026-05-03 (orig); 2026-05-04 (rev 2: .ssql-as-unit; rev 3: drop fallback modes; rev 4: codegen-symmetric, drop -remote interactive flag)
**ssql version target:** v4.42
**Prerequisites:** Phase A planner (v4.39), Phase B mixed mode (v4.40), `-script PATH` flag (v4.41)
**Related:** `distributed-ssh-processing.md`, `distributed-shard-catalog.md`, `codegen-wrapper-proposal.md`

## TL;DR

`ssql from ssh HOST PATH -- STAGES…` runs entirely on the remote
host. The mode is determined by the local invocation context:

- **Plain CLI** (no SSQLGO) → remote runs the existing record-mode
  CLI chain (`ssql … | ssql …`) — the v4.27 baseline behaviour.
- **`SSQLGO=record … | ssql generate go`** → remote runs **generated
  record-mode Go** (single Go process on the remote, faster than
  the multi-process CLI chain).
- **`SSQLGO=typed   … | ssql generate go`** → remote runs **generated
  typed-parallel Go** (Stream[T] + per-shard runtime — the v4.40
  speedups, on the remote).

The local-side generated Go (when SSQLGO is set) embeds the `.ssql`
script as a `const` string and inlines a small ssh-and-cat-and-run
helper. The result is **a single self-contained Go binary** that
orchestrates remote execution — no `~/.ssql/host-caps.json` cache
needed, no extra deployment artifacts, just the binary.

The mental model collapses to one sentence:

> Whatever mode the local pipeline runs in, the remote runs in too.

This rev drops the `-remote` interactive flag introduced in v4.41 —
it was a transitional shape. Users who want typed-parallel
acceleration interactively use the codegen path:

```bash
ssqlgen 'ssql from ssh node1 PATH -- where … + group-by …' -run
# expands to:
# (export SSQLGO=typed; ssql from ssh node1 PATH -- where … + group-by …) | ssql generate go -run
```

`-remote` and the related `-show-remote-source` dry-run flag are
removed (no deprecation cycle — the flag landed in v4.41.0 and
hadn't seen real use).

## Behaviour matrix

| Local invocation | Local does | Remote does |
|---|---|---|
| `ssql from ssh H P -- STAGES` (plain CLI) | compiled-binary handler | record-mode CLI chain (`ssql from P \| ssql STAGE1 \| …`) |
| `(SSQLGO=record; ssql from ssh H P -- STAGES) \| generate go -run` | generated record-mode Go (orchestrator) | generated record-mode Go (data plane) |
| `(SSQLGO=typed; ssql from ssh H P -- STAGES) \| generate go -run` | generated typed-parallel Go (orchestrator) | generated typed-parallel Go (data plane) |

Each row's local and remote run the same mode. The local generated
Go is a thin orchestrator: it opens an SSH connection, ships the
embedded `.ssql` script via stdin, exec's `ssql generate go -script
-mode $MODE -run` on the remote, streams stdout, propagates errors.
The remote does the actual data work.

## Why this rev

Rev 3 (v4.41) introduced `-remote` as an interactive opt-in flag.
It worked but added a second mental model the user had to track:

- "Am I in CLI mode? Codegen mode? Remote-Go mode?"
- "Does `-remote` compose with `SSQLGO=typed`? (Today: no — codegen
  silently wins.)"
- "If I want typed-parallel locally AND remotely, do I do
  `SSQLGO=typed -remote …`? (Today: no — remote part is dropped.)"

Rev 4 collapses this. There's exactly one decision: do you want
codegen at all (set SSQLGO) or not? If yes, the remote follows.
If no, you get today's CLI baseline. No third axis.

## Implementation

### 1. Drop interactive flags

- `-remote` flag on `ssql from ssh` — removed
- `-show-remote-source` dry-run flag — removed (it inspected the
  `-remote` path; no longer applicable)
- `executeFromSSHRemoteGo` function — removed
- `lib/sshgo/{Probe, RunRemote}` — kept, called from generated Go
  via inlined equivalent code (see below). The package itself can
  stay for any future tooling needs.

### 2. Codegen path generates ship-and-run Go

`generateFromSSHRemoteCode` (the existing codegen path for `from
ssh -- pushdown`) is updated to emit Go that:

1. Defines `const remoteScript = "<the .ssql pipeline as a string>"`
2. Inlines a small `runRemote(host, script string) error` that does
   `exec.Command("ssh", "-o", "BatchMode=yes", host, "trap 'rm -f
   /tmp/ssql-X.ssql' EXIT; cat > /tmp/ssql-X.ssql && ssql generate
   go -script /tmp/ssql-X.ssql -mode $MODE -run")`, with stdin =
   the script bytes and stdout = io.Writer the caller picks
3. Calls `runRemote` from `main`, wires stdout to a `readJSONSchemaAware
   → writeWithInferredSchema` chain so downstream local stages see
   the same wire format the existing CLI baseline produces

The mode is whatever SSQLGO was at codegen time. Get it from the
generator's environment, bake it into the generated Go's ssh
command.

### 3. .ssql script as a const string

Building the script reuses `buildRemoteSSQLScript` from v4.41 (it
still produces a clean multi-line bash pipeline). The codegen path
formats it as a Go raw-string literal:

```go
const remoteScript = `ssql from /data/x.csv
| ssql where -if status ge 500
| ssql group-by service -count n
`
```

Self-contained: the binary IS the artifact. No sibling files, no
config to ship.

### 4. Mode propagation

The generator sees `SSQLGO=record` (or typed) at the time `ssql
generate go` runs. It bakes that mode into the remote command:

```go
const remoteMode = "record"  // or "typed", per SSQLGO at codegen time
// ...
remoteCmd := fmt.Sprintf(
    `trap 'rm -f %s' EXIT; cat > %s && SSQLGO=%s ssql generate go -script %s -run`,
    path, path, remoteMode, path)
```

(Or — simpler — the generator passes `-mode $remoteMode` to the
remote `ssql generate go -script` since that flag was added in
v4.41 specifically for this kind of thing.)

### 5. Capability handling

Generated Go is a one-shot artifact — no cache, no probe
infrastructure. If the remote doesn't have ssql installed, the
generated program errors at exec time:

> `bash: line 1: ssql: command not found` → wrapped local error
> "remote node1 has no `ssql` installed; install ssql to enable
> code generation pipelines"

This is the right tradeoff: the binary knows it requires ssql on
the remote (that's part of its contract), and reports clearly when
that contract is violated. No interactive probe-and-fall-back
machinery.

## Catalog extension (later)

`from catalog` opens N parallel SSH connections, one per shard.
Codegen-symmetric remote-Go composes naturally: the catalog's
codegen path already builds per-shard pipelines; each becomes its
own embedded `.ssql` const + ship-and-run helper invocation in the
generated Go. Local merges results.

Defer to a separate phase. The single-host codegen-symmetric path
is the v4.42 deliverable.

## Wire format

Same as v4.41.2: the typed assembler's no-sink JSONL fallback
emits a `{"_schema":…}` header inferred from the pipeline output.
Local-side generated Go pipes the remote's stdout through the
existing `readJSONSchemaAware → writeWithInferredSchema` chain so
the wire format is byte-identical to today's baseline pushdown.

## Effort

About a day, including tests and docs:

- ~30 lines: drop `-remote`, `-show-remote-source`,
  `executeFromSSHRemoteGo`
- ~80 lines: rewrite `generateFromSSHRemoteCode` to emit
  ship-and-run Go (including the inline runRemote helper as a
  string-constant template)
- ~30 lines: tests (new pattern: invoke `ssql generate go -run`
  with SSQLGO=typed and `from ssh -- pushdown`, verify the
  generated binary ships+runs against an LXD test container)
- ~20 lines: doc updates (cli-codelab `from ssh` section,
  TODO.md)

## What this unlocks

- **One artifact for remote work.** A v4.42 user generates a Go
  binary, scp's it to a CI runner or jump host, and runs it.
  Pipeline + remote orchestration baked in. No dependency
  management on the deploy target (only on the SSH-target hosts,
  same as `--` pushdown today).
- **Composable with the optimiser.** `(SSQLGO=typed; ssql from
  ssh … -- where … + group-by …) | generate go -optimise -run`
  applies the pipeline optimiser locally before shipping. The
  remote sees the optimised pipeline.
- **The "distributed Go pipelines with stock SSH" story.** Once
  the catalog extension lands, you have parallel typed-Go
  pipelines across N hosts orchestrated by a single Go binary
  generated from one ssql command line. No daemon, no service
  registry, no service mesh — just SSH.

## See also

- `codegen-wrapper-proposal.md` — `-script PATH` and
  `-shell-helpers` (shipped v4.41.0)
- `distributed-ssh-processing.md` — original SSH proposal (shipped v4.27.0)
- `distributed-shard-catalog.md` — multi-shard catalog (shipped v4.27.0)
- `typed-auto-parallel-proposal.md` — Phase A planner (shipped v4.39.0)
- `mixed-mode-pipelines-proposal.md` — Phase B mixed mode (shipped v4.40.0)
- `ssh-test-environment.md` — LXD containers for SSH testing
