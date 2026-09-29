# User Documentation Cleanup: Audit and Recommendations

Reference: DFC140
Created: 2026-09-29
Last modified: 2026-09-29

[Back to Index](./README.md)

## 0. Brief and verdict

Ross, 2026-09-29: *"everything in `doc/*` (not research) should be
correct and useful to users; anything else should be either removed or
moved somewhere else."*

This document is the audit of the 26 markdown files under `doc/`
(excluding `doc/research/` and `doc/archive/`), 15,170 lines in total,
against ssql v4.109.0 (build b519cf4a), `go doc`, `ssql COMMAND -help`,
the Makefile and the scripts that consume the docs. Nothing has been
changed yet; this is the recommendation for Ross to review. The per-file
findings cite line numbers as of commit c90221e.

**Verdict in one table.** KEEP = correct, leave alone. FIX = user doc,
stays, listed corrections. RESTRUCTURE = user doc, stays, but needs
reorganising as well as fixing. MOVE = not a user doc, relocate to
`doc/research/` or `doc/archive/`. REMOVE = delete from the tree (and
stop scripts regenerating it there).

| File | Lines | Last commit | Verdict | Headline |
|---|---:|---|---|---|
| `README.md` (doc index) | 49 | 2026-09-28 | RESTRUCTURE | step numbers disagree with root README; Signal Processing listed twice; maintainer AI files in the user section |
| `install.md` | 259 | 2026-09-29 | FIX (minor) | two Go hello-worlds duplicate codelab-intro; playground should be the first "no install" option; unsourced "10-50× GPU" |
| `cli-codelab.md` | 989 | 2026-09-29 | FIX | wrong "rig" link (L865 → cli-debugging); README "Installation" reference (L66); duplicated Alt-r sentence; reference line omits `generate schema/json`, `run`, `codelab` |
| `cli-signal-processing.md` | 1067 | 2026-09-19 | FIX | `for … done \| ssql to table` loops concatenate several `_schema` headers → phantom rows (L470-477 is runner-executed); GPU `go build` step wrong |
| `cli-codelab-serve.md` | 46 | 2026-09-04 | FIX | `shuffled.csv` is not a codelab file; dangling "see the serve section" |
| `tmux-for-ssql.md` | 121 | 2026-09-14 | KEEP | one duplicated intro sentence |
| `cli-shell.md` | 47 | 2026-09-28 | FIX | intro paragraph duplicated (L3 vs L7-9, README move leftover); popup wording |
| `cli-debugging.md` | 575 | 2026-03-12 | REMOVE (merge) | every jq recipe is broken by the `_schema` header line; `ssql group` is not a command; superseded by `describe`, loud unknown-field errors, `cast`; duplicates troubleshooting |
| `cli-troubleshooting.md` | 768 | 2026-09-23 | FIX (absorb debugging) | Issues 9-14 accurate; Issues 1-8 and the jq catalogue predate loud errors and the schema header; `split -l` advice produces headerless chunks |
| `codelab-intro.md` | 872 | 2026-09-05 | FIX | three broken anchors into api-reference; misleading Error Handling section; empty "Production Considerations" |
| `typed-codelab.md` | 637 | 2026-09-06 | FIX | cheat sheet says `typed.ReadCSV` is "lossy" (it panics with `*ReadError` since v4.91); nonexistent `cmd/ssql-typed-scale`; stale Tier 3 list; parallel section outside the TOC |
| `typed-reference.md` | 782 | 2026-09-29 | RESTRUCTURE | stale Status box; 140-line Roadmap is ship history; five shipped APIs documented only inside the Roadmap; no `Stream[T]` section; conflicting benchmark numbers |
| `api-reference.md` | 3060 | 2026-09-29 | RESTRUCTURE | ~20 wrong signatures/structs; ~10 non-compiling examples; four duplicated sections; broken TOC; sections filed under the wrong heading; codegen-support exports presented as user API |
| `EXPRESSIONS.md` | 891 | 2026-09-23 | FIX | `where -expr` (flag does not exist, five places); `-if` conditions described as OR (they AND); error-handling section says failures are logged and skipped (they fail the pipeline) |
| `library-tour.md` | 579 | 2026-09-28 | FIX | 16 unclosed `**` markers; dangling "Or use the CLI:"; `correlate -with FILE` (it takes a field); wrong GPU thresholds; `-expr` |
| `performance.md` | 180 | 2026-09-28 | FIX | `generate go -typed`; 0.32 s vs typed-reference's 0.15 s for the same run; machine unnamed for two tables; Tier 3 / Phase B jargon |
| `ai-code-generation.md` | 1129 | 2026-09-27 | FIX | WAV and chart API calls have wrong signatures/argument order (an LLM copies them verbatim); no AsofJoin/Parquet/typed |
| `ai-cli-generation.md` | 523 | 2026-09-20 | FIX | `to json` described as JSONL; `to chart FILE` positional; `correlate -field-a/-field-b`; examples sort on fields they never produce; ~15 commands and the `-pipeline` form absent |
| `ai-human-guide.md` | 648 | 2026-09-05 | FIX (shrink to ~100 lines) | wrong tool commands (`claude-code`, wrong gemini package); nested fence breaks the code block; fictional "success stories"; never mentions the CLI prompt |
| `AI-PROMPT-README.md` | 229 | 2026-05-03 | MOVE → research | maintainer test-loop description; duplicates DFC042 `ai-prompt-system.md`; stale counts |
| `ai-test-cases.md` | 863 | 2026-06-16 | MOVE → research (test input) | script input, not reading material; three cases reward wrong forms |
| `ai-test-results.md` | 20 | 2026-01-29 | REMOVE | script output claiming 100% on 2026-01-29; both prompts edited since |
| `ai-fix-request.md` | 34 | 2026-04-22 | REMOVE | malformed script output with an absolute home path |
| `ai-prompt-improvements.md` | 342 | 2026-01-22 | MOVE → archive | historical analysis; every "high priority" item is already done; line refs point at an old layout |
| `VALIDATION.md` | 460 | 2026-01-22 | MOVE → research | contributor doc; lists 10 of 11 checks; shows CI YAML that does not exist |
| `codelab-data/README.md` | 27 | 2026-09-27 | KEEP | drop "(DFC125)" (this file is copied onto users' disks by `ssql codelab`) |

Net effect if all of it is done: 26 files → 19 user-facing files; the
three biggest (api-reference, cli-troubleshooting, ai-human-guide) get
shorter; nothing a script reads disappears without the script being
edited in the same commit (§5).

## 1. Method

Four independent read-throughs (one per group of files), each checking
every file in full against:

- `ssql COMMAND -help` for every flag and command a doc uses;
- `go doc github.com/rosscartlidge/ssql/v4` and `…/typed` for every
  signature, struct and constraint a doc shows;
- CHANGELOG.md for "is this still true" claims;
- the root README and `doc/README.md` for anchors and step numbers;
- `scripts/*.sh` and the Makefile for which files are consumed by
  tooling (a MOVE/REMOVE must edit the consumer in the same commit).

A shared checklist covered the known legacy patterns: `SSQLGO` as
canonical, `generate go -typed`, `ssqlgen`, `top -by`, map-style record
access, import paths without `/v4`, nonexistent flags, limitations that
have since been fixed, dead links and anchors, maintainer-only content,
version stamps written as news.

Every runtime claim in §2 was re-verified by hand against the installed
v4.109.0 binary before being written here.

**Good news first.** No file uses `SSQLGO` as the canonical name,
`ssqlgen`, `top -by`, or a `/v4`-less import path. Map-style record
access appears only in deliberate "WRONG" examples. The four codelabs
run green under `make doc-test` (added to the pre-tag gates on
2026-09-29). The problems below are drift, accretion and a few generic
sections that were written before ssql grew the features that make them
unnecessary.

## 2. Cross-cutting findings

These recur across files and should each be fixed once, consistently.

### 2.1 The `_schema` header line breaks every jq recipe

Since the schema header shipped, a pipeline's JSONL output starts with a
`{"_schema": …}` line. `cli-debugging.md` and `cli-troubleshooting.md`
(Issues 1-8 and the jq catalogue) were written before it and never
updated. Verified against `doc/codelab-data/employees.csv` (10 rows):

```
$ ssql from employees.csv | head -1 | jq 'keys'      → ["_schema"]   (not the field names)
$ ssql from employees.csv | wc -l                    → 11            (every count is +1)
$ … | jq -r '.dept' | sort | uniq -c                 → an extra "1 null"
$ … | jq '.age |= tonumber'                          → error: null (null) cannot be parsed as a number
$ … | jq '.status |= ascii_downcase'                 → error: explode input must be a string
```

The same bug bites `cli-signal-processing.md` differently: a
`for window in …; do ssql … ; done | ssql to table` loop concatenates
one `_schema` line per iteration, and every header after the first is
read as a record (verified: an empty row appears between the `hann` and
`none` groups at L470-477, which `codelab-run.sh signal` executes and
does not catch because the oracle is a substring). L961-987 has the
same shape writing to files.

Fix: where jq is genuinely wanted, pipe through `ssql to jsonl` first
(or `tail -n +2`); for the loops, end each body with `| ssql to jsonl`
or restructure with `ssql union`. But most of the jq material should
simply go (§2.3).

### 2.2 Flags and commands that do not exist

| Wrong | Right | Where |
|---|---|---|
| `ssql where -expr '…'` | `-if-expr` (alias `-x`) | EXPRESSIONS.md L825, 834, 837, 840, 861; library-tour.md L543 |
| `ssql group …` | `group-by` | cli-debugging.md L263, 285, 469, 552; cli-troubleshooting.md L301, 318, 581-587, 624 |
| `correlate -field-a F -field-b F` | `-field F -with G` | ai-cli-generation.md L68, L248 |
| `correlate -field reading -with template.csv` | `-with` takes a field, not a file | library-tour.md L336 |
| `to chart -x … -y … chart.html` | output via `-output/-o` | ai-cli-generation.md L78, L500 |
| `to json` "writes JSONL" | `to json` is a pretty-printed array; JSONL is `to jsonl` | ai-cli-generation.md L77, 91, 100, 436, 499 |
| `distinct [-field F]` | `distinct` has no field flag | ai-cli-generation.md L494 |
| `generate go -typed` | `-mode typed` (the default for `-pipeline`) | performance.md L93 |
| `cmd/ssql-typed-scale/data.csv` | does not exist | typed-codelab.md L603 |

### 2.3 Advice that predates a feature

| Doc says | ssql now | Where |
|---|---|---|
| "filter silently returns 0 rows if the field is misspelt; diagnose with jq" | `Error: where references unknown field(s): nonexistent (available: age, city, …)` | troubleshooting Issue 3, debugging L350-367 |
| "numeric filter returns 0 because the field is a string; fix the JSONL with jq" | literal of the wrong kind stops the pipeline (Issue 12a); `ssql cast -type F int` | troubleshooting Issue 6, debugging L382 |
| "GROUP BY materialises; split the file with `split -l 10000`" | `sort -spill DIR -memory SIZE`, `group-by -spill`; `split -l` also drops the CSV header from every chunk after the first | troubleshooting L292-325, L627-647 |
| "sample with `shuf \| head`" (shuffles the schema line away) | `ssql sample`, `from csv -sample N -sample-seed S` | debugging L300 |
| "inspect types with jq" | `ssql describe`, `ssql generate schema -pipeline '…' -data` | debugging throughout |
| "expression runtime errors are logged and processing continues" | eval errors fail the pipeline in exec and in generated code | EXPRESSIONS.md L855-858 |
| "`from ssh`, `-if-expr` need a Record adapter (Tier 3)" | both transpile to typed | typed-codelab.md L612-617; performance.md L138-146 |
| "`typed.ReadCSV` is lossy on parse error" | panics with `*typed.ReadError` since v4.91 | typed-codelab.md L522 |
| "`generate ssql` infers used columns and rewrites `-columns`" | `generate go -O` does the same by default | performance.md L174-176 |

### 2.4 Benchmarks: one number, three files, two values

The 14.6 M-row group-by with projection is **0.15 s** in
typed-reference (L273-277, L722) and **0.32 s** in performance.md
(L161). The 14.6 M cube is **1.7 s** at typed-reference L536 (the
pre-default-change figure) and **0.27 s** everywhere else. The 10 M
3-join table (74.8 / 4.94 / 0.42 s) is copied into three files; the 1 M
codegen table into two; the 10 M parallel table into three; "15× faster,
34× less memory" into two. Only typed-reference names the machine
(Core Ultra 9 275HX) for the 3-join run; performance.md promises "the
sections say which machine" and then does not for two tables.

Fix: `performance.md` becomes the single source for every measured
number, stating the machine per table; every other file links to it
with at most a one-line summary. Re-measure the two conflicting rows
rather than picking one.

### 2.5 Maintainer material in user docs

Three kinds, all of which should go or be collected into one
"Design notes" line at the end of a file:

- **DFC numbers and research links** in prose: api-reference (8
  places), EXPRESSIONS (5), typed-reference (3), typed-codelab, codelab-
  intro, doc/README, codelab-data/README, performance. A user does not
  know what "DFC129" means.
- **Version stamps written as news**: "As of v4.40", "(v4.57.0+)",
  "before v4.91.0 …", "(2026-09-06)", "found 2026-09-27 by the
  `join_left_type` equivalence case". CHANGELOG owns history; a reference
  states the current rule.
- **Implementation vocabulary**: "lanes", "Tier 3", "Phase B", "record
  fallback", "shared by exec and generated code so the lanes cannot
  disagree", "71× faster than Window() at 10K rows", "building a schema
  per record cost 4× on a 14.6 M-row file". These belong in `claude/` or
  research.

### 2.6 Learning-path numbering

The root README (L76-80) numbers codelab-intro 3 and typed-codelab 4.
`doc/README.md` (L12-17) inserts `cli-codelab-serve.md` as step 3 and
so numbers them 4 and 5, and both codelabs say so in their "Where you
are" boxes. Make serve a sub-bullet under the CLI codelab (as tmux is)
and use the README's numbering everywhere.

## 3. Per-file findings

Grouped by the role each file should have after cleanup. Line numbers
are as of c90221e; the auditors' full reports are summarised, not
repeated, where the table in §0 already says enough.

### 3.1 Index and install

**`doc/README.md`** RESTRUCTURE. Sections should be: Learning path
(README numbering; serve and tmux as sub-bullets; Signal Processing
listed once, not at L14 and L33), Install and setup, Reference
(api-reference, typed-reference, EXPRESSIONS, cli-shell, cli-
troubleshooting), Background (performance, library-tour), Using an LLM
(ai-human-guide, ai-cli-generation, ai-code-generation), and a short
"For contributors" pointer to `doc/research/README.md`. Drop the
DFC125 / runner-script paragraph at L7-10 and the "(15× faster, 34× less
memory than Record)" marketing at L29.

**`install.md`** FIX (minor). Move the playground section (L235-259) to
the top as the "try it without installing" option. Collapse "Hello ssql
(Go library)" and "Your first chart" (L185-233) into one verify-it-works
snippet and link codelab-intro for the rest. Source or drop "10-50×
faster" for GPU (L141; performance.md has no GPU numbers). The .deb
version pins (L232-239) are already rewritten by `make deb` and checked
by doc-check Check 10; leave them. README L95 says "Go 1.23+" while
install.md L9 says "Go 1.21+" (toolchain auto-download makes 1.21
correct); align README to install.md.

### 3.2 CLI learning path

**`cli-codelab.md`** FIX. L66-67 "see the README's Installation
section" → link `install.md#option-6-debian-packages`. L865-866 "cli-
debugging.md covers the rig" → the rig is in
`doc/research/ssh-test-environment.md` (cli-debugging has nothing about
SSH). Merge the two Alt-r sentences at L791-797. Extend the reference
line at L972 with `generate schema|json`, `run`, `codelab`. L781's "the
README's 14.6 M-row cube" still resolves but should point at
performance.md. L117 "about 600 lines" → `ssql -shell-init | wc -l` is
690 now; say "a few hundred". Everything else checks out: `-mode typed`,
`SSQL_MODE`, `top 3 -field`, `/v4`, `generate go -run -pipeline`.

**`cli-signal-processing.md`** FIX. The three concatenation loops (§2.1)
at L470-477, L961-972, L979-987. The GPU build illustration at
L1017-1022: `go build -tags gpu -o fft_program fft_program.go` only
works inside a module that requires ssql, and `./fft_program <
input.csv` is wrong because the generated program reads `multi_freq.csv`
itself. Rewrite as `ssql_gpu generate go -run -pipeline '…'` or show the
module steps. All flags match `-help`; the research/gpu-acceleration.md
deep-dive link at L1066 is acceptable.

**`cli-codelab-serve.md`** FIX. L12 `ssql serve shuffled.csv` →
`employees.csv` (the fields used at L29-31 are employees fields; there
is no shuffled.csv in the codelab data). L46 dangling "see the serve
section" → link `cli-codelab.md#4-save-and-share`. Flags and defaults
match `ssql serve -help`.

**`tmux-for-ssql.md`** KEEP. Cut one of the two "five things" sentences
at L7-12.

**`cli-shell.md`** FIX. L3 and L7-9 are the same paragraph twice (a
leftover from the README move). L21 "Alt-g opens in a tmux popup" →
"popup or pager" (L41 already says it falls back). L41-44 is redundant
with itself. "~80 expression functions" at L37 → `ssql functions` lists
about 65-70; say "the `ssql functions` list".

**`cli-debugging.md`** REMOVE, merging the little that survives into
troubleshooting. It is the oldest CLI doc (2026-03-12) and almost every
recipe now gives a wrong number or an error (§2.1). `ssql group` at four
places. L136-139 (`jq '.metadata.created'`, `jq 'flatten'` on flat
records) are nonsense examples. It never mentions `describe`, `generate
schema -data`, `count`, `from -records`, `tee`, `-spill`, `generate go
-run`, Alt-h or Ctrl-O, which between them replace nearly all of its jq.
Its "Troubleshooting Common Issues" (L312-400) repeats troubleshooting
Issues 2-5 and its one-liners (L499-556) repeat troubleshooting
L485-607. What to keep: a short "inspect a pipeline with ssql itself"
section (describe, generate schema -data, count, tee, `to jsonl | jq`
for jq users) inside troubleshooting. Update README L87 and doc/README
L31 which link it.

**`cli-troubleshooting.md`** FIX and absorb debugging. Keep Issues 9-14
as they are (accurate, recent). Rewrite Issues 1-8 around the loud errors
ssql now gives (§2.3): L59-60 quotes a wrong message (actual: `Error:
reading file: open nosuch.csv: no such file or directory`); Issue 3
(L120-127) and Issue 6 (L243-250) describe silent zero-row results that
no longer happen, and Issue 6 is superseded by Issue 12a. Renumber
12a/12 (L411, L422 are out of order). L478 "(README, Installation)" →
install.md anchors. Drop the generic jq catalogue (L485-607, L756-768),
the `split -l` and TMPDIR filler (L292-325, L627-647), and point at
`-spill`, `from -records/-last/-sample/-columns`, `generate go -run` and
performance.md instead. L447-448 (`generate sql` refuses reserved
names) is still true.

### 3.3 Go learning path

**`codelab-intro.md`** FIX. Broken anchors into api-reference: L539
`#aggregation-operations` (heading is "Aggregation & Analysis"), L611
`#window-operations` ("Batch Window Operations"), L676 `#stream-control`
(no such heading; nearest is "Limiting & Pagination"). These will move
again under the api-reference restructure (§3.4), so fix them last. L14
"step 4" → 3 (§2.6). Error Handling (L765-791): "Unsafe … panics on
error" is false for `ssql.Select` (the undefined `mustParseInt` would
panic); `SelectSafe`'s `dataWithErrors` is never explained. Rewrite
around a real `ReadCSVSafe` / `*CellError` example. Delete the empty
"Production Considerations" (L828-832). Merge What's Next / Try It
Yourself / Need Help into one section (they point at typed-codelab five
times). Drop DFC125 from L5-9. The emojis (15 places) are a taste call;
the rest of the docs do not use them.

**`typed-codelab.md`** FIX. L522 cheat sheet: `ReadCSV` "lossy" →
"panics with `*typed.ReadError`; use `ReadCSVSafe` for a per-row error".
L458 "the parallel API is ReadJSONL" → "counterpart". L475-476
`encoding/json` + `bufio.Scanner` → the positional decoder (and link the
performance notes it mentions). L601 "the same `SSQL_MODE=typed` you've
used all tutorial" is false (first mention); L594 uses `-mode typed`
which is the default. L603 nonexistent path. L612-617 Tier 3 list is
stale (§2.3) and "Tier" is jargon. L490-492 bench comment ("six
benchmarks, ~5 min") disagrees with the regex (two) and with typed-
reference ("~2 min"). Step 4: the L343-345 comment describes two passes,
the code does one; `DeptStats` (L334) is unused; `NewCounter` /
`GroupByParallel` never appear. L5 "every program is a complete file" is
untrue for Steps 5-7. Move the `SSQL_MODE=typed` parallel section
(L582-628) into the TOC as Step 9 before the cheat sheet. Replace the
benchmark table (L52-56, L605-608) with a link to performance.md. Trim
"Where to next" (L626-636, stale Tier list and PoC links) to one design-
notes line.

**`typed-reference.md`** RESTRUCTURE. Delete the Status box (L7-15,
"Phase 1.5 … Arrow is the next major addition"; Parquet, TSV, Stream,
window, rollup, ASOF and set ops have all shipped since). Move the
Roadmap (L627-765, 18% of the file, phase-by-phase ship history with a
second copy of two benchmark tables) to `doc/research/` next to
`typed-package-proposal.md`, keeping only "Still record-only" (L755-762)
as a short "What falls back to Record" section. Before moving it, lift
the APIs that are documented **only inside the Roadmap** into proper
sections: `Strict()`/`CSVOption`, `HashJoinSized`, `SortBy` /
`SortByDesc` / `SortByStable` / `SortByFunc`, `Distinct`, `Concat`,
`Union`, `Window` (`WindowClause`, `WindowSpec`, `WindowFrame`,
`WindowKind`, `WRowNumber`). Add the missing `Stream[T]` section
(`Parallel`, `Stream.Where`, `Serial`, `ReadCSVParallel` signature).
Fix L159 `ReadCSV` signature (omits `opts ...CSVOption`). Move "Text
lines" (L768-782) up from after the Roadmap. Replace the 85-line
Performance section (L29-113) with a two-line summary and a link to
performance.md; fix the conflicting numbers (§2.4). Strip the version
and date stamps (L58, L63-64, L136, L146-148, L401-402, L448-458) and
the DFC IDs (L336, L424, L760). L71-72 "~600 LOC" → those files are
1,337 lines now; drop the number.

### 3.4 Reference

**`api-reference.md`** RESTRUCTURE. 3,060 lines assembled by accretion.
Coverage Check 11 (`scripts/api-coverage.sh`) only asserts each export
is *mentioned*, so the recent sections (L349-383, 589-622, 1004-1023,
1559-1595, 1642-1659, 1794-1814, 1877-2002) are signature dumps that
pass it; the restructure must keep every name mentioned or doc-check
fails. Check 11 also lets `SortRecords` and the `Mode` aggregate through
without a signature (they appear only in prose or as a struct field).

Wrong signatures and structs (verified against `go doc`):

| Line | Doc | Actual |
|---|---|---|
| 376-377 | `Record.TimeSeq(field) (iter.Seq[time.Time], bool)`, same for `BoolSeq` | setters: `TimeSeq(field string, value iter.Seq[time.Time]) Record` |
| 264-268 | MutableRecord `Int8Seq`, `Int16Seq`, `Int32Seq`, `UintSeq`, `FloatSeq`, `Float32Seq`, `SetAny` | none exist; real: `IntSeq`, `Int64Seq`, `Float64Seq`, `StringSeq`, `RecordSeq`, `TimeSeq`, `BoolSeq`, `Null`, `Rename`, `Delete` |
| 318-334 | `Value` allows `iter.Seq[int8…float32]`, "any numeric iterator" | only `iter.Seq[int|int64|float64|bool|string|time.Time|Record|any]` |
| 1185 | `type JoinPredicate func(left, right Record) bool` | interface with `Match(left, right Record) bool` |
| 1462 | `type AggregateFunc func([]Record) any` | returns `AggregateResult` (sealed interface) |
| 1489-1490 | `Min/Max[T cmp.Ordered]` | `[T OrderedValue]` |
| 1645 | `func (a AggResult) GetValue()` | generic `AggResult[V Value]`; `Welford` receivers are values, `Add`/`Merge` missing |
| 1754 | `Materialize(field string)` "converts to []string" | `Materialize(sourceField, targetField, separator string)` adds a joined-string field |
| 1760 | `MaterializeJSON(field string)` | `MaterializeJSON(sourceField, targetField string)` |
| 1821 | `TailTSVFile(filename string, n int)` | missing `config ...CSVConfig` |
| 2186-2194 | `CSVConfig{FieldsPerRecord, LazyQuotes, TrimLeadingSpace}` | none exist; real: `HasHeaders`, `TypeOverrides`, `DefaultType`, `InferRows` |
| 2355-2359 | `CommandConfig{SkipLines, MinColumnWidth}` | real: `HasHeaders`, `TrimSpaces`, `SkipEmpty`, `HeaderPattern` |
| 2704-2727 | `ChartConfig` omits `XField`, `YFields`, `ZField`, `ColorField`, `ColorScale` | the examples at 2778-2813 use exactly those |
| 2822-2833 | `ExploreConfig.SsqlWasmJS` | does not exist; missing `FsPolyfillJS`, `SsqlUIJS`, `WasmBinary`, `AllowEmpty`, `Version` |
| 941 | "All 15 / 13 window functions" | 20 `W*` constructors exist |
| 1837 vs 2267 | `ReadLines` yields `line_number`+`line` vs only `line` | one of them is wrong; document once |

Non-compiling examples: L708-710 and L2019 (`ReadCSV` returns `(seq,
error)`); L2077, L2230, L2333 (`ReadCSVSafe` / `ReadJSONSafe` /
`ExecCommandSafe` return one `iter.Seq2`, not `(x, err)`); L1250-1253
(missing trailing comma before `)`); L2022/2025, L2426/2437, L2448/2451
(`:=` redeclared in one block); L1709-1724 (DotFlatten/CrossFlatten
comments show renamed fields; `go doc` says names are kept).

Structure: TOC links `#window-operations` (no such heading) and omits
three sections; "Installation & Setup" (L51-150) duplicates install.md
and contradicts it on apt (L67-69 vs install.md L14-16); "Go 1.21+" at
L55 vs "1.23 or higher" at L87; duplicated sections (MutableRecord
L226-271 and L448-472; ReadLines L1832 and L2263; JSONL wire format
L1844-1875 and L1877-1901; Best Practices L3037 and L3046); misfiled
sections (UnpivotRecords inside the SQL-Style heading L1118-1139;
SortRecordsSpill between LookupJoin and AsofJoin L1297-1321; Pivot and
Resampling between set ops and GroupBy L1404-1426; CompareAny under
window functions L1025-1035; `DefaultXLSXConfig` under Chart L2693;
Tables/Catalogs/Remote under I/O L1969-2002). Maintainer content: eight
DFC references, "shared by exec and generated code so the lanes cannot
disagree" (four places), performance anecdotes (L362-363, L975), and
CLI-internal exports (`ProcessCatalogShards*`, `BuildRemoteCommand`,
`RemoteBinPrologue`, `SelfBin`, `SplitOnPlus`, `ShellQuote`,
`IsLocalHost`, `CompileAggExprPatched`, `ExprFieldName`, `Must*`,
`ParseFloat64`) documented as user API. Version-history notes at L1839
and L2108-2113.

Proposed target structure (17 sections; keeps every export mentioned):
1 Overview → 2 Core types (Record, MutableRecord, Schema, Value,
JSONString, Times, Filter) → 3 Record helpers → 4 Creating and consuming
iterators → 5 Transform and filter → 6 Limiting and sampling → 7
Ordering (incl. SortRecords, SortRecordsSpill, MergeSorted) → 8 Grouping
and aggregation (incl. Mode, Rollup, Describe) → 9 Reshaping → 10 Joins
and set ops → 11 Window and time → 12 Composition → 13 I/O (one JSONL
section incl. the schema header) → 14 Signal → 15 Charts and Explorer →
16 Error handling (once) → 17 Appendix: CLI/codegen-support exports.

**`EXPRESSIONS.md`** FIX. `-expr` → `-if-expr` (§2.2). L824 "combine
with `-if` conditions using OR" → conditions in a clause AND; OR is
between `+` clauses. L799 "`+` (OR), `-` (exclusive OR)" → in `update`
both start a new clause and the first match wins. L814-815 `-set-expr
category 'minor'` sets the field `minor`, not the string; use `-set` or
`'"minor"'`. L522 `startsWith(email, …)` contradicts the doc's own L91
note that these are operators. Rewrite Error Handling (L843-868): eval
errors fail the pipeline; non-boolean `-if-expr` is a runtime error.
L611 "10-100× faster" is unsupported (the Performance section says 19×).
Drop "(v4.57.0+)" markers, DFC129 (L275), the dfc134/dfc135 links
(L438-441), the paper link (L639-640), the "Implementation Details"
research links (L882-885), and the lanes/Tier/fallback vocabulary
(L648-653, L761-764). The `bucket` SQL-translation paragraph (L265-285)
is `generate sql` detail and could shrink.

**`library-tour.md`** FIX. Sixteen unclosed `**` (L97, 161, 215, 268,
324, 339, 346, 353, 408, 439, 452, 467, 490, 529, 538, 545) break the
rendered page. L91 "**Or use the CLI:**" is followed by nothing. L336
`correlate -with template.csv`. L349 GPU thresholds "FFT ≥ 1024,
kernels ≥ 64" → source says ≥ 16K samples and ≥ 16-point kernels. L543
`-expr`. L534 "~1 ms for 1 M records" vs L547 "~1-2 µs per record"
(which is 1-2 s). L446 hard-coded "12 optimization rules". L498/509
steer users to `cmd/ssql/lib/runtime.MustCompileExpr`, a CLI-internal
package; use expr-lang with `ExprFieldShadowing`/`ExprDate` from the
root package, or label it internal. L572-578 "run these examples" needs
a repo checkout; say so. Cut the Distributed Processing block (L406-448,
pure CLI) and the Expression Support block (L450-564, repeats
EXPRESSIONS.md) to one-line links so the tour stays library-only.

**`performance.md`** FIX and make it the single benchmark source (§2.4).
L93 `-typed`. L118 "As of v4.40". L138-146 Tier 3 / Phase B / "silent
alias" paragraph (§2.3). L174-177 `generate ssql` framing and "(default
since v4.37.3)". L62 "~600 LOC". Name the machine for the cube table
(L7-46) and the 3-join table (L55-59). Cut the Go API walkthrough
(L48-91, `Senior { … }`) to a link; it duplicates typed-codelab. Missing
blank line before the L48 heading. L180 links two research proposals;
one design-notes line is enough.

### 3.5 Using an LLM

**`ai-code-generation.md`** FIX; stays at this path (consumed by
validate-docs.sh checks 1 and 3-8, doc-test.sh, doc-verify.sh, test-ai-
prompts.sh). An LLM copies these verbatim, so wrong signatures ship into
users' programs: L100 `data, err := ExtractSignalFromWAV(…)` (returns
`(Signal, *WAVMetadata, error)`; `ReadWAV` is the record reader);
L117-118 `WAVMetadata{Channels}` and `WriteWAV(signal, file, metadata)`
(real: `NumChannels`; `WriteWAV(records, filename, sampleRate int)`);
L942-943 `config.LogX/LogY` (real: `XAxisType`/`YAxisType`); L945
`EnhancedChart(records, file, config)`, L951 `HeatmapChart(records, x,
y, z, file, cfg)`, L959 `DataExplore(records, file, cfg)`, L967
`AnimateChart(records, file, cfg)` all have the wrong argument order
(real: `(sb, config, filename)` / `(records, config, filename)`, fields
inside the config). L1012-1014 lists `parallel` as a live mode and omits
`generate go -pipeline '…' -mode record|typed`. Missing: `AsofJoin`,
`ReadParquet`/`WriteParquet`, `ReadLines`, `ReadWAV`, `Tee`, and any
mention of `ssql/typed` (which `generate go` now emits by default).

**`ai-cli-generation.md`** FIX; stays at this path (`make ai-test-cli`).
The most user-valuable prompt and the one with the most runtime-wrong
content (§2.2 rows for `to json`, `to chart`, `correlate`, `distinct`).
L162/L182-196 say a raw CSV on the right of `join` "FAILS at runtime"
while Examples 3 (L398) and 7 (L445) use exactly that (and it works:
`readAuxInput` reads `.csv` directly). Example 3 (L399-400) names the
aggregate `total` then sorts on `amount_sum`; Example 8 (L461-462) the
same with `revenue_sum`. L280 calls `… | ssql to table | ssql generate
go` an anti-pattern; `generate go -help` does exactly that. Only the
`export SSQL_MODE=record` ceremony is taught (L260-281, L504, L519);
`generate go -pipeline '…' -mode record` never appears. The command
table (L41-78) has ~20 of ~40 commands; absent entirely: except,
intersect, window, pivot, unpivot, top, count, describe, extract, fill,
sample, tee, resample, merge, run; join `-type`, `-asof`, `-suffix`;
`-spill`, `-presorted`, `-param`, `-if-field`, `-set-field`, `-not`;
`from xlsx|parquet|lines|wav|ssh`; `to jsonl|tsv|xlsx|parquet|markdown|
explore|animate`; `generate json|sql|ssql`.

**`ai-human-guide.md`** FIX and shrink to ~100 lines; stays at this path
(validate-docs.sh check 1 requires it; doc-test.sh and doc-verify.sh
read it). Wrong tool commands: `claude-code` (L55, 61, 125; the binary
is `claude`), `npm install -g @google/generative-ai-cli` (L165-166; the
package is `@google/gemini-cli`, command `gemini`). L73-122 is a heredoc
containing a nested ```` ```bash ```` fence, which closes the outer
block early. L24 "copy the code block starting with 'You are an expert
Go developer'" points at a 5-line block; the whole file must be pasted.
It never mentions the CLI prompt. L600-615 "Success Stories" (Sarah,
Mike, Lisa) are fiction. L231-234, L551-555 "real-time alerting"
templates are for features ssql does not have. L630 links the maintainer
README. Target shape: which prompt to paste (CLI vs Go), how to paste it
into each tool, the verification checklist, troubleshooting.

**`AI-PROMPT-README.md`** MOVE → `doc/research/` (merge into DFC042
`ai-prompt-system.md`, which covers the same system). It describes the
maintainer test loop, not something a user does. Stale: "20 structured
tests" (L111, L200; there are 30), "21 data commands" (L225; ~40), "7
examples" (L219; 8), CLI-09 described as `SSQLGO=record` (L140; the case
uses `SSQL_MODE`), version table ends 2026-01-28. Fold its L21-47 "which
prompt, how to paste" into ai-human-guide. **validate-docs.sh:65 lists it
as a required file; edit in the same commit.**

**`ai-test-cases.md`** MOVE → `doc/research/` (or a testdata dir); it is
script input (`test-ai-prompts.sh:39`). Fix while moving: CLI-06 (L441)
accepts raw `join customers.csv` which the prompt says fails (the prompt
is wrong, not the case); CLI-09 (L517-520) forces the `export` form and
would reject `-pipeline … -mode record`; CLI-12 (L736) expects `.html`
and so rewards the invalid positional chart filename. No cases for
except, intersect, `join -asof`, `sort -spill`, window, run, generate
json, `-param`, `-if-field`.

**`ai-test-results.md`** REMOVE. Script output (`test-ai-prompts.sh:43`)
claiming 30/30 on 2026-01-29; both prompts have been edited since
(2026-09-20, 2026-09-27) and the later fix-request shows CLI-05 failing.
Point `RESULTS_FILE` at the results directory under `/tmp` and gitignore.

**`ai-fix-request.md`** REMOVE. Script output (`test-ai-prompts.sh:44`,
L343-405) dated 2026-04-22, malformed (empty failure ID at L19, empty
prompt at L21, broken path `/tmp/ssql-ai-test-results/.sh` at L28, raw
pipe-delimited record at L30), containing an absolute home path (L5,
L13). The writer has a parsing bug worth a TODO. Point
`FIX_REQUEST_FILE` at `/tmp` and gitignore. doc/README L41 calls it a
"template", which it is not.

**`ai-prompt-improvements.md`** MOVE → `doc/archive/`. Historical
analysis from 2026-01-22; every "High Priority" item is already in ai-
code-generation.md (anti-patterns L373, parameterless Count L206,
namespace matching L399, import path L55); its line references point at
an older layout; L340 proposes a `-v2.md` that never existed. Nothing
reads it.

### 3.6 Internal

**`VALIDATION.md`** MOVE → `doc/research/` as contributor documentation.
Lists 10 of the 11 doc-check checks (missing 9 DFC metadata, 10 release
pins, 11 API coverage; its items 4-6 are all script check 4); stale
example output (L53-54); Level 2 list omits the codelab section; shows a
`.github/workflows/ci.yml` and a weekly audit workflow (L207-220,
L346-410) that do not exist (only playground.yml and release.yml, neither
running `make doc-*`); pseudo-bash threshold at L302 that is not in the
script. Nothing reads it. Fix the check list and delete the fictional
YAML when moving; or fold the accurate half into `claude/` and drop it.

**`codelab-data/README.md`** KEEP. Drop "(DFC125)" at L24 (this file is
written onto users' disks by `ssql codelab`). L3 could mention that
`signal.csv` also serves cli-signal-processing.md.

**`doc/archive/`** (9 files, 2025-era plans and the pre-v4 tutorial).
Nothing in `doc/` outside research links to any of them; doc/README L49
mentions the directory in prose. Leave as is; it is already the place
for history. `scripts/test-ai-code-generation.sh` and `scripts/test-ai-
generation.sh` depend on a `test-output/` directory that no longer
exists and on `test-ai-generation-cases.md` (archive only); they are dead
and can be deleted in the same sweep (TODO entry).

## 4. Proposed shape of `doc/` after cleanup

```
doc/
  README.md                 index (restructured, §3.1)
  install.md                incl. playground-first
  cli-codelab.md            step 1
    cli-codelab-serve.md      sub-page
    tmux-for-ssql.md          sub-page
    cli-shell.md              key-binding reference
  cli-signal-processing.md  step 2 (optional branch)
  codelab-intro.md          step 3
  typed-codelab.md          step 4
  api-reference.md          reference (restructured, §3.4)
  typed-reference.md        reference (restructured, §3.3)
  EXPRESSIONS.md            reference
  cli-troubleshooting.md    reference (absorbs cli-debugging)
  performance.md            background: the single benchmark source
  library-tour.md           background: library showcase
  ai-human-guide.md         using an LLM: which prompt, how (~100 lines)
  ai-cli-generation.md      the CLI prompt
  ai-code-generation.md     the Go prompt
  codelab-data/             fixtures + README
  archive/                  unchanged

doc/research/
  + ai-prompt-system.md     absorbs AI-PROMPT-README
  + ai-test-cases.md        test input (script path updated)
  + validation.md           contributor doc (renamed lowercase, DFC-numbered on move)
  + typed-roadmap.md        the Roadmap lifted out of typed-reference (or folded into typed-package-proposal.md)

doc/archive/
  + ai-prompt-improvements.md

deleted: cli-debugging.md, ai-test-results.md, ai-fix-request.md
```

Whether the moved research files get DFC numbers: `dfc.py` Check 9
requires a metadata block on every `doc/research/*.md`, so each gets one
on arrival (new number, `Created:` = move date, a line saying where it
came from). Pre-DFC files keep their filenames per convention, but these
are new arrivals so lowercase `dfcNNN_` names are fine.

## 5. Edits that must travel with a MOVE or REMOVE

| Change | Consumers to edit in the same commit |
|---|---|
| move `AI-PROMPT-README.md` | `scripts/validate-docs.sh:65` (required-file list, check 1); `doc/README.md`; `ai-human-guide.md:630` |
| move `ai-test-cases.md` | `scripts/test-ai-prompts.sh:39` (`TEST_CASES`); `doc/README.md` |
| remove `ai-test-results.md` | `scripts/test-ai-prompts.sh:43` (`RESULTS_FILE` → `/tmp/ssql-ai-test-results/`); `.gitignore`; `doc/README.md`; `AI-PROMPT-README.md:201` |
| remove `ai-fix-request.md` | `scripts/test-ai-prompts.sh:44` (`FIX_REQUEST_FILE`) and the `--apply` reads at :421/:430; `.gitignore`; `doc/README.md` |
| move `ai-prompt-improvements.md` | `doc/README.md` |
| move `VALIDATION.md` | `doc/README.md`; `CLAUDE.md` mentions `make doc-check/doc-test/doc-verify` directly, no link to fix |
| remove `cli-debugging.md` | root `README.md:87`; `doc/README.md:31`; `cli-codelab.md:865` (already wrong, §3.2); add it to the `old_files` tombstone list in `scripts/validate-docs.sh` check 2 so it cannot come back |
| move typed Roadmap | `typed-codelab.md:626-636` links; `performance.md:180`; anchor `#5d-parallel-mode-codegen-ssqlgoparallel` into `typed-codegen-proposal.md` |
| api-reference restructure | `codelab-intro.md` anchors L539/611/676 and the eight that currently resolve; `typed-reference.md` and `library-tour.md` anchors into it; Check 11 must stay green (`make doc-check`) |

`validate-docs.sh` check 2 is a tombstone list: it fails if any of five
long-deleted 2025 docs reappears. Add every file this cleanup removes
from `doc/` (cli-debugging, ai-test-results, ai-fix-request) and every
file it moves out (AI-PROMPT-README, ai-test-cases, ai-prompt-
improvements, VALIDATION) to that list, so a stale branch or an old
script cannot recreate them.

## 6. Execution plan

Ordered so each batch is one reviewable commit and the gates stay green
throughout (`make doc-check`, `make doc-test`, `go test ./cmd/ssql -run
'Codelab|Doc'`).

**Status (2026-09-29):** Ross decided the §7 points (1 remove-and-merge,
2 new research doc, 3 `claude/doc-validation.md`, 4 strip, 5 yes but
deferred to batch 5 because a signature-line check would flag 69 root +
34 typed exports today, 6 `scripts/testdata/`). Batches 1, 2 and 3 are
done (commits on main the same day); 4-6 remain. Batch 3 found that the
0.15 s / 0.32 s "conflict" was two machines (the 275HX laptop and the
Xeon 6154 workstation), not two values: re-measured on the laptop the
projected group-by is 0.13-0.17 s and the cube 0.28 s; the 1 M-row
codegen table was re-measured too (typed is now the planner-parallel
program: 0.14 s, not the 0.77 s serial figure from April). The 32-core
machine of the parallel-vs-serial table could not be identified and is
labelled as such.

1. **Removes and moves** (§3.5 AI family, VALIDATION, cli-debugging
   merge into troubleshooting), with the script edits in §5, the
   `.gitignore` entries, and `doc/README.md` restructured (§3.1). One
   commit; largest visible change, smallest risk of breaking content.
2. **Cross-cutting text fixes** (§2.2 wrong flags, §2.3 stale advice,
   §2.5 DFC/version/jargon strip) across EXPRESSIONS, library-tour,
   performance, typed-codelab, codelab-intro, cli-codelab, cli-signal-
   processing, cli-serve, cli-shell, tmux, codelab-data/README. Re-run
   `make doc-test` because cli-signal-processing's loop fix changes
   runner-executed blocks; strengthen that block's oracle so a phantom
   row would fail it.
3. **performance.md as single source** (§2.4): re-measure the two
   conflicting rows on a named machine, then replace copies elsewhere
   with links.
4. **typed-reference restructure** (§3.3): lift Roadmap-only APIs into
   sections, add `Stream[T]`, move the Roadmap out, fix anchors.
5. **api-reference restructure** (§3.4): fix every signature and example
   first (each one is a small, checkable diff), then reorder into the
   17-section layout, then fix inbound anchors from codelab-intro,
   typed-reference and library-tour. Consider making Check 11 assert a
   signature line (`func Name` or `type Name`) for every export rather
   than a bare word match, so this cannot drift back.
6. **ai-* prompt content** (§3.5 FIX items): the two prompts and the
   shrunken human guide; then run `make ai-test` once to see the new
   pass rate, and add ai-test-cases for the new commands.

Batches 1-2 are an afternoon. 4 and 5 are each a session. 6 depends on
an LLM run and is the least mechanical.

## 7. Open points for Ross

1. **cli-debugging.md: remove, or keep as a short jq-users page?** The
   recommendation is remove-and-merge; the counter-argument is that
   "ssql + jq" is a real workflow for people who already know jq. If
   kept, it needs a full rewrite around `to jsonl | jq`, not a patch.
2. **Where does the typed Roadmap go?** New research doc vs folded into
   `typed-package-proposal.md`. Recommendation: new doc (the proposal is
   2026-04 and already long).
3. **VALIDATION.md: research doc, or `claude/`?** It is contributor
   process, which is what `claude/` holds. Recommendation: fold the
   accurate half into a `claude/doc-validation.md` and delete the rest;
   `make doc-check` is already described in CLAUDE.md.
4. **Emojis in codelab-intro.** Taste call; the rest of `doc/` is plain.
5. **Check 11 strengthening** (signature line, not word match). Small
   script change; recommend yes, in batch 5.
6. **`ai-test-cases.md` destination**: `doc/research/` (readable history)
   or `scripts/testdata/` (it is test input). Recommendation:
   `scripts/testdata/ai-test-cases.md`, since nothing about it is a
   design discussion.

## 8. Related

- [DFC125](./dfc125_codelab_guided_path.md): the codelab guided path
  and its runner (`make doc-test`), which keeps the four codelabs green.
- [DFC042](./ai-prompt-system.md): the AI prompt system that
  `AI-PROMPT-README.md` duplicates.
- `doc/research/multimode-equivalence-testing.md`: why oracles must be
  equality, not substring (the signal-processing phantom-row bug passed
  the runner for the same reason `top` passed the corpus).
- Journal 2026-W39 (2026-09-28): the README cut to 95 lines that moved
  `cli-shell.md`, `library-tour.md` and `performance.md` into `doc/`,
  which is where the duplicated paragraphs in §3.2 came from.
