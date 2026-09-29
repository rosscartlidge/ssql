# Where `generate`'s Source Flags Belong: `-pipeline`, `-script`, `-json` and `-mode`

Reference: DFC139
Created: 2026-09-29
Last modified: 2026-09-29

[Back to Index](./README.md)

Status: **option C built 2026-09-29 as the half-way step (§7); A or B
still for a decision.** Ross, 2026-09-29: "do you think `-script`,
`-pipeline` and `-json` should be options of `ssql generate` and not
`ssql generate go|…`, as it's valid for all forms?" and, after the
autocli deep dive in §2: "as a half-way step we ensure consistency
where possible between the child flags now."

## 1. The problem

`ssql generate` has four targets: `go`, `sql`, `ssql`, `json`. Each
reads code fragments and renders them. The fragments come from one of
four sources, and the source is the same idea whichever target is
chosen:

| Source | Meaning |
|---|---|
| stdin (default) | the fragments an `SSQL_MODE=…` pipeline wrote |
| `-pipeline 'ssql … \| ssql …'` | run that pipeline text under `-mode` and take its fragments |
| `-script FILE` | the same, from a `.ssql` script file |
| `-json FILE` | run a pipeline document (as `ssql run` does: no shell, validated) and take its fragments |

The semantics are in one place already: `generateFragmentSource`
(`pipeline_doc.go`) resolves the three flags for every target, and
`jsonDocFlag` declares `-json` for each. But the **grammar** is
declared per target, and it has drifted:

| Flag | `go` | `sql` | `ssql` | `json` |
|---|:-:|:-:|:-:|:-:|
| `-pipeline` | ● | ● | ● | ● |
| `-json` | ● | ● | ● | ● |
| `-script` | ● | ○ | ○ | ○ |
| `-mode` | ● | ○ (always record) | ○ | ○ |

(The state on 2026-09-29 before §7; every cell is ● now.)

`ssql generate sql -script q.ssql` is refused for no reason anyone
decided; nobody added it when `-script` grew on `go`. That is the
DFC115 pattern (a grammar repeated is a grammar that drifts), in
miniature.

`-run` is NOT part of the problem: it exists on `go`, `sql` and `ssql`
with a different meaning on each (compile and execute; feed the SQL to
the dialect's CLI; execute the optimised pipeline). It stays per target.

## 2. What autocli does today (measured, v4.20.0)

Read from `parser.go` (`ExecuteWith`, `parseRootGlobalFlags`,
`parseSubcommand`) and `subcommand.go` (`rootGlobalFlags`,
`demotedRootGlobalFlags`), then confirmed with a probe program: a root
global `-verbose`, a parent `gen` declaring its own global `-pipeline`,
leaves `gen go` / `gen sql` declaring `-run`.

| Spelling | Result |
|---|---|
| `app gen go -run` | runs the leaf; `-run=true` |
| `app gen go -pipeline X -run` | **`flag -pipeline: unknown flag`** — the parent's flag is not visible in the leaf |
| `app gen -pipeline X go` | **prints `gen`'s help** — the tree walk consumes subcommand NAMES only; a flag stops it at `gen`, which has no handler, so it shows help; `go` is never reached |
| `app gen -pipeline X` | prints `gen`'s help (same reason) |
| `app gen go -verbose`, `app -verbose gen go` | both run; the root global is accepted before or after the leaf |
| `app gen go -help` | lists `-run` only: neither the root global nor the parent's flag |
| `app -complete 3 gen go -` | offers `-run`, `--help`; the root global appears only on its own prefix (`-v` → `-verbose`, the demoted rule); the parent's `-pipeline` never |
| `app -complete 2 gen -` | offers `-pipeline` (the parent's own completion works at the parent) |
| `-spec-json` | `-verbose` on the root node, `-pipeline` on `gen`, `-run` on each leaf: a flag lives on exactly the node that declared it |

So, today: a parent's flag is parsed **only** when the parent is the
leaf being executed (which `generate` never is, having no handler), it
is not inherited downward, and a flag placed between the parent and the
leaf ends the subcommand walk. Root globals are the one inherited kind:
parsed before the walk (`parseRootGlobalFlags`) or after the leaf (the
temporary command in `parseSubcommand` is `rootGlobalFlags() +
leaf.Flags`), offered by completion in demoted form, absent from leaf
help, and present once in `-spec-json` on the root.

Consequences for the options: **A needs a parser change** (flags
between parent and leaf are not parsed at all), so "just move the
flags" is not a cheap option; **B is a change to one list** in four
places (`parseSubcommand`, the completion merges, help, spec) from
"root globals" to "every ancestor's globals", with the demotion
question in §3.B; **C needs nothing** from autocli.

The probe (`main.go`, 40 lines) is the natural first test of B: the
same eight spellings with the expected results flipped for the parent
flag.

## 3. Options

### A. Move the flags to `generate`

`ssql generate -pipeline '…' -mode typed go`. One declaration. The
spelling changes for every user: the source comes before the target and
the target is the last word, which reads back to front (what you want
should come before where it comes from), and every README, codelab,
key-binding (`Alt-g`, `Alt-r` build `generate go -run -pipeline`) and
DFC example changes with it. Needs a check that autocli parses flags
between parent and leaf at all. **Not recommended on spelling alone.**

### B. autocli inherits ancestor globals

Extend `rootGlobalFlags` to walk every ancestor: a `Global()` flag
declared on `generate` appears in `generate go|sql|ssql|json`, parsed
after the leaf word as today's spelling has it, listed in each leaf's
help, offered by completion, present in `-spec-json`. One declaration,
no spelling change, and `-script`/`-mode` reach every target the moment
they are declared once. Also makes `ssql generate -pipeline '…' go`
legal as a by-product, which costs nothing.

Touches: autocli `parseSubcommand` (the temporary command's flag list),
the four completion merge points in `completion_script.go`, `help.go`
(leaf help should list inherited flags, which it does not even for root
globals today — a separate small gap), `spec_json.go` (either list the
inherited flags on each leaf, or leave them on the declaring node and
let consumers walk up; the `serve` explorer and DFC134's schema read
`-spec-json`, so this is a real choice), plus the §2 probe as a test;
then ssql declares the four flags on `generate` and deletes the
per-target copies. About half a day, one autocli minor version.

Risk: the demoted-globals rule (root globals are offered only on a
specific prefix so they do not crowd a leaf's `-<TAB>`) must decide
whether an intermediate global is "the leaf's own" (offered broadly)
or "inherited" (demoted). For `generate`, the source flags are what a
user reaches for, so they should be offered broadly; a rule such as
"demote root globals, offer intermediate globals" would do.

### C. Declare once in ssql, no autocli change

A builder helper `pipelineSourceFlags(sub)` applied to all four
targets, the way `jsonDocFlag` already is for `-json`. Fixes the drift
(one declaration, `-script` and `-mode` on every target), no grammar or
spelling change, no autocli change, an hour. The flags still appear
four times in `-spec-json` and help, which is the truth of the grammar
(they are leaf flags), and a fifth target would have to remember the
helper, which is the same drift risk one step removed (a registration
drift test can pin it: every `generate` leaf must carry the source
flags).

### D. Both spellings by design

Do B, and document `ssql generate -pipeline '…' go` as the program-
facing spelling (source first, then target) while people keep typing
`generate go -pipeline`. Two spellings for one thing is what DFC134
§5.2 avoided with `-arg`; I would not advertise the second.

## 4. What `-mode` means on each target

If the flag reaches every target, its meaning must be the same on all:
"the mode the pipeline is run in to produce the fragments". `go` uses
it to choose record or typed codegen; `sql` and `ssql` and `json` read
record-mode fragments today and would accept `-mode typed` only to run
the pipeline that way (the fragments carry typed schemas the SQL and
document renderers ignore). Either refuse `-mode typed` on the three
targets that cannot use it, or accept and ignore with a plan note; I
lean refuse (loud, DFC133).

## 5. Recommendation

**B**, with **C** as the fallback if the autocli change turns out to
have sharp edges in completion. Either way the drift is closed and
`-script` works on every target; B additionally stops the next
`generate` flag from being declared four times.

## 6. Open points for the decision

1. A or B: is the source-first spelling (`generate -pipeline X go`)
   wanted as the primary form? If yes, A; if the current spelling stays,
   B or C.
2. Whether intermediate globals are offered broadly on `-<TAB>` or
   demoted like root globals (B only).
3. `-mode` on non-Go targets: refuse `typed`, or accept and ignore.
4. Whether `-run` should also be looked at while here (its three
   meanings are three flags sharing a name).

## 7. Built: option C, the half-way step (2026-09-29)

`pipelineSourceFlags(sub, verb, modeDefault)` in `pipeline_doc.go`
declares `-pipeline`, `-script`, `-json` and `-mode` once, with one help
text per flag parameterised by what the target does with the fragments;
all four targets call it (`jsonDocFlag` and the per-target copies are
gone). `TestGenerateTargetsShareSourceFlags` reads `-spec-json` and
asserts every `generate` leaf except `schema` carries all four; watched
to fail with one target's call removed.

What changed for a user:

- `ssql generate sql|ssql|json -script FILE` work (they refused
  `-script` before, by omission).
- `-mode` is accepted on every target. On `sql`, `ssql` and `json` it
  must be `record` (their default); `-mode typed` is refused loudly:
  "has no meaning here (this target reads record-mode fragments)". On
  `go` the default stays `typed`. §4's question is answered: refuse,
  not ignore.
- The four flags read the same in every target's `-help`, in the same
  order, and complete the same.

`generate schema` was first left out on the grounds that it reads a
schema header, not fragments; Ross pointed at its `-help`. The flags
mean the same thing there ("run the pipeline in the mode this target
consumes and read what it writes"), so it has them too, with `schema`
as its fixed `-mode`, and `ssql generate schema -pipeline '…'` lists a
pipeline's output fields with no `export` dance. Making that work
found a gap: in schema mode the `to` sinks ran for real on the header
(no field list after a sink); every sink now passes the schema through.

What did NOT change: the grammar still declares the flags per leaf, so
`-spec-json` lists them on each of the four nodes (true to what autocli
parses), and `ssql generate -pipeline X go` still prints `generate`'s
help (§2). A fifth target would have to call the helper; the drift test
is what makes forgetting loud.

## 8. Way forward

The half-way step removes the user-visible inconsistency and the drift
risk. What remains is a question about where the grammar SAYS the flags
live, and it is now purely a design choice with no bug behind it:

- **Stay here (C).** Nothing more to do. The flags are leaf flags that
  happen to be identical, which is what the parser implements.
- **B, when autocli grows ancestor-global inheritance.** Then the four
  declarations collapse to one on `generate`, `-spec-json` shows the
  flags once on `generate` (a consumer walks up), leaf help lists
  inherited flags (fixing the same gap for root globals), and the
  spelling stays `generate go -pipeline`. Half a day in autocli plus an
  hour in ssql; the §2 probe becomes the test. Worth doing when a
  second parent-with-shared-flags appears (`serve`? `from`?), not for
  `generate` alone.
- **A is off the table** unless the source-first spelling is wanted for
  its own sake: it needs the parser to accept flags between parent and
  leaf, and it changes every documented example.

The decision that remains is therefore B-later versus stay, and it can
wait for a second use case.

## 9. References

- [DFC115](./dfc115_commands_are_the_authority.md) — one grammar, one
  place.
- [DFC134](./dfc134_pipelines_as_data.md) §5.2 — why one spelling per
  thing (`-arg`).
- `cmd/ssql/commands/pipeline_doc.go` (`generateFragmentSource`,
  `jsonDocFlag`), `generate_go.go`, `generate_sql.go`,
  `generate_ssql.go`, `generate_json.go`; autocli `subcommand.go`
  (`rootGlobalFlags`, `demotedRootGlobalFlags`), `parser.go`
  (`parseSubcommand`).
