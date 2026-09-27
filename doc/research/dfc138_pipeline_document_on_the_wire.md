# The Pipeline Document on the Wire: SSH, Catalogs and serve Without Shell Text

Reference: DFC138
Created: 2026-09-28
Last modified: 2026-09-28

[Back to Index](./README.md)

Status: **proposal, for review. Nothing built.** Ross, 2026-09-28: "now
we have the json form of the pipeline — should we consider using that
form to pass between machines when using ssh? bash is great for humans
but can be a minefield for inter-machine communication. We need to make
sure it is easy to run the json in exec mode or as optimised typed go so
we can use the performance advantages." And: "`ssql run -check` could
also check for valid fields using the completion machinery (schema et
al) as well as the syntax."

Builds on [DFC134](./dfc134_pipelines_as_data.md) (the document, `ssql
run`, `generate json`), [DFC093](./remote-go-execution-proposal.md)
(remote Go execution: whatever mode the local pipeline runs in, the
remote runs in too) and [DFC115](./dfc115_commands_are_the_authority.md).

## 1. The problem

A pipeline that crosses a machine boundary is rendered as **shell
text** today, three times over:

1. `from ssh HOST PATH -- STAGE + STAGE …` → `BuildRemoteCommand`
   (`catalog.go`) joins the stages into `ssql from PATH | ssql STAGE |
   …`, every argument through `ShellQuote`, and hands the string to
   `ssh HOST CMD`, where the remote login shell parses it.
2. `from catalog` → `ProcessCatalogShards` does the same per shard.
3. `serve`'s `POST /api/execute` takes `{"pipeline": "<text>"}` and
   splits it with `splitServePipeline`.

The typed remote path is different in kind but the same in spirit:
`RemoteScriptCommand` ships a `.ssql` **script** (pipeline text) over
ssh's stdin and runs `generate go -script … -mode MODE -run` there.

Four things are wrong with shell text as the inter-machine form, and
DFC134 already named the principle they violate:

- **A second grammar.** The `+` between pushed-down stages
  (`SplitOnPlus`) is a pipeline syntax that lives outside the commands,
  and it is weaker than the real one: `join <(…)`, `except -file <(…)`
  and every nested pipeline have no `+` spelling. `+` is also the
  negation prefix (`+if`), so the separator and a flag share a
  character and only position tells them apart.
- **Safety stops at the wire.** Locally an argument is never syntax
  (DFC134 §2). Remotely it becomes text a shell parses, made safe again
  by `ShellQuote` — correct, but exactly the query-builder posture
  DFC134 argues against, and correct only while the remote shell's
  rules match ours (a `csh` login shell, a `fish`, a hardened `sh`
  with `-o posix` differences: the rig has never tried them).
- **Three consumers, three renderers.** ssh, catalog and serve each
  turn a pipeline into text their own way; the injection fuzz of
  DFC134 §6 covers the local argv path and none of these.
- **Nothing is checked before data moves.** The remote pipeline is
  discovered wrong (a field that is not in the remote file's header)
  after the ssh session is up and the file is being read.

## 2. The proposal in one sentence

**The JSON document is the only thing that crosses a machine boundary;
the command that receives it is a constant.**

```
ssh HOST 'SSQL=…; "$SSQL" run -'          ← five fixed words; document on stdin
```

Nothing variable is ever parsed by a shell. The remote `ssql run`
parses the document with its own grammar (the command is the authority
on itself, across machines), checks it, and runs it in the mode asked
for.

## 3. Design

### 3.1 The remote side: `ssql run -` gains `-mode`

`ssql run FILE|-` exists (DFC134 §5.1): exec mode, the interpreter
chain, with `-check` and `-print`. It gains:

```
ssql run - [-mode exec|record|typed] [-check] [-check-fields]
```

- `-mode exec` (default): today's stage chain.
- `-mode record|typed`: `generate go -json - -mode MODE -run`, which
  already exists (DFC134 §5.2a): compile the document to a Go program
  against the remote's module cache and run it. This is the "optimised
  typed Go on the remote" path, and it is one flag on the receiving
  side, so the sender never renders a `.ssql` script again.
  `RemoteScriptCommand` and the `-script` landing retire.
- The document arrives on stdin; the pipeline's own source is a file
  on the remote, so stdin is free. (If a stage ever needs remote stdin
  for data, the document can go as ONE argument instead — a single
  opaque value under `ShellQuote`, not a pipeline rendered as shell —
  `run -doc JSON`. Not needed now.)

### 3.2 The local side: senders build documents, not text

`from ssh HOST PATH -- STAGES` keeps its syntax: bash is for humans,
and a person typing a push-down at a prompt is the reason `--` exists.
What changes is what the sender does with the parsed stages: it builds
the document `[["from", PATH], STAGE, STAGE, …]` from `Op.Argv` (the
same lossless argv `generate json` uses) and sends it. `SplitOnPlus`
stays only as the local parser of the human form; the remote never sees
`+`.

In document form (`generate json`, a program), the push-down is a
**nested pipeline**, which the document already has:

```json
["from", "ssh", "node1", "/data/orders.csv", "--", [["where","-if","status","eq","shipped"],["group-by","dept","-count","n"]]]
```

so `generate json` of a pipeline with a push-down round-trips, and a
program can build a remote pipeline without touching `+`.

`from catalog` builds one document per shard (the `from` stage's path
and format from the shard row, the push-down stages shared) and sends
each to its host the same way; `ProcessCatalogShards` loses
`BuildRemoteCommand`.

`serve`'s `/api/execute` accepts `{"document": [...]}` beside
`{"pipeline": "…"}`; the text form stays for the console (a human
typing), the document is what the explorer and any program send. The
read-only policy applies to stages, not text, so it checks the
document (`serve -readonly` refuses `run`, `to FILE`, … by stage name,
as today).

### 3.3 The prologue

`RemoteBinPrologue` stays: the one shell-facing piece is finding the
remote binary (`/usr/bin`, `/usr/local/bin`, `$HOME/go/bin`,
`$HOME/.local/bin`, or `-remote-bin`), a fixed list of absolute paths
with the one variable under `ShellQuote`. It was already the safe part.

### 3.4 Version handshake

`ssql run` exists since v4.104.0; `-mode` will exist from this unit.
A remote older than that gets the document and fails to parse `run -`
as a command. The sender must make that loud and specific rather than
let it surface as a shell error: on failure of the remote command,
run `"$SSQL" version` and report "remote ssql vX.Y.Z at HOST; this
needs ≥ v4.109.0". No text fallback: a fallback keeps the minefield
alive and untested. The LXD rig (ssql-node1/2/3) can hold one node on
an old version to pin the message.

## 4. `-check`: syntax AND fields, from the grammar

`run -check` validates the document against the grammar (`root.Check`,
autocli v4.19.0): unknown commands, unknown flags, arity. Ross's
extension: check the **field references** too, before anything runs,
using the machinery completion already has. Two parts, both generic
(no per-command code):

1. **Which arguments are field names?** The grammar says so:
   `FieldsFromFlag` marks a flag's argument as a field slot, and
   `-spec-json` exposes that. So a checker can walk each stage's
   parsed arguments and collect the field slots (`where -if AGE …`,
   `group-by DEPT -sum SALARY …`, `join … -using KEY`, `sort FIELD`).
2. **What fields exist at that stage?** The schema rules
   (`schemaOps`, the same ones `SSQL_MODE=schema` and serve's
   `/api/schema-fields` fold a pipeline through): start from the
   source's header (the `from` stage, in schema mode, reads it) and
   apply each stage's rule to get its output fields. `fragmentFields`
   in `join.go` already does this fold for generation-time checks.

Then: for each stage, every field slot must be in the incoming field
list, else `stage 3 (where): field "statuss" not found (available:
order_id, status, amount)` — the exec-time message, before exec. A
stage whose rule cannot predict its output (a `join` with a source the
checker cannot read) ends the walk with "fields unknown from stage N";
syntax is still checked to the end.

Run **on the remote** (`run -check-fields -` over ssh, the document on
stdin) this validates against the remote file's real header before
any data moves, which no amount of local checking can do. `from ssh`
does it as its first step and only then sends the document to run; the
cost is one extra ssh round trip, and it can be skipped with
`-no-check` for the tight loop.

Serve gets it for free: `/api/check` with a document returns the
first error with its stage, which the explorer can show inline.

## 5. What it takes

- `run -mode` (a switch to the existing `generate go -json -run`),
  `run -check-fields` (the fold above; the field-slot walk is new,
  the schema fold exists).
- `from ssh`: build the document from the parsed push-down; send over
  stdin; the version handshake; retire `RemoteScriptCommand`,
  `sshScriptLandingCode`, the `-script` landing. The generated Go
  landing (DFC093: the local program embeds the remote pipeline)
  embeds the document as a `const` and inlines the same ssh-and-run
  helper, so a generated program still orchestrates remotes on its
  own.
- `from catalog`: per-shard documents; `BuildRemoteCommand` retires
  (it stays exported one release as deprecated for anyone who called
  it).
- `serve`: `{"document"}` on `/api/execute`, `/api/check`; the
  explorer sends documents.
- `generate json` / `ssql run`: the `["from","ssh",HOST,PATH,"--",[…]]`
  nested form; `run -print` renders it back to `-- … + …`.
- Docs: codelab §8 (distributed data), api-reference, DFC134 §5.1a
  cross-reference.

Roughly two days; the ssh and catalog halves are one shape.

## 6. Tests

- **Equivalence lanes `ssh-exec` and `ssh-typed`** (gated on
  `SSQL_TEST_SSH_HOST`): the same pipeline run locally and pushed to
  the rig in both modes, byte-identical after normalisation, on the
  shuffled fixtures the other lanes use. Today's ssh tests assert
  substrings.
- **Injection across the wire**: the DFC134 §6 hostile-argument corpus
  (`'`, `;`, `$(…)`, backticks, `--`, `+`, a field named `-desc`, a
  value with a newline) pushed to the rig and back, asserting the
  remote received exactly the local argv (`run -print` on the remote
  echoes what it got). The random tester gains an ssh stage when the
  rig is present.
- **Version handshake**: one rig node pinned to v4.107.0; the error
  names the host and the version.
- **`-check-fields`**: a unit table over documents with a wrong field
  at each stage kind, asserting the stage number and the available
  list; a case where a `join` source makes the fields unknown and the
  syntax check still runs to the end.
- **Login shells**: the rig gives one node a `fish` (or `csh`) login
  shell and the pushed pipeline must still run — the test that would
  have failed under shell text.

## 7. Open points for review

1. §3.1 `run -mode` versus honouring `SSQL_MODE` on the remote (DFC093's
   "whatever mode the local runs in, the remote runs in too"). I lean
   `-mode`: the document is explicit about everything else, and an
   environment variable is one more thing that has to cross the wire.
2. §3.4 no text fallback for old remotes. I lean loud refusal.
3. §4 `-check-fields` as a separate flag or part of `-check`. I lean
   part of `-check` (fields are syntax from the user's point of view)
   with `-check-syntax` for the grammar-only form, since the field
   check needs to read a header and may be slow on a remote object
   store.
4. §3.2 whether `from ssh` runs the remote check before every push, or
   only under `-check`. I lean before every push: the round trip is
   cheap next to reading a file, and an error before data moves is the
   point.

## 8. References

- [DFC134](./dfc134_pipelines_as_data.md) — the document, `ssql run`,
  `generate json`, the injection fuzz.
- [DFC093](./remote-go-execution-proposal.md) — remote Go execution,
  the mode-follows-local rule, the generated landing.
- [DFC115](./dfc115_commands_are_the_authority.md) — why a second
  renderer of the pipeline grammar is the bug, not the cure.
- [DFC136](./dfc136_duckdb_feature_comparison_2026_09.md) §5.2 — safe
  construction as the property DuckDB cannot have.
- `catalog.go` (`BuildRemoteCommand`, `RemoteScriptCommand`,
  `SplitOnPlus`, `RemoteBinPrologue`), `cmd/ssql/commands/run.go`,
  `pipeline_doc.go`, `serve_http.go` — the code this replaces or grows.
