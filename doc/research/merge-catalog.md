# Merge with Catalog Support

Reference: DFC074
Created: 2026-04-03
Last modified: 2026-04-03

[Back to Index](./README.md)

## Problem

`ssql merge` does K-way sorted merge of pre-sorted inputs, streaming with O(K) memory. Currently it only accepts local JSONL files (or process substitutions). For distributed time-series or log data where each node holds a pre-sorted shard, you'd need to manually construct process substitutions:

```bash
ssql from ssh node1 /data/events.csv \
  | ssql merge \
    <(ssql from ssh node2 /data/events.csv) \
    <(ssql from ssh node3 /data/events.csv) \
    -by timestamp
```

This is verbose and doesn't scale to 50 nodes listed in a catalog.

## Proposed Solution

Add `-catalog` flag to `merge` that reads a catalog CSV and opens each entry as a merge source:

```bash
ssql merge -catalog shards.csv -by timestamp
```

Where `shards.csv` is:
```csv
host,path
node1,/data/events.csv
node2,/data/events.csv
node3,/data/events.csv
```

### With pushdown

Push filters to each shard before merging:

```bash
ssql merge -catalog shards.csv -by timestamp -- where -if level eq ERROR
```

Each shard runs `ssql from /data/events.csv | ssql where -if level eq ERROR` remotely, streams pre-sorted filtered results, and merge interleaves them by timestamp.

### With partition pruning

Reuse catalog's existing `-if` partition pruning:

```bash
ssql merge -catalog shards.csv -by timestamp -if date_from le 2024-03-01 -if date_to ge 2024-02-01
```

Only connects to shards whose date range overlaps the query.

### No stdin required

Unlike current `merge` which always reads stdin as the first source, catalog mode reads ALL sources from the catalog. No stdin piping needed — the command is self-contained.

## Architecture

### Data flow

```
catalog.csv ──→ parse entries ──→ prune (optional -if filters)
                                      │
                    ┌─────────────────┼────────────────────┐
                    ▼                 ▼                    ▼
              SSH node1          SSH node2            SSH node3
              ssql from ...      ssql from ...        ssql from ...
              [+ pushdown]       [+ pushdown]         [+ pushdown]
                    │                 │                    │
                    ▼                 ▼                    ▼
              JSONL stream       JSONL stream         JSONL stream
                    │                 │                    │
                    └─────────────────┼────────────────────┘
                                      ▼
                              K-way merge heap
                              (sorted by -by fields)
                                      │
                                      ▼
                                JSONL stdout
```

### Key design decisions

1. **Each SSH connection is a merge source.** The catalog entry's host+path becomes a `ssql from` command run over SSH. The JSONL output is fed into the merge heap as one of K sources.

2. **Reuse existing infrastructure.** `ReadCatalog()`, `PruneCatalog()`, `BuildRemoteCommand()`, `SplitOnPlus()` all exist. The new code mainly wires catalog entries into `MergeSorted()` instead of simple concatenation.

3. **Local entries work too.** Entries with `host=local` or `host=localhost` run via `bash -c` (same as `ProcessCatalogShards`). Mixed local+remote catalogs work.

4. **Streaming, O(K) memory.** Unlike `from catalog` which concatenates (each shard fully before next), merge interleaves records from all K sources simultaneously. Each source streams independently. Memory is O(K) — one buffered record per source.

5. **SSH connections open in parallel.** All K SSH connections start simultaneously. The merge heap pulls from whichever source has the next record in sort order. Slow shards don't block fast ones (the heap just pulls from other sources until the slow one catches up).

## Implementation plan

### Phase 1: Core `-catalog` flag

1. Add `-catalog` flag to `merge` command (accepts a CSV file path)
2. Add optional `-if` flag for partition pruning (reuse `CatalogFilter`)
3. In handler: if `-catalog` is set, read catalog, prune, open SSH pipes per entry
4. Each SSH pipe becomes an `iter.Seq[Record]` source for `MergeSorted()`
5. Skip stdin — catalog provides all sources

**New function** in `merge.go`:
```go
func executeMergeCatalog(catalogFile string, filters []CatalogFilter,
    orderBy []OrderField, gpu bool, pushdownArgs []string) error {

    entries, _ := ssql.ReadCatalog(catalogFile)
    entries = ssql.PruneCatalog(entries, filters)

    remoteBin := sshRemoteBin(gpu)
    pipelineGroups := ssql.SplitOnPlus(pushdownArgs)

    // Open all SSH connections, get JSONL iterators
    sources := make([]iter.Seq[ssql.Record], len(entries))
    for i, entry := range entries {
        cmd := buildSSHCommand(entry, remoteBin, pipelineGroups)
        sources[i] = streamJSONLFromCommand(cmd)
    }

    merged := ssql.MergeSorted(orderBy, sources...)
    return writeWithInferredSchema(merged)
}
```

**Helper** `streamJSONLFromCommand` — starts a subprocess, returns an `iter.Seq[Record]` that reads JSONL from its stdout. This is a reusable primitive (useful beyond merge).

### Phase 2: Shard metadata enrichment

Add shard field like `from catalog` has:
```bash
ssql merge -catalog shards.csv -by timestamp -shard source
```

Each record gets a `source` field with `host:path`.

### Phase 3: GPU support

Add `-gpu` flag to use `ssql_gpu` on remote nodes (same as `from catalog -gpu`).

## Example usage

### Time-series merge across fleet
```bash
# shards.csv:
# host,path,date_from,date_to
# node1,/data/metrics-2024-01.csv,2024-01-01,2024-01-31
# node2,/data/metrics-2024-02.csv,2024-02-01,2024-02-28
# node3,/data/metrics-2024-03.csv,2024-03-01,2024-03-31

# Merge all months, sorted by timestamp
ssql merge -catalog shards.csv -by timestamp | ssql to table

# Only February data (partition pruning)
ssql merge -catalog shards.csv -by timestamp \
  -if date_from le 2024-02-28 -if date_to ge 2024-02-01 | ssql to table

# Merge with pushdown filter
ssql merge -catalog shards.csv -by timestamp \
  -- where -if service eq api | ssql to table
```

### Distributed log aggregation
```bash
# hosts.csv:
# host,path
# web1,/var/log/app.csv
# web2,/var/log/app.csv
# web3,/var/log/app.csv

# Merge logs sorted by time, only errors
ssql merge -catalog hosts.csv -by time \
  -- where -if level eq ERROR \
  | ssql to table
```

## Estimated effort

- Phase 1: ~2 hours (core wiring, reuses existing catalog + merge infrastructure)
- Phase 2: ~15 min (copy pattern from `from catalog`)
- Phase 3: ~10 min (add flag, pass to `sshRemoteBin()`)
