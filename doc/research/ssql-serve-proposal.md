# ssql serve — Browser UI with Native Backend

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
