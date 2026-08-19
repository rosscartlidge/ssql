# ssql serve — Browser UI with Native Backend

Reference: DFC079
Created: 2026-04-09
Last modified: 2026-08-19

[Back to Index](./README.md)

**Status (rev 2, 2026-05-13):** The original rev-1 design (this whole doc below)
sketched an HTTP + WebSocket + browser-UI daemon. v4.44.0 shipped a **different
shape under the same name**: an SSH-accessible CLI operator console
(autocli-shell stack — see `autocli-shell-proposal.md`). The two are
complementary, not competing — rev 2 reframes them as **two protocols sharing
one daemon**.

## Status at end of week 20 (2026-05-13)

| Variant | Shipped | Module | Audience | Notes |
| --- | --- | --- | --- | --- |
| **SSH-CLI** | ✅ v4.44.0 | `cmd/ssql/commands/serve.go` | power users at a terminal | `status` / `schema` / `count` / `head` against in-memory data; pubkey auth; multi-user sessions |
| **HTTP+WebSocket+UI** | 🔶 transport shipped (Phase 2a, 2026-08-19 — `-listen-http`, /api/execute + /api/cursor + /api/files + /api/health; see DFC108 and `cmd/ssql/commands/serve_http.go`); UI is Phase 2b | browser users wanting charts + visual exploration | executes via per-stage self-exec (exec-lane semantics), NOT `cli.ExecuteWith` — rev-1's open question resolved as its option 2, processes instead of io.Pipe |

### Dual-protocol design (rev 2)

One process loads the dataset once and exposes it via *two* listeners:

```
                  ssql serve data.csv
                  -listen-ssh :2222
                  -listen-http :8080
                          │
                ┌─────────┴─────────┐
                │   serveState      │   ← single in-memory dataset
                │   (loaded once)   │     shared across all sessions
                │                   │
                │  records []Record │
                │  schema  []string │
                │  …                │
                └─────────┬─────────┘
                          │
              ┌───────────┴───────────┐
              ▼                       ▼
     ┌──────────────┐         ┌──────────────────┐
     │ autocli/ssh  │         │ net/http + WS    │  ← Phase 2
     │ (Phase 1 ✅) │         │ + embedded UI    │
     └──────┬───────┘         └────────┬─────────┘
            │                          │
   ssh -p 2222 user@host       http://host:8080
   `status`, `schema`, …        pipeline editor + charts
```

Both drivers run the same autocli `Command` tree under the hood — the SSH side
runs it via `autocli/shell` reading from the SSH channel, the HTTP+WS side
parses incoming pipeline strings, dispatches via `cli.ExecuteWith` with a
buffered `Stdout`, and streams the resulting JSONL down the WebSocket. Tab
completion in the browser editor goes through the same `cli.Complete` API
already exercised by the SSH path.

### What Phase 1 (shipped) provides as foundation for Phase 2

- `serveState` loaded once at startup, shared across sessions
- `ssql.Record` materialised in memory — no per-query startup cost
- Per-session `Context.State` plumbing → handlers reach the dataset cleanly
- `cli.Complete(line, pos)` → ready-to-use completion engine for the browser
  editor
- `cli.ExecuteWith(args, ctx)` → ready-to-use dispatch with arbitrary
  `io.Writer` sinks (a `bytes.Buffer` per WebSocket message instead of
  `os.Stdout`)
- Subcommand tree (`status` / `schema` / `count` / `head`) → already exists,
  works against the cache, just needs to be exposed through the second
  protocol

### What Phase 2 adds (the original rev-1 design, scoped fresh)

- `-listen-http :8080` flag alongside `-listen :2222`
- `net/http` server with embedded static UI (`//go:embed`)
- WebSocket `/ws` endpoint multiplexing `execute` / `complete` / `cancel`
  messages per the rev-1 wire format
- REST endpoints (`/api/execute`, `/api/complete`, `/api/files`, `/api/schema/:file`,
  `/api/health`) per rev-1
- Browser UI based on existing `cmd/ssql-playground/playground.html` —
  swap the WASM execution path for WebSocket dispatch
- `to chart` and `to explore` HTML output captured and rendered in the UI
- localhost-bind default, optional `-token` auth for remote mode
- `-readonly` flag for write-protected deployments

### Implementation note for Phase 2

The handlers are the easy part — `cli.ExecuteWith` already does the right thing.
The work is HTTP routing, WebSocket framing, browser-UI plumbing, and
streaming-JSONL-to-records bridge for the editor's results pane. Estimated
effort matches the rev-1 plan (~1 week for core server, ~1 week for UI
polish), but shorter than rev-1 thought because the dataset-cache layer is
already done in Phase 1.

### Open question for Phase 2

The rev-1 design talks about pipelines being submitted as **single strings**
(`"ssql from data.csv | ssql where -if age gt 25 | ssql to table"`). The
SSH-CLI today exposes **discrete subcommands** (`status`, `head`, …) instead.
For Phase 2 to give browser users a real pipeline editor we need one of:

1. **Pipe support in autocli/shell** (Position 2 of the autocli-shell
   proposal). Implement once, both protocols benefit.
2. **Strip the `ssql ` prefix and split on `|`** server-side, dispatching each
   stage separately and threading `io.Pipe` between them. Works without
   touching autocli/shell.
3. **In-process composition (Position 3)** — bigger lift, eventual end state,
   defer until perf is a concrete pain.

Option 1 is the cleanest and aligns with the SSH-CLI getting pipes too. The
autocli-shell proposal already reserves the grammar and design.

---

(The rev-1 design below is preserved verbatim. Treat it as the Phase-2
design document; the dataset-cache and authentication concerns it raised are
already solved by Phase 1.)

## Problem

The WASM playground is useful for demos but fundamentally limited: no real filesystem, no SSH, no GPU, simulated pipes. The WebVM terminal has real Linux but boots slowly and can't access local files.

Users want an interactive, visual way to explore data that has:
- Real filesystem access (local files, not just uploads)
- Full ssql performance (native, not WASI)
- SSH pushdown to remote hosts
- Charts and visualizations rendered in the browser
- Tab completion and pipeline editing

## Proposed Architecture

### Two-tier design

```
┌─────────────────────────────────────────────────┐
│                  Browser UI                      │
│  ┌───────────┐  ┌──────────┐  ┌──────────────┐ │
│  │ Pipeline   │  │ Results  │  │ Charts /     │ │
│  │ Editor     │  │ Table    │  │ Explore      │ │
│  └─────┬─────┘  └────▲─────┘  └──────▲───────┘ │
│        │              │               │          │
│    ────┴──────────────┴───────────────┴────      │
│              WebSocket / HTTP API                 │
└──────────────────────┬──────────────────────────┘
                       │
              ┌────────┴────────┐
              │   ssql serve    │
              │   (native Go)   │
              │                 │
              │  • Real FS      │
              │  • Real pipes   │
              │  • SSH access   │
              │  • GPU accel    │
              │  • Tab complete │
              └─────────────────┘
```

### Tier 1: Browser-only (WASI)

The existing playground enhanced with a proper WASI runtime in the browser:
- Uses wasmer-js or jco to run ssql.wasm directly (not the JS pipe simulation)
- Upload CSV/JSON files to a virtual filesystem
- All ssql commands work (except SSH, GPU, subprocess-based features)
- No server needed — fully static, hostable on GitHub Pages

This replaces the current playground with something more capable while keeping the zero-install story.

### Tier 2: Connected mode (`ssql serve`)

A new ssql command that starts an HTTP server:

```bash
ssql serve -port 8080
# Opens http://localhost:8080 in browser
```

The server:
- Accepts pipeline strings via WebSocket
- Executes them as native ssql (full performance, real filesystem)
- Streams results back as JSONL
- Serves the UI as embedded static files
- Handles tab completion requests (field names, operators, values)

```bash
# With SSH access to remote hosts
ssql serve -port 8080 -dir /data
# Browser can now read /data/*, SSH to configured hosts, use GPU

# With directory restriction for security
ssql serve -port 8080 -dir /data -readonly
```

### Tier 3: Remote server mode

`ssql serve` on a remote machine, accessed via SSH tunnel or direct:

```bash
# On the data server
ssql serve -port 8080 -dir /data

# From your laptop
ssh -L 8080:localhost:8080 dataserver
# Open http://localhost:8080 — full access to remote /data/
```

Or with Tailscale, access directly. The browser UI talks to the remote ssql serve, which has direct access to the data and SSH keys for catalog queries.

## API Design

### WebSocket protocol

```json
// Client → Server: execute pipeline
{"type": "execute", "pipeline": "ssql from data.csv | ssql where -if age gt 25 | ssql to table"}

// Server → Client: streaming results
{"type": "schema", "fields": ["name", "age"], "types": {"name": "string", "age": "int"}}
{"type": "record", "data": {"name": "Alice", "age": 35}}
{"type": "record", "data": {"name": "Bob", "age": 42}}
{"type": "done", "count": 2, "elapsed": "0.003s"}

// Client → Server: tab completion
{"type": "complete", "pipeline": "ssql from data.csv | ssql where -if ", "cursor": 45}

// Server → Client: completions
{"type": "completions", "items": ["age", "name", "dept", "salary"]}

// Client → Server: cancel
{"type": "cancel"}

// Server → Client: chart HTML
{"type": "chart", "html": "<html>...Chart.js...</html>"}
```

### REST endpoints (for simpler integration)

```
POST /api/execute    — run pipeline, return JSONL
POST /api/complete   — tab completion
GET  /api/files      — list available files in -dir
GET  /api/schema/:file — read schema (headers) from a file
GET  /api/health     — server status
```

## Browser UI

Reuse and enhance the existing playground HTML:
- Pipeline editor with syntax highlighting (Prism.js, already integrated)
- Results table with sorting and filtering
- Chart rendering (already works — Chart.js iframe)
- Explore mode (already exists — `to explore`)
- File browser sidebar showing available data files
- Tab completion in the editor (via WebSocket to server)
- Pipeline history (localStorage)
- Share links (URL fragment encoding, already on TODO)

### What changes from the current playground

| Feature | Current Playground | ssql serve |
|---------|-------------------|------------|
| Execution | WASM in browser | Native on server |
| Filesystem | Virtual (uploaded files) | Real filesystem |
| SSH | Not available | Full SSH pushdown |
| GPU | Not available | Available |
| Performance | WASI speed (~4x slower) | Native speed |
| Tab completion | Limited (pre-cached fields) | Full (live field extraction) |
| Installation | None (static page) | `ssql serve` on host |
| Charts | Works | Works |
| to explore | Works | Works |

## Implementation Plan

### Phase 1: Core server (1 week)

1. New `ssql serve` command with `-port` and `-dir` flags
2. Embed the playground HTML as static files (`//go:embed`)
3. WebSocket handler that executes pipelines via `exec.Command`
4. Stream JSONL results back over WebSocket
5. Serve at `http://localhost:8080`

### Phase 2: Enhanced UI (1 week)

6. File browser sidebar (lists files in `-dir`)
7. Tab completion via WebSocket
8. Pipeline history in localStorage
9. Better results table (sortable columns, row count)

### Phase 3: Polish (days)

10. Security: `-readonly` flag, directory sandboxing
11. `to chart` and `to explore` output detection and rendering
12. Connection status indicator
13. Auto-open browser on start

## Security Considerations

- **Default: localhost only** — bind to `127.0.0.1`, not `0.0.0.0`
- **`-dir` flag** — restrict filesystem access to a specific directory
- **`-readonly`** — prevent `to csv`, `to parquet` etc. from writing files
- **No auth by default** — it's a local tool, like Jupyter. Add optional token auth for remote mode.
- **Command injection** — the server parses the pipeline string and executes via `ssql` commands, not raw shell. But need to be careful with `from command` which runs arbitrary commands.

## Why not just a terminal?

Terminals are great for experts. But:
- **Charts need a browser** — `to chart` produces HTML that can't render in a terminal
- **`to explore`** produces an interactive data explorer — needs a browser
- **Discoverability** — a visual file browser and tab completion UI is more approachable
- **Sharing** — URL-encoded pipelines are easier to share than shell commands
- **Mobile** — browser works on phones, terminals don't

## Relationship to existing tools

- **Replaces the current WASM playground** for local use (better performance, real files)
- **Complements the WASM playground** for zero-install demos (keep the static version for GitHub Pages)
- **Alternative to WebVM terminal** for interactive use (faster, no boot time, native speed)
- **Similar in spirit to:** Jupyter, DBeaver, pgAdmin — but for Unix pipeline data processing
