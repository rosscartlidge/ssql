# Cloud Data Strategy: Mounts, Serve-in-Region, and `from https://`

Reference: DFC112
Created: 2026-08-23
Last modified: 2026-08-23

[Back to Index](./README.md)

**Status:** Position — discussed and agreed (Ross + Claude,
2026-08-23). Records the reasoning so it isn't relitigated; the one
build item (`from https://` with Range) is queued, not started.

Builds on: [DFC108](./dfc108_split_pipelines_server_browser.md)
(split pipelines), [DFC110](./dfc110_sample_command.md) /
[DFC111](./dfc111_sampling_case_study.md) (byte-offset sampling).

## The question

With the multi-level workspace working — local wasm tail, served
head, ssh fan-out — should ssql natively support data in the major
cloud object stores (S3, GCS, Azure Blob)? Cloud-backed filesystems
(rclone mount, Dropbox, pcloud) already make bucket data look like
files. Do we need more?

## The analysis

### 1. Mounts cover more than expected — with one sharp caveat

An rclone mount (S3/GCS/Azure/Dropbox/pcloud/…) makes EVERY existing
feature work unchanged: `from`, direct-file joins, catalogs,
`serve -dir`, and even `-sample`'s byte-offset seeks (they become
HTTP range requests under FUSE).

The caveat: **latency multiplies through `-sample`'s sequential
seeks.** At ~50–100ms per range request, `-sample 1000` on a mounted
bucket is 1–2 minutes — the feature that is 14ms on local disk
degrades worst on exactly the storage this question is about. The
fix (parallelizing the seek draws — they are independent by
construction, each a pure function of (seed, draw index)) is queued
below.

### 2. Object storage is a DUMB tier — and that decides the architecture

The local/server/ssh success works because every tier has COMPUTE:
pushdown moves work to where data lives (`-- where` on the remote,
typed heads on the server). S3/GCS have no compute; any native
support is fetch-only by nature.

Therefore the architecturally-correct answer for heavy cloud data
ALREADY EXISTS: **run `ssql serve` on a small VM in the data's
region** — `-dir` on the (mounted or synced) bucket, workspace from
anywhere, ⚡ typed heads for speed, tailscale for reach. Compute goes
near the data; only reduced results cross the wide network. That is
DFC108 doing precisely its job. No new code, and no new code could
do better, because there is no compute to push to inside a bucket.

### 3. Why NOT native `from s3://` (SDK-based)

- Three cloud SDKs (AWS/GCS/Azure) is a large, churning dependency
  surface in a deliberately boring dependency tree — and each brings
  credential chains, region config, retry policy, and its own bug
  classes.
- Auth becomes ssql's problem: credentials files, env conventions,
  SSO flows — none of which we can do better than the user's own
  cloud CLI.
- It duplicates what the mount already does, minus the mount's
  universality (a mount also covers Dropbox, pcloud, WebDAV, SFTP…).

### 4. The one native piece worth building: `from https://` with Range

Plain HTTPS with Range requests is:

- **SDK-free** — net/http only.
- **Auth-free by design** — every major cloud can mint a
  **presigned URL** (`aws s3 presign`, `gsutil signurl`, `az storage
  blob generate-sas`); auth stays the cloud CLI's problem, never
  ssql's. Plain static hosts and internal artifact stores work too.
- **Feature-complete for the read side**: streaming `from` (one GET),
  byte-offset `-sample` (Range requests — parallelizable naturally),
  and eventually **parquet-over-Range** — fetch only the byte ranges
  of the columns/row-groups a query touches, which is the real prize
  and its own future unit.
- **One mechanism instead of three SDKs**, consistent with the
  "one semantics" doctrine (an https source is just another source;
  codegen/`generate sql` treat it like any file — DuckDB reads https
  URLs natively, pleasingly).

Refusal edges, per the loudness doctrine: servers without Range
support → `-sample` refuses loudly (naming the full-read
alternative); redirects followed; no retry magic in v1.

## The decision

1. **No cloud SDKs.** Position, not a deferral — revisit only if
   presigned-URL workflows prove genuinely insufficient in practice.
2. **Document the two working patterns** (codelab section): mount for
   casual/local use; serve-in-region for serious use — the latter is
   the recommended pattern and deserves a worked example (small VM +
   `ssql serve -listen-http tailscale:8080 -dir /mnt/bucket`).
3. **Build `from https://` with Range** as the one native piece
   (queued): streaming reads + `-sample` via Range + presigned-URL
   examples in the help and codelab.
4. **Parallelize `-sample`'s seek draws** so high-latency storage
   (mounts, and later https) isn't punished — independent draws, so
   this is concurrency-safe by construction; determinism is
   unaffected (selection stays a pure function of seed and draw
   index; only the fetch order changes, and emit order is by file
   position regardless).
5. **Parquet-over-Range** noted as the eventual big win; not scoped.

## Queued items (also in TODO)

- [ ] `from https://URL` (+ `-sample` via Range; loud no-Range refusal)
- [ ] Parallel seek draws in the byte-offset samplers
- [ ] Codelab: "Cloud data" section (mount pattern, serve-in-region
      worked example, presigned URLs)
- [ ] (future) parquet column/row-group pruning over Range
