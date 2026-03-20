# Distributed Processing via SSH

**Status:** Implemented (v4.27.0)
**Date:** February 2026
**ssql version:** v4.15.0+

## Problem Statement

Data processing pipelines break down when data is remote:

```bash
# Today: download 50GB, then process locally
scp server:/data/logs.csv .
ssql from logs.csv | ssql where -if status eq error | ssql group-by -field service -count

# Tomorrow: process where the data lives, stream 200 bytes of results
ssql from ssh server /data/logs.csv | ssql where -if status eq error | ssql group-by -field service -count
```

Moving data is slow, expensive, and often unnecessary. Most pipelines reduce — filtering, aggregating, grouping — so the result is orders of magnitude smaller than the input. The ideal architecture processes data where it lives and streams only the reduced result back to the local machine.

This is especially true for GPU-accelerated workloads: the machine with the GPU has the data (or should), and the machine requesting the analysis probably doesn't have a GPU at all.

## SSH as Transport

SSH is the right choice for this because it's already everywhere:

- **Authentication is solved.** SSH keys, agents, `~/.ssh/config` — users already have this working.
- **Encryption is solved.** Everything over the wire is encrypted by default.
- **No new infrastructure.** No daemons, no ports to open, no service discovery, no certificates to manage.
- **Firewall-friendly.** Port 22 is open on virtually every server.
- **Multiplexing exists.** `ControlMaster` reuses connections for zero-latency follow-up commands.
- **It composes.** Jump hosts, tunnels, proxying — all handled by SSH config, invisible to ssql.

The alternative — a dedicated ssql daemon with gRPC or HTTP — adds operational complexity for marginal benefit. SSH gives us authenticated, encrypted, bidirectional streaming for free.

## What Already Works Today

ssql's `from command --` pattern already supports a degenerate form of distributed processing:

```bash
# This works RIGHT NOW — runs ssql on the remote machine, streams JSONL back
ssql from command -- ssh server 'ssql from /data/logs.csv | ssql where -if status eq error' \
  | ssql group-by -field service -count \
  | ssql to table
```

This is powerful but verbose. The proposed syntax is sugar over this pattern, with optimizations that the explicit form can't achieve.

Similarly, `join` already handles process substitution, which is structurally identical to a remote data source:

```bash
# Process substitution — subprocess as data source (works today)
ssql from local.csv | ssql join <(ssql from lookup.csv) -on id

# Remote source — structurally the same
ssql from local.csv | ssql join <(ssh server 'ssql from /data/lookup.csv') -on id
```

The pieces exist. The design is about making them seamless.

## Proposed Syntax

### Remote file references

The `from ssh` subcommand takes host and path as separate arguments (autocli style — no URL parsing needed):

```bash
# Read a remote file — runs ssql on the remote machine
ssql from ssh server /data/logs.csv

# With explicit user
ssql from ssh alice@analytics-box /data/sales.csv

# SSH config alias (most common in practice)
ssql from ssh prod-db /export/users.csv
```

This desugars to:

```bash
ssql from command -- ssh server 'ssql from /data/logs.csv'
```

### Pipeline push-down with `--`

When you want to run part of the pipeline on the remote machine (to reduce data transfer), add `--` after the path followed by the remote pipeline. Individual remote commands are separated by `+`:

```bash
# Simple: stream all records, filter locally
ssql from ssh server /data/logs.csv | ssql where -if status eq error

# Push-down: filter remotely, stream only matching records
ssql from ssh server /data/logs.csv -- where -if status eq error

# Multiple remote commands, separated by +
ssql from ssh server /data/logs.csv -- \
  where -if status eq error + group-by service -count cnt
```

This desugars to:
```bash
ssh server 'ssql from /data/logs.csv | ssql where -if status eq error | ssql group-by service -count cnt'
```

The split point is unambiguous: everything after `--` runs on the server; everything after the shell pipe runs locally. No structured string arguments — each piece is a plain CLI argument, and `+` is the same clause separator used elsewhere in ssql. The `--` convention is consistent with `from command --`.

### Multi-source pipelines

Remote sources in joins and unions:

```bash
# Join local data with remote lookup table
ssql from local_orders.csv \
  | ssql join <(ssql from ssh warehouse /data/products.csv) -on product_id -as name product_name

# Union data from multiple servers
ssql union \
  <(ssql from ssh server1 /data/events.csv) \
  <(ssql from ssh server2 /data/events.csv) \
  <(ssql from ssh server3 /data/events.csv) \
  | ssql group-by -field event_type -count
```

### GPU-aware remote execution

When the remote machine has `ssql_gpu`:

```bash
# Explicitly request GPU pipeline
ssql from ssh gpu-box /data/signal.csv -gpu -- fft -field amplitude

# Simple remote read, local visualization
ssql from ssh gpu-box /data/signal.csv -gpu | ssql to chart -x frequency -y magnitude
```

The `-gpu` flag tells the remote side to use `ssql_gpu` instead of `ssql`. Auto-detection is a nice-to-have: `ssh server 'which ssql_gpu'` on first connection, cached in `~/.ssh/ssql-capabilities`.

## Wire Format

JSONL with schema headers — ssql's existing inter-process format — is the wire protocol. No new format needed.

### Why JSONL schema headers are ideal

The schema header is already the first line emitted by every `from` command:

```json
{"_schema":{"fields":["name","age","dept"],"types":{"name":"string","age":"int","dept":"string"}}}
{"name":"Alice","age":30,"dept":"Engineering"}
{"name":"Bob","age":25,"dept":"Sales"}
```

This gives us:

1. **Self-describing streams.** The receiver knows field names and types before the first data record arrives.
2. **Type-safe merging.** When joining local and remote data, types are known at both ends.
3. **Streaming.** Records flow one per line — no buffering the entire dataset.
4. **Human-debuggable.** Pipe through `head` or `jq` to inspect what's flowing.
5. **Compression-friendly.** JSONL compresses well with SSH's built-in compression (`-C` flag) or zstd.

### Schema negotiation

For push-down optimization, the local side can send a "wanted fields" hint:

```json
{"_request":{"fields":["name","age"],"where":"status eq error"}}
```

The remote side can honour this (projection push-down, predicate push-down) or ignore it and send everything. The local side handles both cases — it already does, since `include` and `where` are idempotent.

### Performance envelope

On a typical SSH connection:

| Scenario | Records/sec | Bandwidth |
|----------|------------|-----------|
| LAN (1 Gbps) | ~2M | ~100 MB/s |
| WAN (100 Mbps) | ~200K | ~10 MB/s |
| WAN + SSH -C (compression) | ~500K | ~10 MB/s (compressed) |
| Arrow over SSH (future) | ~5M | ~100 MB/s |

For reduced results (post-aggregation), even WAN is fast enough — a `group-by` over 100M records might produce 50 result rows, which transfers in microseconds.

## Implementation Phases

### Phase 1: `from ssh` subcommand with `--` push-down

Add `from ssh` subcommand to the `from` command tree. Push-down is handled via `--` (same convention as `from command --`). Both simple read and push-down are handled by a single handler.

**`from ssh` (simple remote read):**
- `cmd/ssql/commands/from.go` — add `registerFromSSH()` with HOST and PATH positional args
- Desugars to: `ssh host 'ssql from /path'`
- Code generation: emit `ssql.ExecCommand("ssh", []string{host, "ssql from " + path})`

**`from ssh HOST PATH -- pipeline` (push-down):**
- Uses `--` to capture `RemainingArgs` on the `ssh` subcommand
- Splits on `+` to get individual commands
- Desugars to: `ssh host 'ssql from /path | ssql cmd1 args | ssql cmd2 args'`

```go
func registerFromSSH(cmd *cf.SubcommandBuilder) {
    cmd.Subcommand("ssh").
        Description("Read from a remote file via SSH").
        Example("ssql from ssh server /data/logs.csv | ssql to table", "Read remote CSV").
        Example("ssql from ssh server /data/logs.csv -- where -if status eq error", "Push filter to remote").
        Flag("HOST").String().Global().Help("SSH host (from ~/.ssh/config)").Done().
        Flag("PATH").String().Global().Help("Remote file path").Done().
        Flag("-gpu").Bool().Global().Help("Use ssql_gpu on the remote machine").Done().
        Flag("-generate", "-g").Bool().Global().Help("Generate Go code").Done().
        Handler(func(ctx *cf.Context) error {
            // If RemainingArgs present (after --), it's a push-down pipeline
            // Split on "+" to get individual remote commands
            ...
        }).
        Done()
}
```

**Implementation:**
- Single handler checks `ctx.RemainingArgs`: empty = simple read, non-empty = push-down
- Push-down: split `RemainingArgs` on `+`, construct `ssh host 'ssql from /path | ssql cmd1 | ssql cmd2'`
- Uses `shellQuote()` for safe command construction

```bash
# Simple remote read
ssql from ssh server /data/logs.csv
# → ssh server 'ssql from /data/logs.csv'

# Push-down pipeline via --
ssql from ssh server /data/logs.csv -- \
  where -if status eq error + group-by -field service -count
# → ssh server 'ssql from /data/logs.csv | ssql where -if status eq error | ssql group-by -field service -count'
```

**Effort:** Small-medium. Two handlers + SSH command construction + code generation.

### Phase 2: Remote source in join/union

Enable `from ssh` as a source in joins and unions via process substitution.

**For join:**
```bash
ssql from local.csv \
  | ssql join <(ssql from ssh warehouse /data/products.csv) -on product_id
```

This is structurally identical to how join already handles process substitution — the right-side records come from a subprocess instead of a file.

**For union:**
```bash
# Each source can be local or remote
ssql union \
  <(ssql from ssh server1 /data/part1.csv) \
  <(ssql from local_part2.csv) \
  | ssql to json
```

This already works with the `from ssh` subcommand, since `<(ssql from ssh ...)` is just process substitution.

### Phase 3: Code generation for remote pipelines

Extend `generate-go` to handle remote fragments.

**For simple remote reads:**
```go
// Generated code for: ssql from ssh server /data/logs.csv
records, err := ssql.ExecCommand("ssh", []string{"server", "ssql from /data/logs.csv"})
if err != nil {
    fmt.Fprintf(os.Stderr, "error: %v\n", err)
    os.Exit(1)
}
```

**For push-down pipelines:**
```go
// Generated code for: ssql from ssh server /data/logs.csv -- where -if status eq error
records, err := ssql.ExecCommand("ssh", []string{
    "server",
    "ssql from /data/logs.csv | ssql where -if status eq error",
})
```

This is straightforward because `ExecCommand` already exists and handles subprocess output parsing. The generated code is a faithful reproduction of the SSH pipeline.

### Phase 4: SSH connection management

Optimize repeated connections (e.g., join reading a remote lookup table while streaming remote primary data).

**Approach:** Leverage SSH `ControlMaster` multiplexing — no ssql code needed.

```bash
# User's ~/.ssh/config:
Host analytics-*
    ControlMaster auto
    ControlPath ~/.ssh/cm-%r@%h:%p
    ControlPersist 60
```

With this config, the first `ssh` spawns a master connection; subsequent `ssh` commands to the same host reuse it with near-zero latency. ssql doesn't need to manage connections — SSH already handles this.

For ssql to *hint* at this:
- Document the recommended SSH config in CLI help
- Optionally: `ssql ssh-setup` command that configures ControlMaster for a given host

## GPU Awareness

Remote GPU processing extends naturally from the syntax:

```bash
# Remote FFT on GPU server with push-down
ssql from ssh gpu-server /data/signal.wav -gpu -- fft -field amplitude \
  | ssql to chart -x frequency -y magnitude
```

Internally:
```bash
ssh gpu-server 'ssql_gpu from /data/signal.wav | ssql_gpu fft -field amplitude'
```

### Capability detection

First time connecting to a remote host, probe for capabilities:

```bash
ssh server 'ssql version 2>/dev/null; ssql_gpu version 2>/dev/null'
```

Cache results in `~/.cache/ssql/hosts.json`:

```json
{
  "gpu-server": {
    "ssql": "4.15.0",
    "ssql_gpu": "4.15.0",
    "probed": "2026-02-13T10:00:00Z"
  },
  "analytics-box": {
    "ssql": "4.15.0",
    "ssql_gpu": null,
    "probed": "2026-02-13T09:30:00Z"
  }
}
```

**Auto-selection logic:**
1. If `-gpu` flag: use `ssql_gpu`, fail if not available
2. If command benefits from GPU (fft, convolve): use `ssql_gpu` if available
3. Otherwise: use `ssql`

## Mixed Pipeline Patterns

### Pattern 1: Remote filter, local presentation

The most common case. Filter/aggregate on the server, visualize locally.

```bash
ssql from ssh prod /data/access_log.csv -- \
  where -if response_code ge 500 + group-by -field endpoint -count \
  | ssql sort -field count -desc \
  | ssql to chart -x endpoint -y count -type bar
```

Data flow:
```
[prod server]                          [local machine]
access_log.csv (50GB)
  → where response_code >= 500
  → group-by endpoint, count
  → JSONL (50 rows, ~2KB)  ──SSH──→  sort, chart
                                       → output.html
```

### Pattern 2: Fan-out aggregation

Process the same dataset with different aggregations in parallel.

```bash
# Three aggregations, one SSH connection (with ControlMaster)
ssql union \
  <(ssql from ssh server /data/sales.csv -- group-by -field region -sum revenue) \
  <(ssql from ssh server /data/sales.csv -- group-by -field product -avg price) \
  <(ssql from ssh server /data/sales.csv -- group-by -field month -count) \
  | ssql to explore output.html
```

With SSH multiplexing, all three read from the same server over one TCP connection.

### Pattern 3: Remote enrichment (join)

Local stream enriched with remote lookup data.

```bash
ssql from local_events.csv \
  | ssql join <(ssql from ssh warehouse /data/products.csv) -on product_id -as name product_name \
  | ssql to table
```

The join command fetches the remote lookup table once, builds an index, then matches against the local stream. The lookup table travels over SSH; the local stream never leaves.

### Pattern 4: Cross-server correlation

Join data from two different servers.

```bash
ssql from ssh web-server /logs/access.csv -- where -if status ge 500 \
  | ssql join <(ssql from ssh db-server /logs/queries.csv -- where -if duration gt 1000) \
    -on request_id \
  | ssql to table
```

Both servers filter locally; only matching records travel to the local machine for the join.

### Pattern 5: GPU processing with local visualization

Offload compute to GPU server, visualize locally.

```bash
# FFT on GPU, stream frequency data locally, visualize locally
ssql from ssh gpu-box /data/audio.wav -gpu -- fft -field amplitude \
  | ssql to animate -frame segment -x freq -y time -z magnitude
```

The FFT runs on the GPU. The frequency-domain data (much smaller than raw audio) streams back for local visualization.

## Code Generation for Remote Pipelines

Code generation (`SSQLGO=1 ... | ssql generate-go`) handles remote pipelines by generating `ExecCommand("ssh", ...)` calls.

### Simple remote read

```bash
SSQLGO=1 ssql from ssh server /data/logs.csv | ssql where -if status eq error | ssql generate-go
```

Generated:

```go
package main

import (
    "fmt"
    "os"

    ssql "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // ssql from ssh server /data/logs.csv
    records, err := ssql.ExecCommand("ssh", []string{"server", "ssql from /data/logs.csv"})
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }

    // ssql where -if status eq error
    filtered := ssql.Where(func(r ssql.Record) bool {
        return ssql.GetOr(r, "status", "") == "error"
    })(records)

    ssql.WriteJSONFastToWriter(filtered, os.Stdout)
}
```

### With push-down

```bash
SSQLGO=1 ssql from ssh server /data/logs.csv -- where -if status eq error \
  | ssql group-by -field service -count \
  | ssql generate-go
```

Generated:

```go
package main

import (
    "fmt"
    "os"

    ssql "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // ssql from ssh server /data/logs.csv -- where -if status eq error
    records, err := ssql.ExecCommand("ssh", []string{
        "server",
        "ssql from /data/logs.csv | ssql where -if status eq error",
    })
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }

    // ssql group-by -field service -count
    grouped := ssql.GroupByFields("_group", "service")(records)
    aggregated := ssql.Aggregate("_group", map[string]ssql.AggregateFunc{
        "count": ssql.Count(),
    })(grouped)

    ssql.WriteJSONFastToWriter(aggregated, os.Stdout)
}
```

The remote portion becomes a single `ExecCommand` call — opaque to the generated program. The local portion is compiled Go. This is the right boundary: network I/O is inherently dynamic, but local processing benefits from compilation.

## Security Considerations

### No new attack surface

ssql adds zero new network surface. All communication goes through SSH:

- Authentication: SSH keys / agents (existing infrastructure)
- Encryption: SSH transport layer (existing infrastructure)
- Authorization: SSH `authorized_keys`, `Match` blocks, `ForceCommand` (existing infrastructure)
- Auditing: SSH logs (existing infrastructure)

### Command injection prevention

The remote command string is constructed by ssql, not user input. However, filenames come from users:

```go
// SAFE: shellQuote escapes the path
cmd := fmt.Sprintf("ssql from %s", shellQuote(remotePath))

// UNSAFE: direct interpolation (never do this)
cmd := fmt.Sprintf("ssql from %s", remotePath)
```

`shellQuote` (already in `cmd/ssql/helpers.go`) handles this. The same function is used for code generation.

### Restricted remote execution

For environments that want to limit what ssql can do remotely, SSH's `ForceCommand` or a restricted shell works:

```bash
# ~/.ssh/authorized_keys on server:
command="/usr/bin/ssql-restricted $SSH_ORIGINAL_COMMAND" ssh-rsa AAAA...

# ssql-restricted validates the command is a read-only ssql pipeline
```

This is standard SSH operational practice — ssql doesn't need to implement access control.

### Secret management

No secrets are managed by ssql. SSH agent forwarding, key files, and SSH config are the user's responsibility. ssql never sees credentials.

## Comparison with Alternatives

| Approach | Pros | Cons |
|----------|------|------|
| **SSH (proposed)** | Zero infrastructure, existing auth, encrypted, composable | Requires ssql on remote, SSH overhead per connection |
| **ssql daemon (gRPC)** | Persistent connection, potential query optimization | New daemon to deploy/manage, new port to open, TLS certs, auth system |
| **ssql daemon (HTTP)** | REST familiarity, easy debugging | Same as gRPC plus higher overhead, no streaming without SSE/WebSocket |
| **rsync + local** | Simple, no remote execution needed | Moves all data, slow for large datasets, no incremental processing |
| **Database (Postgres/DuckDB)** | SQL optimizer, indexes, query planning | Requires data loading, schema management, separate tool |
| **Spark/Flink** | Built for distributed processing | Massive operational overhead, JVM, cluster management |

SSH wins for ssql's use case: ad-hoc exploration of data on remote machines by individual users or small teams. The other approaches serve different audiences (databases for structured querying, Spark for cluster-scale batch processing).

## Design Principles

1. **No new infrastructure.** SSH is already there. Don't make users install daemons.
2. **Explicit is better than magic.** Users declare what runs remotely. No invisible optimization that changes semantics.
3. **Degrade gracefully.** If SSH is slow, the pipeline still works — just slower. If the remote lacks ssql, `from command --` with explicit commands still works.
4. **Compose with Unix.** `ssh`, process substitution, pipes — these are the building blocks. ssql adds convenience syntax, not a new paradigm.
5. **Generate honest code.** Code generation produces `ExecCommand("ssh", ...)` — the same thing the CLI does. No hidden complexity.

## Browser-Initiated Processing

The SSH design assumes a terminal context — a user with shell access and SSH keys. But ssql also runs in the browser: `to explore` generates self-contained HTML apps, and the WASM module executes real ssql transforms client-side. A browser cannot open SSH connections. This section addresses that gap.

### The problem

A user opens an explorer HTML file in their browser and wants to query remote data interactively — filter a 50GB dataset on a server, pull back matching rows, update the chart. The browser has no access to SSH, no access to the local filesystem, and no way to spawn subprocesses.

### Approach: Local relay via `ssql serve`

The minimal addition is a local relay process that bridges browser WebSocket to SSH:

```
[Browser]  ──WebSocket──→  [ssql serve :9090]  ──SSH──→  [Remote server]
   HTML/WASM                  localhost only               ssql on data
```

The browser talks to `localhost:9090` over WebSocket. The relay SSHs to remote machines on the browser's behalf. The user's existing SSH keys and config are used — no new auth system.

**Why WebSocket:** Bidirectional streaming. The relay pushes JSONL records as they arrive from the remote side. The browser renders incrementally. HTTP request/response would require buffering the entire result.

**Why localhost only:** The relay binds to `127.0.0.1` by default. It's not a server — it's a local bridge. No new attack surface beyond what the user already has via their terminal.

### Proposed `ssql serve` command

```bash
# Start local relay (binds to localhost:9090)
ssql serve

# Custom port
ssql serve -port 8080

# Allow specific remote hosts only
ssql serve -allow prod-server,analytics-box
```

The relay accepts WebSocket connections and exposes a simple JSON protocol:

```json
// Browser → relay: execute remote pipeline
{"action": "query", "id": "q1", "host": "prod-server", "pipeline": "from /data/logs.csv | where -if status eq error | group-by -field service -count"}

// Relay → browser: schema header
{"id": "q1", "type": "schema", "data": {"fields": ["service", "count"], "types": {"service": "string", "count": "int"}}}

// Relay → browser: result records (streamed)
{"id": "q1", "type": "record", "data": {"service": "auth", "count": 1247}}
{"id": "q1", "type": "record", "data": {"service": "api", "count": 893}}

// Relay → browser: done
{"id": "q1", "type": "done", "records": 2}
```

Internally, the relay runs: `ssh prod-server 'ssql from /data/logs.csv | ssql where -if status eq error | ssql group-by -field service -count'` and streams the JSONL output back over the WebSocket.

### Explorer integration

The `to explore` HTML app gains an optional remote data panel:

```bash
# Generate explorer with relay support
ssql from ssh server /data/sample.csv \
  | ssql to explore -relay localhost:9090 output.html
```

The generated HTML:
1. Loads the initial sample data (embedded or fetched at generation time)
2. Shows a "Remote Query" panel with a pipeline builder
3. Sends queries to `ws://localhost:9090` when the user adjusts filters
4. Streams results into the AG-Grid table and Plotly chart incrementally

Without `-relay`, the explorer works as it does today — fully self-contained, no network access needed.

### WASM + relay interaction

The WASM module handles local transforms (sort, filter on already-fetched data). The relay handles remote fetches. They compose naturally:

```
User adjusts filter in browser
  → WASM checks: can this filter run on local data? (already fetched)
    → Yes: apply locally, instant
    → No (new remote source): send query to relay
      → Relay SSHs to server, streams results
      → WASM receives records, applies any local transforms
      → UI updates incrementally
```

This keeps the common case fast (local WASM) while enabling remote queries when needed.

### Security constraints

The relay inherits the terminal user's SSH access — it can reach exactly the hosts the user can already reach. Additional safeguards:

- **Localhost binding:** Default `127.0.0.1` only. No remote access to the relay.
- **Host allowlist:** `-allow` flag restricts which remote hosts can be queried.
- **Read-only pipelines:** The relay could validate that pipelines contain only read operations (`from`, `where`, `group-by`, etc.) and reject mutations.
- **No credential storage:** SSH agent handles auth. The relay never sees private keys.
- **Origin checking:** WebSocket accepts connections only from `file://` or `localhost` origins.

### Alternative: Pre-fetch with refresh

A simpler approach that avoids the relay entirely:

```bash
# Generate explorer with a refresh script
ssql from ssh server /data/logs.csv -- where -if status eq error \
  | ssql to explore -refresh-script refresh.sh output.html
```

The `refresh.sh` script re-runs the remote pipeline and regenerates the HTML. The user clicks "Refresh" in the browser, which... can't run shell scripts. So this only works with a file watcher or manual re-run.

This is adequate for dashboards that refresh periodically but not for interactive exploration.

### Recommendation

Implement in two stages:

1. **Pre-fetch (Phase 1):** `ssql from ssh server /data/... | ssql to explore` fetches remote data at generation time. The explorer is fully self-contained. Simple, works today with Phase 1 SSH support.

2. **Live relay (Phase 6):** `ssql serve` + `-relay` flag for interactive remote querying. Only needed when datasets are too large to pre-fetch or when the user needs real-time data.

Most users will be well-served by pre-fetch. The relay is for power users who need interactive exploration of data that can't leave the server.

## Open Questions

1. **Arrow over SSH?** Binary Arrow format is 10-20x faster than JSONL. Worth adding `-format arrow` for the SSH transport? Requires Arrow support on both ends.
2. **Parallel SSH?** For fan-out patterns, should ssql manage parallel SSH sessions, or leave that to shell parallelism (`&`, `xargs -P`)?
3. **Progress reporting?** Long-running remote pipelines are silent. Should the remote side emit progress on stderr?
4. **Version mismatch?** What happens when local ssql is v4.15 but remote is v4.10? Schema headers should be compatible, but new commands won't exist.
5. **Windows/PowerShell?** SSH syntax assumes bash on the remote side. Windows remotes would need different quoting.

## Summary

Distributed processing in ssql is not a new architecture — it's a thin layer over patterns that already work:

- `from command -- ssh server 'ssql ...'` already executes remote pipelines
- JSONL schema headers already carry type information between processes
- Process substitution already treats subprocesses as data sources
- Code generation already wraps subprocesses into standalone functions

The proposal adds:
1. **`from ssh` subcommand** to make remote sources first-class (host and path as separate args, autocli style)
2. **Pipeline push-down** via `--` and `+` separators — no structured string arguments, consistent with `from command --`
3. **GPU awareness** (`-gpu` flag, auto-detect `ssql_gpu` on remote hosts)
4. **Connection optimization** (documentation + optional tooling for SSH ControlMaster)

The result: distributed processing that feels like local processing, with SSH handling all the hard parts (auth, encryption, multiplexing) that ssql shouldn't reinvent.
