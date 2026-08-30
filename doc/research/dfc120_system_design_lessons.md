# Drift Is the Enemy: System-Design Principles from Building ssql

Reference: DFC120
Created: 2026-08-29
Last modified: 2026-08-30

[Back to Index](./README.md)

**Status:** Conference paper draft — the fundamental system-design
lessons of the project, distilled from the DFC corpus, the journals,
and the CLAUDE.md rules they produced. Companion to
[the AI-assisted development case study](./conference-paper-ai-assisted-go-development.md)
(which covers the *methodology* of building with an AI pair); this
paper covers what the system taught us about *design*, and is meant
to stand alone for readers who have never seen ssql.

## Abstract

ssql is a Unix-style data-processing tool with an unusual property
that makes it a good laboratory: the same user program — a shell
pipeline — runs five different ways (interpreted, three flavours of
generated Go, and generated SQL under DuckDB), completes in three
environments (bash, a browser workspace, SSH), and is manipulated by
multiple user interfaces (terminal, grid gestures, chart controls).
Every one of those multiplicities is a feature, and every one is an
invitation for two implementations of the same fact to disagree.

Across ~120 releases, nearly every serious bug we shipped had the
same underlying shape: **a fact that lived in two places drifted.**
A parser copied, a vocabulary transcribed, a config cached, a
semantics reimplemented "just for the fallback." This paper presents
the principles we extracted — each stated generally, each grounded
in the specific failure that taught it — and the meta-principle they
add up to: system design is largely the art of making drift
impossible, loud, or dead on arrival.

## 1. The laboratory

One pipeline:

    ssql from csv data.csv | ssql top -asc 10 -field name | ssql to table

Five executions: the interpreted CLI; `generate go` in record,
typed, and parallel modes (compiled Go over maps, structs, and a
sharded stream runtime); `generate sql` run by DuckDB. Plus an
optimiser that rewrites pipelines, a completion engine, a WASM build
of the whole tool embedded in a self-contained HTML workspace, and a
server mode that splits pipelines between a data-local host and the
browser. Facts about a command — its grammar, its semantics, its
output schema, its performance shape — are needed by all of them.

That need is where every lesson below was born.

## 2. Every fact wants exactly one authority

**The failure.** Our `serve` HTTP mode needed to know how many rows
a pipeline's source stage would produce. It parsed the stage's
arguments itself — a hand-rolled copy of the `from` command's flag
grammar. That copy took **three drift bugs in one week** (a
subcommand word parsed as a filename, flag values parsed as files, a
new flag it had never heard of). Each fix improved the copy. The
real fix deleted it: the `from` command grew `-records`, a protocol
flag that prints the one number serve needed, computed by the code
that owns the grammar.

The same season: a completion helper kept its own map of which
flags take values — it listed `-sample` for the parquet subcommand,
which is a *csv* flag; the map had already drifted when we found it.
A widget UI carried a hand-typed operator list `eq, ne, gt, …` — the
real command had gained `regex`, which the list never had. A
formats-by-extension switch existed in *twelve* files.

**The principle.** A command (module, service) is the authority on
its own grammar, semantics, and facts. Consumers ask through a
protocol — completion queries, schema mode, a machine-readable spec
dump, `-records` — and never re-implement. When a consumer needs a
new fact, you grow the authority a new protocol surface; you do not
grow the consumer a new parser. Second implementations are not
"probably fine": every one we audited had either already drifted or
did so within weeks.

**The audit is cheap.** Violations have greppable signatures — ext
switches, argv tokenizing outside the parser, hand-kept lists of
someone else's names. A survey (our DFC116) found seven candidates
in thirty seconds of grep; fixing the two worst took a day and
deleted twelve copies of one fact.

## 3. What "asking the authority" looks like, concretely

Abstract principles hide the engineering. These are the actual
protocol surfaces an ssql command exposes — each one a question a
consumer can ask instead of a fact it would otherwise copy:

**"What can go at this cursor position?"** — the completion protocol.
Any consumer (bash, the browser workspace, a future form) sends the
argv and a cursor index; the command's own declaration answers:

    $ ssql -complete 5 from parquet data.parquet -columns ""
    a_kind
    a_name
    relationship
    ...

Those are real column names, read from the parquet footer by the
code that owns the format. Enum answers (where's operators) come
from the same declaration that parses them at runtime — they cannot
be out of date.

**"What does the word under the cursor mean?"** — `-help-at POS
argv…` returns the owning flag's documentation for any position; if
the declaration marks that argument as an expression slot, the
response appends the expression-function reference. One keystroke in
any client, and the help came from the declaration, not a manual.

**"What fields exist at this point in the pipeline?"** — the schema
shadow mode. Set one environment variable and the SAME binaries run
the pipeline as an abstract interpretation: each stage reads a
schema header instead of data, applies its transform to the SCHEMA
(a rename renames, a group-by replaces fields with keys+aggregates),
and passes it on:

    $ (export SSQL_MODE=schema;        ssql from csv d.csv | ssql group-by dept -count n)        | ssql generate schema
    dept
    n

Completion after a five-stage pipeline is exact because the pipeline
itself computed the answer — no consumer models what group-by does
to a schema.

**"How many records would you produce?"** — `from -records` prints
one integer, computed the cheapest way the owner knows (parquet: the
footer; csv: a newline count). Born after a UI's hand-rolled parser
of from's arguments took three drift bugs in a week.

**"Describe your entire grammar."** — `-spec-json` dumps the
declared command tree: flags, arities, per-argument types,
expression marks, and completer *kinds* (static enums inline;
dynamic vocabularies marked so consumers ask the live protocols
above). This is what lets a UI *render* a command without knowing
any command.

**"Generate yourself."** — each command emits its own compiled-Go
and SQL representations as fragments; the code generators assemble
what commands declare rather than reimplementing their semantics.

Two supporting disciplines make the surfaces trustworthy: the
completion library itself asks the host tool (a hook) for anything
it can't derive — the library never grows format knowledge — and
when a protocol can't answer, consumers show an honest, actionable
hint token rather than a guess.

### Could this be adopted widely?

Mostly, yes — the ingredients are ordinary:

1. **Declare once.** A CLI framework where flags, args, docs,
   completers, and semantic marks are one declaration (ours is a
   builder API; Nushell and PowerShell prove the model at
   ecosystem scale). Everything else derives from it.
2. **Meta-flags as protocol.** `-complete`, `-help-at`,
   `-spec-json` are just hidden entry points on the same binary —
   any tool can add them; Cobra's `__complete` already does the
   first.
3. **The shadow schema mode needs one contract**: a wire format
   whose header carries the schema, so each process can transform
   metadata instead of data. Any typed-pipeline ecosystem (or any
   JSON-lines convention with a schema line) can do this; it is the
   piece we'd most like to see standardized, because it makes
   *composition* introspectable, not just single commands.
4. **UI-writes-text needs a lawful model**: a canonical printer, the
   round-trip laws, and the ownership rule for machine-written
   spans. None of it is deep; all of it must be property-tested.

The honest cost: the authority must be *runnable* by every consumer
(our browser runs the real engine as WASM — that decision carries
the whole architecture), and protocols must be versioned with the
same care as any public API.

## 4. Configuration is program text

**The failure, three times.** The browser workspace held state the
pipeline didn't: a widget builder's step list, the grid's
filter/sort gestures, the chart's axis picks. All three failed, each
differently: the builder was a one-way mirror that taught a stale
dialect; the grid ops ran invisibly on stale data while "Copy as
CLI" reproduced a *different* result than the screen; the axis picks
were silently clobbered by a stale JavaScript closure — three
separate stale-closure bugs in one callback neighbourhood, because
every copy of state outside the model needs its own synchronization.

**The principle.** Any state that determines what the user sees or
gets should be expressible in the program text itself — for us, the
pipeline string — because program text is the only state that is
*durable* (it rides links, clipboards, files, and history),
*inspectable* (it can be read, diffed, pasted into a bug report),
and *single-parsed* (one grammar, one meaning). The UI becomes a
view and an editor of that text, never an owner of state: a grid
click literally writes `| ssql sort salary -desc` into the pipeline
bar and reruns it; a chart axis change rewrites the `-x` flag of a
trailing `to chart` stage. When a UI needs state the language can't
express, the correct move is growing the language.

**Bijectivity needs laws, not vibes.** Text↔model sync fails in
exactly the ways two-store sync always fails, so we made the model
lawful: `parse(print(m)) == m` (printing loses nothing) and
`print(parse(t))` idempotent (parsing is canonicalization), with an
opaque-text escape hatch so constructs the model can't represent
round-trip verbatim instead of being dropped or "repaired". The laws
are property-tested against the same tokenizer the runtime uses —
the model *cannot* disagree with execution, because it is not a
second implementation of parsing.

## 5. One semantics, N implementations → differential gates

**The failure.** A `top`-by-string-field operation was fixed in two
of our five execution paths and left silently wrong in the other
three *for two releases*, found only when a user compared outputs by
hand. Our regression tests were green throughout, for two
independent reasons: the oracle was weak (substring `Contains`, not
equality) and the fixture was non-discriminating (already-sorted
data, so "first N" equalled "sorted N").

**The principles.**

- *A test's power = oracle strength × input discrimination.*
  Weakness in either blinds it. Assert byte-identical normalized
  output; use shuffled fixtures with distinct values so a wrong
  answer actually diverges.
- *Differential-test the implementations against each other* — every
  path, one pipeline, outputs must agree exactly (after normalizing
  legitimate differences like column order).
- *Guard the "all agree" oracle with an independent one.* Unanimous
  can still be unanimously wrong: hand-written goldens, plus a
  second engine entirely (DuckDB running our generated SQL) as an
  implementation we didn't write.
- *When you fix a result bug anywhere, assume it lives everywhere*
  until a gate proves otherwise.

## 6. A gate you haven't watched fail isn't a gate

Three times in one week, a freshly written test passed against the
bug it was meant to catch: one read a counter that only a different
code path updated; one used a fixture where the buggy and correct
answers coincided; one asserted around a "graceful" fallback that
made failure look like success. Each time, the practice that caught
it was the same: **reintroduce the bug and watch the gate fail**
before trusting it. Sabotage verification is now as much a part of
writing a test as the assertion is. It costs one extra build cycle
and it has never once been a waste of time.

## 7. Fail loudly; never impersonate

**The failures.** A browser fallback implemented "group-by" in
JavaScript with `parseFloat(x) || 0` coercion — strings silently
became zero: not our semantics, wearing our UI. Worse, a *legitimate*
degraded mode masked a total failure for months: same-command field
completion had silently died (a strict parse discarded everything on
a dangling flag), but the degraded output — a generic hint token —
looked exactly like the intentional cross-pipe fallback, so nobody
filed a bug.

**The principles.**

- Unknown field names, invalid flags, unsupported combinations:
  *terminate loudly with the real vocabulary in the message*
  ("unknown field: nick (available: dept, name, salary)"). Silently
  empty results are how users learn to distrust a tool.
- *Never ship a semantics impostor.* A fallback that approximates
  the real engine is a third implementation that will drift; honest
  refusal ("this needs the engine; grid browsing only") beats a
  confident wrong answer.
- *When a feature degrades to something that looks acceptable, the
  GOOD path needs its own test* — the fallback's plausibility is
  precisely what hides the primary path's death.
- *Truncate visibly.* When we cap a server result at 10,000 rows to
  protect the browser, the cap is appended to the executed command
  and the status line says so. Silent truncation reads as "that's
  all the data."

## 8. A fixture that fits in a pipe buffer tests nothing about scale

Five performance bugs shipped in one week — quadratic re-reads,
accidental full-file scans, a serial path where a parallel one was
intended — all invisible to every functional test, because output
oracles pass regardless of how slowly the output was produced. The
cure is an opt-in scale gate: a cached ~120MB fixture and **absolute
wall-time ceilings** per operation. Generous budgets, deliberately:
they never flake on a busy machine, but a complexity-class
regression (the only kind that matters) blows through a generous
ceiling anyway. Stored baselines rot and ratchet; ceilings encode
"this must never be *architecturally* slower."

Related: *measure through the interface users feel.* A rows/second
display added for demo polish immediately exposed that one common
operation ran serially in the "fast" engine (6.7s vs 1.5s) — no
profiler session ever prompted the question, but a number on screen
did. And interactive behaviour must be tested through the real
interface: three shipped completion bugs passed every simulated-
input test and fell only to a real pseudo-terminal driving real
bash, because readline keymaps and completion scoping simply do not
exist in a fake.

## 9. Recompute; don't cache what you can ask

Our completion system once exported field names into shell
environment variables so completion could work across pipe
boundaries. Every transform that renamed, aggregated, or joined made
the cache confidently wrong — it completed *yesterday's* schema.
We removed the cache entirely and made the keybinding recompute the
live schema by running the pipeline's schema protocol on demand.
The rule that survived: **wrong-but-confident is worse than
honest-but-empty**, and anything cached across a boundary you don't
control is a drift bomb with a UX fuse. Cache only what you can
invalidate by construction (we key one server-side cache by argv
plus a directory fingerprint); otherwise ask the authority again —
asking is usually cheaper than you think.

## 10. The best fix deletes something

The pattern across every arc: serve's parser — deleted for a
protocol. The TinyGo mini-engine (a third semantics) — deleted for
the real engine compiled to WASM. The widget builder and its
fallback — deleted (net −459 lines, and the feature *improved*).
The grid's parallel ops path — deleted for model edits. The
completion caches — deleted for recomputation. When two components
must agree, the elegant fix makes one of them *ask* instead of
*know*; the code that isn't there can't drift. We now treat "what
does this change delete?" as a design-review question: refactors
that only add have to justify themselves.

## 11. Keep your negative results

A channel-per-row concurrency design measured 3× *slower* than
single-threaded (~100ns per row of channel transit × millions of
rows). The benchmark that proves it is still in the tree, named and
commented, because the idea is seductive and will be proposed again
— by a future contributor, or a future AI session. GPU acceleration
for simple aggregations (7× slower than CPU — memory-bound), same
treatment. A negative result that isn't recorded will be re-run at
full price.

## 12. Institutional memory is a system, not a habit

None of the above survives on good intentions. The mechanisms that
made lessons *stick*:

- **Numbered design docs** (RFC-style "DFCs") — stable, chronological
  handles that journals, commits, and other docs cite; superseded
  plans are marked so nobody implements from a dead one.
- **Weekly journals** — what was tried, what failed, *why* decisions
  went the way they did; read at session start. The reflection habit
  ("after bug #3 of the same shape, stop and name the shape") is
  where most principles in this paper were first written down.
- **Hybrid search over all of it** — concept queries, not just
  grep, run before designing anything that smells like prior art;
  wired into the workflow so it happens by default, not by virtue.
- **A rules file with teeth** — the distilled principles live in the
  context every session loads, each rule one line with its war story
  attached; detailed rationale lives in reference docs the rule
  points to. A lesson that isn't in the loaded context effectively
  doesn't exist.
- **Gates as memory** — the deepest lessons are encoded as tests
  (differential harnesses, scale budgets, drift gates, round-trip
  laws) precisely because tests outlive attention. The registration
  parity gate exists because six commands once vanished from a
  secondary build for *months*; no one will ever have to remember
  that lesson again, because CI remembers it.

## 13. Prior art: is any of this new?

We looked. Most individual mechanisms have honorable precedent —
and the combination still seems rare.

**Well-established elsewhere:**

- *Runtime completion served by the program itself*: Cobra's hidden
  `__complete` subcommand, carapace, bash's `complete -C`; our
  `-complete` differs mainly in being position-exact and paired with
  the other surfaces.
- *Declaration-driven signatures, help, and completion in one
  place*: PowerShell cmdlets and Nushell command signatures are the
  large-scale proofs that "declare once, derive everything" works —
  within a single closed shell/runtime.
- *Runtime self-description that UIs build themselves from*: gRPC
  server reflection (grpcurl/grpcui construct requests and whole
  UIs from it) and Kubernetes' OpenAPI discovery behind `kubectl
  explain` are `-spec-json`'s server-side cousins.
- *The N×M → N+M protocol pattern*: the Language Server Protocol is
  the canonical demonstration that putting intelligence in the
  authority and a protocol in front of it collapses the
  editor×language matrix. Our cursor protocols are, in effect, an
  LSP for a command language — same shape, much smaller.
- *GUI generates syntax*: SPSS's Paste button and Stata's command
  echo have taught statisticians the CLI for decades — strictly
  one-way, GUI → text.

**Rare or (to the best of our searching) novel:**

1. **The schema shadow mode across a real POSIX pipe.** Nushell
   knows types because it is one closed program; we get
   pipeline-exact schemas across *separate processes composed by
   the shell*, by running the actual binaries in a
   metadata-transforming mode. We found no other CLI ecosystem
   doing abstract interpretation of arbitrary pipe compositions
   with the production binaries themselves.
2. **Bijective GUI↔text.** SPSS pastes one way; grpcui builds
   requests without a canonical text. A workspace where grid
   clicks and chart controls *edit the command text in place*,
   under round-trip laws and a machine-span ownership rule — so
   the GUI and the text are provably views of one model — appears
   to be genuinely uncommon, possibly new as a disciplined whole.
3. **The system-wide prohibition as method.** Frameworks make
   single-sourcing *possible*; treating every second implementation
   as a defect class — with a greppable audit, protocol growth as
   the standard fix, and gates that fail on reintroduction — is a
   discipline we have not seen named elsewhere. It is the part of
   this paper we most want other teams to steal.

## 14. The meta-principle

Lay the principles side by side and they are one idea wearing ten
coats: **drift is the enemy.**

Two parsers drift (§2). Two state stores drift (§4). Five backends
drift (§5). A test and the bug it hunts drift (§6). A fallback and
the real semantics drift (§7). Performance drifts under fixtures
too small to feel it (§8). Caches drift from their sources (§9).
Code that exists drifts; code you deleted cannot (§10). Teams drift
from their own hard-won knowledge (§11, §12).

Every mechanism in this paper is an anti-drift device: single
authorities with protocols, program text as the one store,
differential gates, sabotage-verified tests, loud refusal, absolute
budgets, recomputation, deletion, and memory systems. None of them
required foresight — every one was purchased with a shipped bug and
then *institutionalized* so it could never be purchased twice.

That is perhaps the most transferable lesson of all: you cannot
avoid learning things the hard way, but you can refuse to learn the
same thing the hard way twice — if, and only if, your project has
somewhere durable for lessons to live: a principle with a name, a
gate that fails, a document with a number.
