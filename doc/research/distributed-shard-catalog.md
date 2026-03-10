# Distributed Shard Catalog

**Status:** Research / Design Proposal
**Date:** March 2026
**Depends on:** [distributed-ssh-processing.md](distributed-ssh-processing.md)

## Problem Statement

When a dataset spans multiple files across multiple machines, you need a way to discover and locate shards before you can process them. Today, `from ssh://server/data/file.csv` addresses a single known file. But real distributed datasets look like:

```
machine-1:/data/events/2025-01.csv    (January events)
machine-1:/data/events/2025-02.csv    (February events)
machine-2:/data/events/2025-03.csv    (March events)
machine-2:/data/events/2025-04.csv    (April events)
machine-3:/data/events/2025-05.csv    (May+ events, ongoing)
```

Users need to:
1. Know which machines hold which shards
2. Know what data range each shard covers (dates, regions, etc.)
3. Query across all shards transparently
4. Push filters to only read relevant shards (partition pruning)

## Shard Catalog: A CSV That Describes Your Data

The simplest approach: a CSV file (or multiple CSVs) that maps shards to machines and describes what each shard contains.

### Basic catalog format

```csv
host,path,format
machine-1,/data/events/2025-01.csv,csv
machine-1,/data/events/2025-02.csv,csv
machine-2,/data/events/2025-03.csv,csv
machine-2,/data/events/2025-04.csv,csv
machine-3,/data/events/2025-05.csv,csv
```

Usage:

```bash
# Read all shards
ssql from --catalog shards.csv | ssql where -where status eq error | ssql to table

# Equivalent to (but automatic):
ssql union \
  <(ssql from ssh://machine-1/data/events/2025-01.csv) \
  <(ssql from ssh://machine-1/data/events/2025-02.csv) \
  <(ssql from ssh://machine-2/data/events/2025-03.csv) \
  <(ssql from ssh://machine-2/data/events/2025-04.csv) \
  <(ssql from ssh://machine-3/data/events/2025-05.csv) \
  | ssql where -where status eq error | ssql to table
```

### Catalog with range metadata (partition pruning)

The real power: telling ssql what data range each shard covers so it can skip irrelevant shards entirely.

```csv
host,path,format,date_from,date_to,region
machine-1,/data/events/2025-01.csv,csv,2025-01-01,2025-01-31,
machine-1,/data/events/2025-02.csv,csv,2025-02-01,2025-02-28,
machine-2,/data/events/2025-03.csv,csv,2025-03-01,2025-03-31,
machine-2,/data/events/2025-04.csv,csv,2025-04-01,2025-04-30,
machine-3,/data/events/2025-05.csv,csv,2025-05-01,,
machine-eu,/data/events/europe.csv,csv,,,europe
machine-us,/data/events/americas.csv,csv,,,americas
```

Empty `date_to` means "ongoing" (current shard). Empty date fields mean "all dates" (partitioned by region instead).

```bash
# Only reads shards covering March 2025 — skips machine-1 and machine-3
ssql from --catalog shards.csv -where date ge 2025-03-01 -where date le 2025-03-31 \
  | ssql group-by -field service -count | ssql to table

# Only reads the europe shard
ssql from --catalog shards.csv -where region eq europe \
  | ssql to json
```

### Required vs optional catalog columns

**Required columns:**
| Column | Description |
|--------|-------------|
| `host` | SSH host (from `~/.ssh/config`) or `local` for local files |
| `path` | Absolute path to the shard file |

**Optional columns:**
| Column | Description |
|--------|-------------|
| `format` | File format: `csv`, `json`, `jsonl`, `arrow` (default: inferred from extension) |
| `*_from` / `*_to` | Range bounds for partition pruning (any field name) |
| Any other column | Static metadata attached to every record from that shard |

The `local` host value means the file is on the local machine — no SSH needed:

```csv
host,path,format
local,/data/current/events.csv,csv
machine-1,/data/archive/2024.csv,csv
machine-2,/data/archive/2023.csv,csv
```

## Catalog Distribution

How do machines share catalog information? Several patterns, increasing in sophistication:

### Pattern 1: Single catalog file (simplest)

One machine holds the catalog. Users copy it or access it via SSH.

```bash
# Catalog lives on a known machine
ssql from --catalog ssh://catalog-server/etc/ssql/shards.csv | ssql to table

# Or keep a local copy
scp catalog-server:/etc/ssql/shards.csv ~/.ssql/catalogs/events.csv
ssql from --catalog ~/.ssql/catalogs/events.csv | ssql to table
```

**Pros:** Dead simple. One file to maintain.
**Cons:** Single point of truth must be kept up to date. Manual sync.

### Pattern 2: Per-machine catalogs merged at query time

Each machine publishes its own catalog describing its local shards. The query merges them.

```bash
# Each machine has /etc/ssql/catalog.csv describing its local shards
# host column is implicitly "self" (the machine serving the catalog)

# machine-1:/etc/ssql/catalog.csv
# path,format,date_from,date_to
# /data/events/2025-01.csv,csv,2025-01-01,2025-01-31
# /data/events/2025-02.csv,csv,2025-02-01,2025-02-28

# Query merges catalogs from all machines
ssql from --catalog-hosts machine-1,machine-2,machine-3 \
  | ssql where -where date ge 2025-03-01 | ssql to table
```

This desugars to:

```bash
# 1. Fetch and merge catalogs
ssql union \
  <(ssql from ssh://machine-1/etc/ssql/catalog.csv | ssql update -set host machine-1) \
  <(ssql from ssh://machine-2/etc/ssql/catalog.csv | ssql update -set host machine-2) \
  <(ssql from ssh://machine-3/etc/ssql/catalog.csv | ssql update -set host machine-3) \
  > /tmp/merged-catalog.csv

# 2. Use merged catalog
ssql from --catalog /tmp/merged-catalog.csv -where date ge 2025-03-01 | ssql to table
```

**Pros:** Each machine owns its own catalog. No central coordination.
**Cons:** Extra SSH round-trip to fetch catalogs before querying data.

### Pattern 3: Catalog in a shared location

Catalog stored in a shared filesystem, git repo, or object store.

```bash
# Git-managed catalog (versioned, auditable)
git clone git@github.com:team/data-catalog.git ~/.ssql/catalog
ssql from --catalog ~/.ssql/catalog/events.csv | ssql to table

# NFS/shared filesystem
ssql from --catalog /shared/ssql/catalogs/events.csv | ssql to table
```

**Pros:** Versioned, shared, can be updated by CI/CD when data is ingested.
**Cons:** Requires shared infrastructure (git, NFS).

### Pattern 4: Auto-discovery via SSH glob

No catalog file at all — discover shards by globbing on remote machines.

```bash
# Discover shards matching a pattern on each machine
ssql from --discover machine-1,machine-2,machine-3 \
  --pattern '/data/events/*.csv' \
  | ssql to table
```

This SSHs to each machine and runs `ls /data/events/*.csv`, then builds a catalog on the fly. No range metadata — every shard is read.

**Pros:** Zero maintenance. Discovers new files automatically.
**Cons:** No partition pruning. Must read every shard. Slower for large datasets.

### Recommended approach

Start with **Pattern 1** (single catalog file). It's the simplest, requires no new infrastructure, and can be managed manually or by a script. Add Pattern 2 (per-machine catalogs) when users have many machines and want decentralized ownership.

Pattern 4 (auto-discovery) is useful as a one-time tool to *generate* a catalog:

```bash
# Generate a catalog by discovering files
ssql catalog-discover machine-1,machine-2,machine-3 \
  --pattern '/data/events/*.csv' \
  > shards.csv

# Edit to add range metadata, then use
vi shards.csv
ssql from --catalog shards.csv | ssql to table
```

## Catalog File Format Details

### Multiple partition keys

Shards can be partitioned by more than one dimension:

```csv
host,path,format,date_from,date_to,region,tier
us-east-1,/data/logs/2025-01-premium.csv,csv,2025-01-01,2025-01-31,us-east,premium
us-east-1,/data/logs/2025-01-free.csv,csv,2025-01-01,2025-01-31,us-east,free
eu-west-1,/data/logs/2025-01-premium.csv,csv,2025-01-01,2025-01-31,eu-west,premium
eu-west-1,/data/logs/2025-01-free.csv,csv,2025-01-01,2025-01-31,eu-west,free
```

```bash
# Prunes to just the 2 premium shards
ssql from --catalog shards.csv -where tier eq premium | ssql to table

# Prunes to 1 shard: us-east premium January
ssql from --catalog shards.csv \
  -where tier eq premium \
  -where region eq us-east \
  -where date ge 2025-01-01 -where date le 2025-01-31 \
  | ssql to table
```

### Exact-value partition columns

Not all partitions are ranges. Some are exact values — the column name appears without `_from`/`_to` suffixes:

```csv
host,path,format,country
server-au,/data/sales/australia.csv,csv,AU
server-nz,/data/sales/new-zealand.csv,csv,NZ
server-uk,/data/sales/uk.csv,csv,GB
```

```bash
# Reads only the AU shard
ssql from --catalog shards.csv -where country eq AU | ssql to table
```

### Static metadata columns

Any catalog column not recognized as a partition key is attached as a static field to every record from that shard. This is useful for enrichment:

```csv
host,path,format,source_system,data_owner
machine-1,/data/crm/contacts.csv,csv,salesforce,sales-team
machine-2,/data/erp/orders.csv,csv,sap,ops-team
```

Every record from `contacts.csv` gets `source_system=salesforce` and `data_owner=sales-team` added automatically.

## Execution Model

### How `from --catalog` works

```
1. Read catalog CSV
2. Apply partition pruning (skip shards that don't match -where filters)
3. For remaining shards:
   a. Group by host (to batch SSH connections)
   b. For each host, open SSH connection (with ControlMaster)
   c. Stream records from each shard
   d. Merge streams (interleave or concatenate)
4. Emit merged record stream to stdout
```

### Parallelism

Read shards in parallel, bounded by a concurrency limit:

```bash
# Default: 4 concurrent SSH connections
ssql from --catalog shards.csv | ssql to table

# Higher parallelism
ssql from --catalog shards.csv --parallel 8 | ssql to table
```

Shards on the same host can share an SSH connection (multiplexed via ControlMaster). Shards on different hosts run truly in parallel.

### Merge ordering

By default, records arrive in arbitrary order (whichever shard responds first). For ordered output, use `--merge` to k-way merge the shard streams — the same operation as the `merge` command:

```bash
# Merge by date across shards (requires shards to be pre-sorted by date)
ssql from --catalog shards.csv --merge date | ssql to table
```

Each shard must be sorted by the merge field, but the shards themselves can arrive in any order.

### Error handling

With multiple machines, SSH failures are inevitable — hosts go down, keys expire, files move, networks timeout. The question is whether to abort the whole pipeline or continue with partial results.

**Default: fail-fast.** The first SSH error stops the pipeline with a clear error message. This is correct for scripts and production — silent partial results are dangerous.

**Opt-in resilience with `--on-error`:**

```bash
# Default: fail on first error
ssql from --catalog shards.csv | ssql to table

# Skip failed shards, log warnings to stderr
ssql from --catalog shards.csv --on-error skip | ssql to table
# stderr: WARN: shard machine-2:/data/events/2025-03.csv failed: ssh: connect to host machine-2: Connection refused

# Retry once per shard, then skip
ssql from --catalog shards.csv --on-error retry | ssql to table
```

**Shard provenance with `--shard-field`:**

To make it visible which records came from which shard (and obvious when a shard is missing), add a provenance field:

```bash
# Each record gets a _shard field showing its origin
ssql from --catalog shards.csv --shard-field _shard | ssql to table

# Output includes:
# _shard                              | name  | age
# machine-1:/data/events/2025-01.csv  | Alice | 30
# machine-2:/data/events/2025-03.csv  | Bob   | 25
```

This composes well with `--on-error skip` — you can see which shards contributed and which are absent:

```bash
# Check which shards actually responded
ssql from --catalog shards.csv --on-error skip --shard-field _shard \
  | ssql distinct -field _shard | ssql to table
```

**Summary of failed shards:**

When `--on-error skip` is used, emit a summary to stderr after the pipeline completes:

```
WARN: 2 of 5 shards failed:
  machine-2:/data/events/2025-03.csv — ssh: connect to host machine-2: Connection refused
  machine-3:/data/events/2025-05.csv — remote: No such file or directory
Processed 3 of 5 shards (60%)
```

This gives users confidence in what they got and what they missed, without polluting stdout.

### Push-down to shards

When the pipeline contains filters or aggregations, push them to each shard:

```bash
# This pushes the where + group-by to each shard
ssql from --catalog shards.csv \
  --remote 'where -where status eq error | group-by -field service -count' \
  | ssql group-by -field service -sum count \
  | ssql to table
```

Note the two-level aggregation: each shard groups locally, then results are re-aggregated locally. This is the standard map-reduce pattern. The user specifies the remote portion explicitly (consistent with the `--remote` design in distributed-ssh-processing.md).

## Code Generation

```bash
SSQLGO=1 ssql from --catalog shards.csv | ssql generate-go
```

Generated code:

```go
package main

import (
    "os"
    ssql "github.com/rosscartlidge/ssql/v4"
)

func main() {
    // ssql from --catalog shards.csv
    catalog, err := ssql.ReadCatalog("shards.csv")
    if err != nil {
        // error handling
    }
    records := ssql.FromCatalog(catalog, ssql.CatalogOptions{
        Parallel: 4,
    })

    ssql.WriteJSONFastToWriter(records, os.Stdout)
}
```

## Example: Building a Catalog

### Manual creation

```bash
cat > shards.csv << 'EOF'
host,path,format,date_from,date_to
local,/data/events/current.csv,csv,2025-06-01,
prod-1,/data/events/2025-q1.csv,csv,2025-01-01,2025-03-31
prod-1,/data/events/2025-q2.csv,csv,2025-04-01,2025-06-30
prod-2,/data/events/2024.csv,csv,2024-01-01,2024-12-31
EOF
```

### Generated from file listing

```bash
# Discover files, pipe through ssql to build catalog
for host in prod-1 prod-2; do
  ssh $host 'ls /data/events/*.csv' | while read path; do
    echo "$host,$path,csv"
  done
done | (echo "host,path,format"; cat) > shards.csv
```

### Self-updating catalog

A cron job or data ingestion pipeline updates the catalog when new shards appear:

```bash
# add-shard.sh — called by ingestion pipeline
echo "$HOST,$PATH,$FORMAT,$DATE_FROM,$DATE_TO" >> /shared/catalogs/events.csv
```

## Relationship to `merge` Command

The `merge` command (v4.26.0) already handles k-way merge of pre-sorted inputs. Catalog-based reads can leverage this directly:

```bash
# Today: explicit merge of known files
ssql merge -field timestamp \
  <(ssql from ssh://m1/data/jan.csv) \
  <(ssql from ssh://m2/data/feb.csv) \
  <(ssql from ssh://m3/data/mar.csv)

# Future: catalog-driven merge
ssql from --catalog shards.csv --merge timestamp
```

The `--merge-sort` flag on `from --catalog` is syntactic sugar that constructs the same k-way merge internally.

## Open Questions

1. **Catalog format: CSV or JSONL?** CSV is natural (it's a table), but JSONL allows nested metadata. Start with CSV for simplicity.
2. **Catalog caching?** Should ssql cache a fetched remote catalog locally? For how long?
3. **Schema validation?** Should ssql verify that all shards have compatible schemas before merging? Or handle mismatches gracefully (union of fields)?
4. **Catalog updates during query?** If a new shard appears mid-query, ignore it (snapshot consistency).
5. **Authentication per host?** The catalog assumes SSH config handles auth for each host. Document this clearly.
6. **Compression hints?** Should the catalog indicate whether shards are compressed (`.csv.gz`, `.csv.zst`)?

## Summary

The shard catalog is a CSV file that describes a distributed dataset:
- **Where**: which machine and path holds each shard
- **What**: what data range each shard covers (for partition pruning)
- **How**: what format each shard is in

It composes naturally with the SSH transport from `distributed-ssh-processing.md`:
- Catalog provides discovery → SSH provides transport → ssql provides processing
- No new infrastructure: it's just a CSV file and SSH connections
- Partition pruning skips irrelevant shards before any data moves
- Push-down reduces what travels over the wire from relevant shards
