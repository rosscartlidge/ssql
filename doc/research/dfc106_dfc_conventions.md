# DFC Conventions for Research Documents

Reference: DFC106
Created: 2026-08-12
Last modified: 2026-08-12

[Back to Index](./README.md)

**Status:** Adopted 2026-08-12 (this document is the first born-DFC doc)

The DFC (Doc For Comment) system gives every document in `doc/research/`
a stable, chronological reference number — like an RFC number for this
project's research trail. Adapted from conventions Ross maintains for a
similar doc collection at work, where they proved useful for two things
this directory lacked:

1. **Referral** — "see DFC085" is short, stable, and survives renames;
   `doc/research/multimode-equivalence-testing.md` is none of those.
2. **Chronology** — the numbers order the research trail. DFC001 is the
   2025-09-29 SeqFactory design; reading the index top to bottom replays
   the project's thinking in order, which `ls` and topic-grouped indexes
   cannot do.

## The Conventions

**Metadata block** — every doc carries, right below its `# Title`:

```
Reference: DFCnnn
Created: YYYY-MM-DD
Last modified: YYYY-MM-DD

[Back to Index](./README.md)
```

**Numbering** — assigned once, never reassigned. The 105 pre-existing
docs were backfilled chronologically by git creation date (DFC001–
DFC105); new docs take the next free number (`scripts/dfc.py --new`).

**Filenames** — NEW docs are named `dfcnnn_short_description.md`
(lowercase), so `ls` sorts the go-forward trail chronologically. The
pre-DFC files deliberately keep their original names: dozens of inbound
links from `CLAUDE.md`, `claude/*.md`, journals, and code comments would
break on rename. Only the metadata identifies them.

**Deprecation links** — when a doc supersedes another, the new doc adds
`Deprecates: [DFCnnn](./old_file.md)` to its metadata and the old doc
gets `Deprecated-by: [DFCnnn](./new_file.md)`. This is the guard against
a future session implementing from a stale plan (the mmap proposal's
numbers were superseded by its own honest-results section — exactly the
kind of drift this catches at the door).

**Referral style** — journals and commit messages cite docs as plain
text (`Ref: DFC085`), greppable in both directions. (The originating
google3 convention used markdown g3doc links in CL descriptions; git
commit messages don't render markdown, so the bare number + the index
is the equivalent here.)

**Context discipline** — locate docs via `scripts/docsearch.sh` or the
README index, then read only the 1–2 DFCs the task needs. Never
bulk-load the research directory into a session.

## Tooling: `scripts/dfc.py`

| Command | Does |
|---|---|
| `--new` | print the next free number + suggested filename |
| `--stamp` | insert missing metadata blocks; sync dates from git (idempotent, never renumbers) |
| `--index` | regenerate the chronological table in `README.md`'s marked region |
| `--check` | validate everything below; exit 1 with named fixes |

Run `scripts/dfc.py --stamp && scripts/dfc.py --index` after creating or
editing research docs, **before committing** — an uncommitted edit
stamps as today, so the header date and the eventual commit date agree.

**Dates come from git, not hands.** A hand-maintained "Last modified"
that drifts is worse than none — it actively lies about freshness. The
originating convention relied on discipline; here `--stamp` computes
both dates from git (creation = first commit, modified = last commit,
or today for uncommitted edits) so the header can't silently rot.

## Enforcement

`make doc-check` (Check 9 in `scripts/validate-docs.sh`) runs
`dfc.py --check`, which fails the build on:

- a doc missing its metadata block or Back-to-Index link;
- duplicate reference numbers;
- `Last modified` older than the file's last git commit (forgot to
  stamp);
- a doc absent from the README index, or the generated region being out
  of date.

Same philosophy as `TestPlaygroundMainMatchesCLIRegistration`: where an
index can drift from reality, gate it — don't rely on remembering. The
gate was watched failing (removed a Reference line; the check named the
doc and the fix) before being trusted.

## The Index

`doc/research/README.md` keeps its curated topic sections (they answer
"what exists about X?") and additionally carries the generated
chronological table between `DFC-INDEX-START/END` markers (it answers
"what happened when?" and maps numbers to files). The table is
regenerated wholesale by `--index`; never edit it by hand.
