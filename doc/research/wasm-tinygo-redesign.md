# WASM Module Redesign: TinyGo + Decoupled Architecture

Reference: DFC047
Created: 2026-02-17
Last modified: 2026-02-17

[Back to Index](./README.md)

## Problem

The current `ssql.wasm` is **19MB raw (~4MB gzipped)**. This makes the `to explore -wasm` feature impractical for most deployments — users wait several seconds to download and instantiate the module before they can interact with data.

The root cause: `cmd/ssql-wasm` imports `github.com/rosscartlidge/ssql/v4`, which transitively pulls in the entire dependency tree:

- Apache Arrow (arrow/go/v18) — IPC, flatbuffers, zstd, lz4
- excelize (xuri/excelize/v2) — Excel support, mscfb, XML
- expr-lang — expression compiler and VM
- goccy/go-json — fast JSON library
- html/template — chart HTML generation
- crypto/\*, encoding/xml — transitive dependencies

The WASM module uses **6 operations** (where, sort, groupby, distinct, limit, pipeline) against JSON arrays. It needs none of these heavyweight dependencies.

## WASM GC Assessment

**WASM GC (the WebAssembly Garbage Collection proposal) is not viable for this project.**

WASM GC is now in all major browsers (Chrome 119+, Firefox 120+, Safari 18.2+) and is part of the Wasm 3.0 spec. It eliminates the need to ship a GC runtime by using the browser's native GC. Languages like Kotlin/Wasm and Dart use it to produce small, fast WASM modules.

However, **neither Go nor TinyGo can target WASM GC**, and there's no timeline for support:

- **Interior pointer problem**: Go relies on pointers into the middle of structs (`&s.field`). WASM GC doesn't support interior pointers — every such field would need boxing, destroying performance.
- **Go team assessment** (golang/go#63904): "an enormous amount of work, almost comparable to writing an entirely separate compiler"
- **Spec-level blocker** (WebAssembly/gc#59): Closed as "not actionable for MVP" — interior pointer support would require a post-MVP proposal that doesn't exist yet.
- **TinyGo**: No WASM GC target exists (`-target wasm` produces traditional linear-memory WASM only).

**Conclusion**: WASM GC would require rewriting the module in Kotlin or Dart. The operations are simple enough that this is technically feasible, but it introduces a second language into the build. Not worth it when TinyGo can get us to ~200KB.

## Proposed Architecture

### Core Idea

Rewrite `cmd/ssql-wasm` to be **completely self-contained** — no `ssql/v4` import. Implement the 6 operations directly against a lightweight `schema` + `[]any` representation, using only stdlib packages that TinyGo handles well.

### Internal Types

```go
// schema holds field names and a lookup index, shared across records.
// Equivalent to ssql.Schema but without generics or reflect.
type schema struct {
    fields []string         // ordered field names
    index  map[string]int   // field name → position in values slice
}

// record is a single data row. The schema is shared across all records
// in a dataset, so only the values slice is per-record.
type record struct {
    s      *schema
    values []any    // parallel to s.fields
}

// dataset is a complete table: one schema, many rows.
type dataset struct {
    s    *schema
    rows []record
}
```

This mirrors ssql/v4's `Schema` + `Record` pattern but without generics, `iter.Seq`, or reflect. Field access is a map lookup for the index, then a slice index:

```go
func (r record) get(field string) (any, bool) {
    i, ok := r.s.index[field]
    if !ok {
        return nil, false
    }
    return r.values[i], true
}
```

### JSON Parsing (No encoding/json for data)

TinyGo's `encoding/json` compiles but fails at runtime for reflection-heavy operations. We avoid it for data parsing by writing a minimal JSON array parser:

1. The JS wrapper already calls `JSON.stringify(data)` — we receive a JSON array of objects
2. Parse the first `{...}` object to extract field names → build `schema`
3. Parse subsequent objects by field-name matching into `[]any` slices sharing the same schema
4. Value types: `string`, `float64`, `bool`, `nil` (same as JSON)

```go
// parseJSONArray parses a JSON array of objects into a dataset.
// First object defines the schema; subsequent objects reuse it.
func parseJSONArray(data string) (dataset, error) {
    // ... minimal JSON tokenizer
    // Returns dataset with shared schema
}
```

For output, build JSON manually with `strconv` and string concatenation — no `json.Marshal`:

```go
// toJSON converts a dataset back to a JSON array string.
func (ds dataset) toJSON() string {
    var buf strings.Builder
    buf.WriteByte('[')
    for i, row := range ds.rows {
        if i > 0 {
            buf.WriteByte(',')
        }
        buf.WriteByte('{')
        for j, field := range ds.s.fields {
            if j > 0 {
                buf.WriteByte(',')
            }
            buf.WriteByte('"')
            buf.WriteString(field)  // fields are safe (came from JSON keys)
            buf.WriteString(`":`)
            appendJSONValue(&buf, row.values[j])
        }
        buf.WriteByte('}')
    }
    buf.WriteByte(']')
    return buf.String()
}
```

We still use `encoding/json` for two small things where it works fine in TinyGo:
- Parsing the pipeline operation descriptors (small, fixed-structure objects)
- Formatting error responses

### Operations

All 6 operations work on `dataset` / `[]record` directly. No generics, no iterators.

**where** — Filter rows matching a predicate:
```go
func (ds dataset) where(field, op, value string) dataset {
    var result []record
    for _, row := range ds.rows {
        if applyOperator(row, field, op, value) {
            result = append(result, row)
        }
    }
    return dataset{s: ds.s, rows: result}
}
```

**sort** — Sort by field value:
```go
func (ds dataset) sort(field string, desc bool) dataset {
    sorted := make([]record, len(ds.rows))
    copy(sorted, ds.rows)
    slices.SortFunc(sorted, func(a, b record) int {
        // numeric-first comparison, fall back to string
    })
    return dataset{s: ds.s, rows: sorted}
}
```
Note: `slices.SortFunc` — TinyGo supports the `slices` package. Alternatively, use `sort.Slice`.

**groupBy** — Group by field, aggregate another field:
```go
func (ds dataset) groupBy(groupField, aggField, aggFunc string) dataset {
    groups := map[string][]record{}  // group key → rows
    for _, row := range ds.rows {
        key := fmt.Sprintf("%v", row.get(groupField))
        groups[key] = append(groups[key], row)
    }
    // Build result dataset with new schema: [groupField, aggFunc]
    resultSchema := newSchema([]string{groupField, aggFunc})
    var rows []record
    for key, group := range groups {
        aggValue := aggregate(group, aggField, aggFunc)
        rows = append(rows, record{
            s:      resultSchema,
            values: []any{key, aggValue},
        })
    }
    return dataset{s: resultSchema, rows: rows}
}
```

**distinct** — Deduplicate by field:
```go
func (ds dataset) distinct(field string) dataset {
    seen := map[string]bool{}
    var result []record
    for _, row := range ds.rows {
        key := fmt.Sprintf("%v", row.get(field))
        if !seen[key] {
            seen[key] = true
            result = append(result, row)
        }
    }
    return dataset{s: ds.s, rows: result}
}
```

**limit** — Pagination (offset + count):
```go
func (ds dataset) limit(n, offset int) dataset {
    start := min(offset, len(ds.rows))
    end := min(start+n, len(ds.rows))
    return dataset{s: ds.s, rows: ds.rows[start:end]}
}
```

**pipeline** — Compose operations (parsed with encoding/json since it's small fixed-structure data):
```go
func (ds dataset) pipeline(ops []operation) dataset {
    for _, op := range ops {
        switch op.Op {
        case "where":
            ds = ds.where(op.Field, op.Operator, op.Value)
        case "sort":
            ds = ds.sort(op.Field, op.Desc)
        // ... etc
        }
    }
    return ds
}
```

### syscall/js Interface (Unchanged)

The 6 global functions registered via `syscall/js` keep exactly the same signatures:

```
ssqlWhere(jsonData, field, op, value)     → JSON string
ssqlSort(jsonData, field, descending)     → JSON string
ssqlGroupBy(jsonData, groupField, aggField, aggFunc) → JSON string
ssqlDistinct(jsonData, field)             → JSON string
ssqlLimit(jsonData, n, offset)            → JSON string
ssqlPipeline(jsonData, pipelineJSON)      → JSON string
ssqlReady = true
```

The `main()` function is identical to today:

```go
func main() {
    js.Global().Set("ssqlWhere", js.FuncOf(jsWhere))
    js.Global().Set("ssqlSort", js.FuncOf(jsSort))
    // ... same as current
    js.Global().Set("ssqlReady", true)
    select {}
}
```

### JavaScript API (Unchanged)

The `SsqlWasm` class in `ssql-wasm.js` does **not change at all**. It already communicates via JSON strings through the global functions. The explorer HTML template doesn't change either — same `ssqlWasm.where()`, `ssqlWasm.sort()`, etc.

The only change visible to the explorer is the `wasm_exec.js` file — TinyGo ships its own version. This file is already inlined in the HTML template, so the swap is transparent.

### File Structure

```
cmd/ssql-wasm/
├── main.go              # syscall/js registration (mostly unchanged)
├── dataset.go           # schema, record, dataset types
├── json.go              # JSON parser and serializer (manual, no encoding/json for data)
├── operators.go         # where, sort, groupby, distinct, limit, pipeline
├── compare.go           # comparison operators (eq, gt, contains, regex, etc.)
├── js/
│   ├── ssql-wasm.js     # JS wrapper class (UNCHANGED)
│   └── wasm_exec.js     # TinyGo's version (replaces Go's version)
└── ssql.wasm            # Built artifact (~200-500KB)
```

### Build Process

```makefile
wasm:
	@echo "Building ssql.wasm with TinyGo..."
	tinygo build -o cmd/ssql-wasm/ssql.wasm \
		-target wasm \
		-no-debug \
		-panic=trap \
		-opt=z \
		./cmd/ssql-wasm
	cp $$(tinygo env TINYGOROOT)/targets/wasm_exec.js cmd/ssql-wasm/js/
	@ls -lh cmd/ssql-wasm/ssql.wasm
	@echo "Done."
```

TinyGo optimization flags:
- `-target wasm` — browser WASM (not WASI)
- `-no-debug` — strip DWARF debug info
- `-panic=trap` — replace panic strings with trap instruction (smaller binary, avoids WASI fd_write leak)
- `-opt=z` — optimize for size

We keep `-gc=conservative` (default) rather than `-gc=leaking` because the module is long-lived in the browser — it stays alive for the entire explorer session.

### Fallback: Standard Go (if TinyGo has issues)

If TinyGo produces unexpected issues (e.g., `syscall/js` edge cases, `regexp` bugs), we can build the same decoupled code with standard Go:

```makefile
wasm-go:
	GOOS=js GOARCH=wasm go build -ldflags="-s -w" \
		-o cmd/ssql-wasm/ssql.wasm ./cmd/ssql-wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" cmd/ssql-wasm/js/
```

This still benefits from decoupling — the binary drops from 19MB to ~5MB because there are no heavyweight dependencies. TinyGo is an optimization on top.

### Expected Sizes

| Approach | Raw | Gzipped | Brotli |
|----------|-----|---------|--------|
| Current (imports ssql/v4, std Go) | 19 MB | ~4 MB | ~3 MB |
| Decoupled, standard Go | ~5 MB | ~1.5 MB | ~1 MB |
| Decoupled, TinyGo | ~300-500 KB | ~100-200 KB | ~80-150 KB |

The TinyGo estimate is based on: `strconv` + `strings` + `sort` + `regexp` + `fmt` + `syscall/js` with size optimizations. The stdlib-only TinyGo WASM baseline is ~100-300KB for programs with these imports.

### Migration Steps

1. **Create the decoupled module** — new files in `cmd/ssql-wasm/` replacing the existing ones
2. **Write the JSON parser** — minimal tokenizer for `[{...}, {...}]` arrays
3. **Port the 6 operations** — rewrite against `dataset` type (logic is already in current `operators.go`, just change from `ssql.Record` to `record`)
4. **Test with standard Go first** — verify identical behavior with the explorer using `GOOS=js GOARCH=wasm go build`
5. **Switch to TinyGo** — change the Makefile target, swap `wasm_exec.js`
6. **Test in browser** — verify explorer still works, check for TinyGo-specific issues
7. **Update `to explore` command** — inline the smaller `wasm_exec.js` and `ssql-wasm.js` as before; the WASM file is still copied alongside the HTML

### What Doesn't Change

- `ssql-wasm.js` (the JS wrapper class) — identical API
- Explorer HTML template integration — same `SsqlWasm` class, same init pattern
- The `-wasm` flag on `to explore` — same usage
- The 6 operations' behavior — same filtering, sorting, grouping semantics
- The `ssqlReady` global flag pattern
- Error response format (`{"error": "..."}`)

### Risks

1. **TinyGo `syscall/js` edge cases** — historical issues with `js.finalizeRef` and callback GC. Mitigation: test thoroughly; fall back to standard Go build if needed.
2. **TinyGo `regexp` correctness** — tests pass but TinyGo's regexp may have edge cases. Mitigation: the `regex` operator is rarely used in explorer; basic patterns work fine.
3. **Custom JSON parser bugs** — hand-rolled parsers are error-prone. Mitigation: extensive test cases covering nested strings, escaped quotes, unicode, null values, mixed types. Consider using a well-tested TinyGo-compatible JSON library if one exists.
4. **`fmt.Sprintf` in TinyGo** — works for basic formatting but reflection-heavy `%v` on complex types may fail. Mitigation: only use `%v` on primitive types (string, float64, int64, bool, nil), which TinyGo handles correctly.
5. **TinyGo memory leaks** — known issue (#4704) for long-running browser WASM. Mitigation: the explorer processes bounded datasets; monitor memory in devtools during testing.
