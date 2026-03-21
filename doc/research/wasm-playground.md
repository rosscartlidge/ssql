# Design: ssql WASM Playground

**Status:** Design
**Date:** 2026-03-21
**Goal:** Browser-based interactive playground where users can type ssql pipelines and see results — zero install

## Overview

A static site (GitHub Pages) with a terminal-like input where users type real ssql commands. The browser executes them via WebAssembly, displaying results as tables, charts, or generated code.

The key demo: type a naive pipeline → click "Optimize" → see the rewrite → click "Generate Go" → see compiled code. All in-browser.

## Architecture

### Two WASM modules

**Module 1: `ssql-pipeline.wasm` — the full CLI pipeline engine**

A new WASM build of the *actual* ssql CLI, compiled with standard Go (`GOOS=js GOARCH=wasm`). Unlike the current `ssql-wasm` (a stripped-down TinyGo reimplementation for `to explore`), this is the real thing — same code that runs on the command line.

Binary size is ~5-8MB (gzipped ~2MB). Acceptable for a playground — users expect a brief load. The current TinyGo build optimizes for embedding in every `to explore` output; the playground loads once.

Exposes a single JS function:
```js
// ssqlExec(args, stdin) → {stdout, stderr, exitCode}
// args: ["from", "csv", "data.csv", ...]  (without the "ssql" prefix)
// stdin: string (JSONL or empty)
// Returns: {stdout: string, stderr: string, exitCode: number}
let result = ssqlExec(["where", "-if", "age", "gt", "25"], jsonlInput);
```

This is the Go `main()` function wired to read from a JS-provided buffer instead of os.Stdin, and write to a JS-captured buffer instead of os.Stdout. Every ssql command works: where, group-by, sort, join, window, generate go, generate sql, generate ssql — because it's the same binary.

**Module 2: existing `ssql-wasm` (optional, for explore widget)**

The current TinyGo module stays as-is for `to explore` output. The playground doesn't use it.

### JS Pipeline Orchestrator

The browser JS replaces bash. It:

1. Parses the pipeline string into individual commands
2. Handles `<(...)` process substitution by executing inner pipelines first
3. Executes each command sequentially via `ssqlExec()`, piping JSONL between them
4. Renders the final output (table, chart, code, raw JSONL)

```
User types:
  ssql from employees.csv | ssql where -if age gt 30 | ssql group-by dept -count cnt | ssql to table

JS orchestrator does:
  1. Parse: ["from employees.csv", "where -if age gt 30", "group-by dept -count cnt", "to table"]
  2. from employees.csv → loads virtual file, runs ssqlExec(["from", "csv", "employees.csv"], "")
     → stdout = JSONL
  3. where -if age gt 30 → ssqlExec(["where", "-if", "age", "gt", "30"], prevJsonl)
     → stdout = filtered JSONL
  4. group-by dept -count cnt → ssqlExec(["group-by", "dept", "-count", "cnt"], prevJsonl)
     → stdout = aggregated JSONL
  5. to table → render as HTML table (JS-side, or via ssqlExec)
```

### Process Substitution (`<(...)`)

The `<(...)` construct is simulated by:

1. **Parsing:** regex/parser identifies `<(...)` blocks in the pipeline
2. **Executing inner pipeline:** each `<(...)` is itself a pipeline — execute recursively
3. **Virtual file:** the inner pipeline's JSONL output is stored in a virtual filesystem entry (e.g., `/dev/procsub/1`)
4. **Substitution:** replace `<(...)` in the outer command with the virtual file path

Example:
```
ssql from orders.csv | ssql join <(ssql from customers.csv | ssql where -if country eq US) -using customer_id | ssql to table
```

JS does:
```
1. Detect <(ssql from customers.csv | ssql where -if country eq US)
2. Execute inner pipeline:
   a. ssqlExec(["from", "csv", "customers.csv"], "") → JSONL
   b. ssqlExec(["where", "-if", "country", "eq", "US"], prevJsonl) → filtered JSONL
3. Store result in virtual FS as "/dev/procsub/0"
4. Execute outer pipeline:
   a. ssqlExec(["from", "csv", "orders.csv"], "") → JSONL
   b. ssqlExec(["join", "/dev/procsub/0", "-using", "customer_id"], prevJsonl) → joined JSONL
   c. Render as table
```

Nested `<(...)` works naturally via recursion.

### Virtual Filesystem

The WASM module needs file access for `from csv FILE` and `join FILE`. Implement via:

- **Go side:** override `os.Open` / file reads to check a virtual FS map before real filesystem
- **JS side:** pre-populate the virtual FS with sample datasets and user uploads

```js
// Pre-loaded sample datasets
virtualFS.set("employees.csv", "name,age,dept,salary\nAlice,30,Eng,120000\n...");
virtualFS.set("orders.csv", "...");
virtualFS.set("customers.csv", "...");

// User can upload their own CSV
fileInput.addEventListener("change", (e) => {
    const file = e.target.files[0];
    const reader = new FileReader();
    reader.onload = () => virtualFS.set(file.name, reader.result);
    reader.readAsText(file);
});
```

For Go WASM, `syscall/js` can intercept file operations. Alternatively, use an in-memory `fs.FS` implementation and modify `ssql.ReadCSV` etc. to accept it. The cleanest approach is likely a build-tag-gated `//go:build js` file that replaces file I/O with virtual FS lookups.

### SSQLGO / Generate Mode

For `generate go`, `generate sql`, `generate ssql`, the pipeline needs `SSQLGO=1` to emit fragments. In the browser:

- The orchestrator sets `SSQLGO=1` in the WASM environment before executing each command
- Each command emits JSONL fragments to stdout (same as CLI)
- The final `generate go/sql/ssql` command reads all fragments and outputs the result
- The output is displayed in a code block with syntax highlighting

The "Optimize" button is just: re-run the pipeline with `| ssql generate ssql` appended. The "Generate Go" button appends `| ssql generate go`.

## UI Design

### Layout

```
┌─────────────────────────────────────────────────────────┐
│  ssql playground                        [Upload CSV] 📁 │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  $ ssql from employees.csv | ssql where -if age gt 30 | │
│    ssql group-by dept -count cnt | ssql to table        │
│                                                         │
│  [Run ▶]  [Optimize 🔧]  [Generate Go 📝]  [SQL 🗄️]   │
│                                                         │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌─ Output ──────────────────────────────────────────┐  │
│  │ dept          cnt                                 │  │
│  │ ──────────────────                                │  │
│  │ Engineering   42                                  │  │
│  │ Sales         28                                  │  │
│  │ Marketing     15                                  │  │
│  └───────────────────────────────────────────────────┘  │
│                                                         │
│  ┌─ Available datasets ──────────────────────────────┐  │
│  │ employees.csv (1000 rows × 8 cols)                │  │
│  │ orders.csv (5000 rows × 5 cols)                   │  │
│  │ customers.csv (500 rows × 4 cols)                 │  │
│  │ events.csv (10000 rows × 6 cols)                  │  │
│  └───────────────────────────────────────────────────┘  │
│                                                         │
│  ┌─ Example pipelines ───────────────────────────────┐  │
│  │ • Filter and aggregate                    [Try] │  │
│  │ • Join two datasets                       [Try] │  │
│  │ • Window functions (ranking)              [Try] │  │
│  │ • Optimize SSH pushdown                   [Try] │  │
│  │ • Sort+limit → top optimization           [Try] │  │
│  │ • Process substitution join               [Try] │  │
│  │ • Full: optimize → generate Go            [Try] │  │
│  └───────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### Interactive buttons

- **Run** — execute the pipeline, show results
- **Optimize** — run through `generate ssql`, show the rewritten pipeline and explain output
- **Generate Go** — run through `generate go`, show syntax-highlighted Go code
- **SQL** — run through `generate sql`, show the DuckDB SQL

### Sample datasets

Pre-loaded (embedded in the HTML or fetched on first load):

| Dataset | Rows | Columns | Purpose |
|---|---|---|---|
| employees.csv | 1,000 | name, age, dept, salary, city, hire_date, level, status | General demos |
| orders.csv | 5,000 | order_id, customer_id, product, amount, order_date | Join demos |
| customers.csv | 500 | customer_id, name, country, tier | Join right side |
| events.csv | 10,000 | timestamp, service, status_code, duration_ms, region, user_id | SSH/aggregation demos |

Small enough to be embedded (~500KB total) but large enough to demonstrate real operations.

### Example pipelines

Clickable examples that populate the input and run:

```bash
# Basic filter + aggregate
ssql from employees.csv | ssql where -if age gt 30 | ssql group-by dept -count cnt -avg salary avg_sal | ssql to table

# Join with process substitution
ssql from orders.csv | ssql join <(ssql from customers.csv | ssql where -if country eq US) -using customer_id | ssql group-by product -sum amount total | ssql sort -desc total | ssql to table

# Window functions
ssql from employees.csv | ssql window -row-number rank -partition dept -order salary -desc | ssql where -if rank le 3 | ssql to table

# Optimize: SSH pushdown (simulated — shows the rewrite)
ssql from ssh node1 /data/events.csv | ssql where -if status_code ge 500 | ssql group-by service -count cnt | ssql sort -desc cnt | ssql limit 10 | ssql to table

# Optimize then generate Go
ssql from employees.csv | ssql where -if age gt 25 | ssql where -if dept eq Engineering | ssql sort -desc salary | ssql limit 10 | ssql to table
```

For the SSH example: `from ssh` can't actually connect from the browser, but the `generate ssql` optimizer can still show the rewrite. The pipeline would run in SSQLGO mode (fragments only) and demonstrate the optimization without executing.

## Implementation Plan

### Phase 1: WASM CLI binary (~2 days)

Build the full ssql CLI as a WASM module:

1. Create `cmd/ssql-playground/main.go` — same as `cmd/ssql/main.go` but with:
   - Virtual filesystem (in-memory file map populated from JS)
   - stdin/stdout wired to JS buffers
   - `ssqlExec(args, stdin)` exported to JS
2. Build with `GOOS=js GOARCH=wasm go build`
3. Test: call `ssqlExec` from JS, verify JSONL output

Key challenge: file I/O. The ssql CSV/JSON readers use `os.Open`. Options:
- **Option A:** Use `//go:build js` to replace `os.Open` with virtual FS lookup at the ssql package level. Invasive but clean.
- **Option B:** Pre-write files to the WASM in-memory filesystem (Go's `js/wasm` runtime has a basic `fs` polyfill). Less invasive.
- **Option C:** Pipe file contents through stdin. For `from csv FILE`, the JS orchestrator reads the virtual file and passes it as stdin to `from csv` (which reads stdin when no file specified). Least invasive — works today.

Recommendation: **Option C** for Phase 1 (zero changes to ssql package), migrate to Option B if needed.

With Option C, the JS orchestrator transforms:
```
ssql from csv employees.csv → ssql from csv   (with file contents as stdin)
ssql join customers.csv -using id → ssql join /dev/stdin -using id  (pipe file as stdin... tricky for join)
```

Actually, Option C doesn't work cleanly for joins where stdin is already the left side. **Option B** is better: Go WASM's `syscall/js` already provides an in-memory filesystem. We write virtual files to it before execution:

```go
// In JS, before calling ssqlExec:
fs.writeFileSync("employees.csv", csvContent);
```

The Go WASM runtime's `fs` module (via `wasm_exec.js`) maps these to in-memory buffers that `os.Open` can read. This requires no changes to ssql code.

### Phase 2: JS orchestrator (~2 days)

1. Pipeline parser: split on `| ssql `, handle `<(...)` recursion
2. SSQLGO mode: set environment variable, collect fragments
3. Sequential execution: pipe JSONL between commands
4. Output rendering: table (HTML), chart (Chart.js), code (syntax-highlighted)

### Phase 3: UI + sample data (~2 days)

1. HTML/CSS layout (terminal aesthetic, monospace)
2. Sample datasets embedded or lazy-loaded
3. Example pipeline buttons
4. File upload for user data
5. Syntax highlighting for generated code (highlight.js or Prism)
6. Share links (encode pipeline in URL fragment)

### Phase 4: Polish + deploy (~1 day)

1. GitHub Pages deployment (static site, no backend)
2. Loading indicator while WASM initializes
3. Error display (stderr shown in red)
4. Mobile-friendly layout
5. "Copy to clipboard" for generated code
6. URL-encoded pipeline sharing: `playground.html#pipeline=ssql+from+...`

## What Works

Everything that doesn't require network or real filesystem:

| Feature | Works | Notes |
|---|---|---|
| from csv/tsv/json/jsonl | Yes | Virtual filesystem |
| where, group-by, sort, limit, offset | Yes | Core operations |
| join (file) | Yes | Right-side file from virtual FS |
| join <(...) process substitution | Yes | JS executes inner pipeline first |
| window functions | Yes | Full window support |
| include, exclude, rename, cast, update | Yes | All transformations |
| distinct, top, pivot | Yes | All analytics |
| to table | Yes | Render as HTML |
| to chart | Yes | Render with Chart.js |
| to csv/json | Yes | Show as downloadable text |
| generate go | Yes | Show syntax-highlighted code |
| generate sql | Yes | Show SQL output |
| generate ssql | Yes | Show optimized pipeline + explain |
| from ssh / from catalog | No | Network-dependent (but optimization demo works via SSQLGO fragments) |
| from command | No | No shell access |
| from arrow / from parquet | Maybe | Depends on WASM binary size; could exclude via build tags |
| from wav / fft / convolve | Maybe | Large binary; could exclude |
| to explore | No | Redundant — the playground IS the explorer |

## Relationship to Existing WASM Module

The current `cmd/ssql-wasm` module is a **separate, lightweight reimplementation** designed for embedding in `to explore` HTML output. It:
- Uses TinyGo for small binary (~500KB)
- Reimplements operators from scratch (no ssql package dependency)
- Has its own JSON/expression parser
- Exposes per-operation JS functions (ssqlWhere, ssqlSort, etc.)

The playground module is the **real CLI compiled to WASM**. It:
- Uses standard Go compiler (~5-8MB, ~2MB gzipped)
- Runs the exact same code as the command-line binary
- Supports every command and flag
- Exposes a single `ssqlExec(args, stdin)` function

Both can coexist. The explore module stays embedded in `to explore` output for lightweight interactivity. The playground module powers the full-featured website.

## Size Budget

| Component | Size (gzipped) |
|---|---|
| ssql-playground.wasm | ~2 MB |
| wasm_exec.js | ~15 KB |
| Sample datasets | ~100 KB |
| HTML + CSS + JS | ~50 KB |
| highlight.js (syntax) | ~30 KB |
| **Total** | **~2.2 MB** |

Comparable to a medium-sized web app. Loads in 1-2 seconds on broadband.

To reduce WASM size, exclude signal processing (fft, ifft, convolve, correlate, spectrogram) and format-specific code (arrow, parquet, wav, xlsx) via build tags. These aren't useful in a browser context. This could bring the WASM down to ~3-4MB uncompressed (~1.5MB gzipped).

## Open Questions

1. **WASM file I/O:** Does Go's `wasm_exec.js` filesystem polyfill support `os.Open` for reading virtual files? If not, need Option A (build-tag filesystem replacement). Test this early.

2. **Environment variables:** Can we set `SSQLGO=1` from JS before calling the Go main? The `wasm_exec.js` supports `go.env` for this.

3. **Multiple invocations:** The Go WASM module runs `main()` once. For sequential pipeline stages, we either:
   - Re-instantiate the module for each command (clean but slow)
   - Keep it running and call an exported function (fast but requires refactoring main)
   - Use Web Workers for parallel execution of `<(...)` subpipelines

4. **Chart rendering:** `to chart` generates a complete HTML file with Chart.js. In the playground, extract the chart data and render inline instead.
