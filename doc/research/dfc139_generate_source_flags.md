# Where `generate`'s Source Flags Belong: `-pipeline`, `-script`, `-json` and `-mode`

Reference: DFC139
Created: 2026-09-29
Last modified: 2026-09-29

[Back to Index](./README.md)

Status: **problem statement and options, for a decision. Nothing built.**
Ross, 2026-09-29: "do you think `-script`, `-pipeline` and `-json`
should be options of `ssql generate` and not `ssql generate go|…`, as
it's valid for all forms?"

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

`ssql generate sql -script q.ssql` is refused for no reason anyone
decided; nobody added it when `-script` grew on `go`. That is the
DFC115 pattern (a grammar repeated is a grammar that drifts), in
miniature.

`-run` is NOT part of the problem: it exists on `go`, `sql` and `ssql`
with a different meaning on each (compile and execute; feed the SQL to
the dialect's CLI; execute the optimised pipeline). It stays per target.

## 2. What autocli does today

Flags declared `Global()` on the **root** command (`ssql`) are inherited
by every subcommand: `parseSubcommand` builds a temporary command from
`rootGlobalFlags() + leaf.Flags` so a root global may be written after
the subcommand word (`ssql where -complete 3 …`); completion, help and
`-spec-json` merge the same list. An **intermediate** command's flags
(`generate`'s own, if it had any) are not inherited by its nested
subcommands, and whether the parser accepts `ssql generate -pipeline X
go` (a flag between the parent and the leaf) is untested.

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

Touches: autocli `parseSubcommand`, the completion merge points (four
call sites in `completion_script.go`), `help.go`, `spec_json.go`, plus
a test that an intermediate global shows in a nested leaf's parse,
help, completion and spec; then ssql declares the four flags on
`generate` and deletes the per-target copies. About half a day, one
autocli minor version.

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

## 7. References

- [DFC115](./dfc115_commands_are_the_authority.md) — one grammar, one
  place.
- [DFC134](./dfc134_pipelines_as_data.md) §5.2 — why one spelling per
  thing (`-arg`).
- `cmd/ssql/commands/pipeline_doc.go` (`generateFragmentSource`,
  `jsonDocFlag`), `generate_go.go`, `generate_sql.go`,
  `generate_ssql.go`, `generate_json.go`; autocli `subcommand.go`
  (`rootGlobalFlags`, `demotedRootGlobalFlags`), `parser.go`
  (`parseSubcommand`).
