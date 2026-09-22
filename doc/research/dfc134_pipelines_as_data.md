# Pipelines as Data: Safe Programmatic Construction, and Why SQL Cannot Have It

Reference: DFC134
Created: 2026-09-20
Last modified: 2026-09-22

[Back to Index](./README.md)

Status: **§5.1 (`ssql run`), §5.2 (`-arg`) and §5.3 (`-param`) BUILT
2026-09-22** — autocli v4.18.0 / v4.19.0 plus the ssql halves; §5.1a,
§5.2a and §5.3a record what building each found. The central claim of §4
now holds end to end for a document run by `ssql run`. §5.4 (policy) and
§5.5 (derived artefacts) not started.
Ross, 2026-09-20: "generating SQL safely programmatically calls for a lot
of complex operations to ensure no SQL injection is possible. I have a
strong feeling that ssql pipelines could be expressed as a simple schema
that would allow easy and safe programmatic construction, because the
syntax is very simple and the argument types unstructured and well
defined. What do you think? … This could be a fundamental advantage over
traditional SQL."

Short answer: **the intuition is right, for a reason that can be stated
exactly, and ssql is most of the way there already. It is not true yet.**
Three specific holes stand between "usually safe" and "safe by
construction", all found by running the experiment rather than by
argument, and all closable.

## 1. Why SQL has the problem

SQL injection is not a bug in applications; it is a property of the
interface. A SQL statement is **one string in a rich grammar**, and the
program that builds it must put untrusted data into that string. Data
and code share a channel, and the only thing separating them is the
builder's correct use of the grammar's quoting rules — for every dialect,
every literal type, every context.

The standard cure, the prepared statement, moves *values* out of the
string. It does not and cannot move anything else:

| Part of the query | Parameterisable in SQL? |
|---|---|
| A value compared in `WHERE` | yes — `?` |
| A column name | **no** |
| A table name | **no** |
| `ORDER BY` column, `ASC`/`DESC` | **no** |
| An operator (`=` vs `>`) | **no** |
| Which clauses exist at all | **no** |

So any program offering "filter by this field, sort by that one" — every
admin screen, every report builder, every API with `?sort=` — is back to
assembling strings, or adopts an ORM or query builder: a second
implementation of the grammar, as an object model, whose job is to
re-serialise safely. That is the "lot of complex operations". The
complexity is not incidental; it is what it costs to make a string
language safe from the outside.

## 2. Why an ssql pipeline is different in kind

A pipeline is a **list of stages**; a stage is a **command name and a
list of argument strings**. That is not an encoding of the pipeline — it
is what the operating system actually receives (`execve` takes an argv
array, not a line). The quoting, the spaces and the `|` that a person
types belong to the *shell*, a convenience layered on top. Remove the
shell and there is no grammar left to escape from:

```json
[
  ["from",  "orders.csv"],
  ["where", "-if", "customer", "eq", "<anything at all>"],
  ["sort",  "-desc", "amount"],
  ["limit", "10"]
]
```

The untrusted string occupies one array element. There is no sequence of
characters it can contain that ends the element, because elements are not
delimited by characters. This is the property prepared statements give
SQL for values — but here it holds for **every slot**: the field name,
the operator, the sort key and the value are all just elements, and which
stages exist is just the length of the outer list.

Two further things ssql already has make the structure *checkable*, not
merely unparseable:

- **`ssql -spec-json`** (DFC118): the CLI's own machine-readable
  description of every command, flag, argument count, argument type and
  vocabulary — `field`, `integer`, `float`, an enumerated set, a file.
  It is generated from the same declarations the parser runs on, so it
  cannot drift (DFC115). A pipeline document can be validated against it
  before anything executes: right arity, a known operator, an integer
  where an integer is required.
- **Closed vocabularies, checked at run time.** A field slot is validated
  against the data's schema, and an unknown name is an error (as of
  DFC133, for every command). So even a *legitimate-looking* untrusted
  field name can only select a column that exists.

Internally the project has already moved this way for its own reasons:
DFC123's `Op` IR is a structured stage description that replaced
re-parsing command strings, and DFC118's builder takes "argv per stage,
parsed by the real parser" as its model. The safety property is a
consequence of a design chosen for other reasons — usually a sign the
design is right.

## 3. Three experiments (2026-09-20)

Run against the current build; this is what is true today, not what
should be.

**3.1 Flag argument slots are safe — by arity, not by luck.**

```
ssql where -if name eq VALUE     with VALUE = -invert | + | - | -- | -generate
```

Every one was treated as data: the rows whose name is literally
`-invert`, `+`, `-` and `--` came back, and nothing else changed. The `+`
and `-` are ssql's own clause separators, and `-invert` is a real flag of
`where` — none of it matters, because autocli binds a flag's arguments by
**count**. Once `-if` has claimed three elements, their content is never
looked at as syntax. This is the core of the thesis, and it holds.

**3.2 Positional slots are NOT safe — option injection.**

```
ssql include name -generate        (a CSV really can have a column called "-generate")
```

`include` switched into **code-generation mode** and emitted Go
fragments instead of data. A positional (variadic) slot has no arity to
protect it, so an element that looks like a flag is a flag. The
conventional cure does not exist yet: `ssql include -- name -generate`
fails ("no fields specified"), and `ssql sort -- -desc` cannot name a
column called `-desc`. This is CWE-88, argument injection, and it is the
one place where a structured pipeline can still be steered by data. It is
also the easiest to close (§5.2).

(Correction, 2026-09-22: autocli's parser does recognise `--`, but not as
"the rest are positionals". It stops parsing and hands everything after it
to `ctx.RemainingArgs`, which `from ssh HOST -- PIPELINE` and `from
catalog` use for the push-down pipeline. For every other command the
elements after `--` are simply dropped, hence "no fields specified". So
`--` is not missing; it is **taken**, which matters for §5.2.)

**3.3 Expression arguments are a string language again.**

```
U='x" || true || "'
ssql where -if-expr "name == \"$U\""      → matches rows it should not
ssql where -if      name eq  "$U"          → matches nothing (correct)
```

`-if-expr`, `-set-expr` and `group-by -expr` take expr-lang *source*.
Splicing a value into that source is exactly SQL injection with a
different grammar. The flag form of the same condition is immune. So the
rule for generated pipelines is: **values go in flag slots, never into
expression text** — and where an expression is unavoidable it needs
parameters (§5.3), which is the prepared-statement idea applied to the
one place ssql has a string grammar.

## 4. What "safe" has to mean

Injection is one threat. A pipeline built from untrusted input has three
more, and the syntax does nothing about them:

| Threat | Example | Syntax helps? |
|---|---|---|
| Escaping a slot (injection) | a value that becomes a flag or a stage | yes — §2, modulo §3.2/§3.3 |
| Reaching things it should not | `from /etc/passwd`, `from ssh host …`, `join ~/.ssh/id_rsa` | no |
| Writing things | `tee FILE`, `to csv FILE`, `generate go -run` | no |
| Consuming the machine | an unbounded `join`, `sort` of a huge file | no |

So the claim worth making is precise: **a pipeline document is safe to
*construct* from untrusted data — no value can change the pipeline's
shape — and that leaves only *authorisation*, which is a policy over a
structure you can inspect, instead of a parsing problem over a string you
cannot.** That second half is the real advantage. "May this request read
this file?" is a question about one typed element of a list. Asking the
same of a SQL string means parsing SQL.

`ssql serve -readonly` is a first instance: it rejects pipelines
containing writers. It currently does so over pipeline text; over a
document it would be a lookup.

## 5. What it would take

**5.1 A canonical pipeline document and a shell-free runner.** The JSON
form in §2 (or an equivalent with named flags — §6), plus `ssql run
-pipeline-json FILE|-`, which validates against `-spec-json` and then
starts each stage with `exec`, wiring the pipes itself. **No shell is
ever involved.** Today `generate go -pipeline`, `serve` and the
equivalence harness all run pipelines as strings through `bash -c`; that
is where the shell's grammar, and therefore injection, re-enters. The
runner is the piece that turns §2 from an observation into a guarantee.
It is also what a library binding in any language would call.

**5.1a What building it found (2026-09-22).** `ssql run DOC` in
`commands/run.go` + `pipeline_doc.go`. Decisions taken while building:

- **Document shape**: a JSON list of stages, each a list of strings; a
  list in an argument's place is a nested pipeline (a list of strings
  there is the one-stage shorthand); `{"pipeline": […]}` is accepted so
  policy fields (§5.4) can sit beside the stages later. Numbers are
  refused with "write it as a string: the command decides what it is",
  since typing by JSON kind would be a second type system.
- **Validation asks the parser.** autocli gained `Command.Check(args)`:
  Execute's walk, parse and required-flag validation with no handler.
  The runner validates every stage, nested ones included, before any
  starts; a failure names the stage (`stage 2 (where): flag -bogus:
  unknown flag`). No copy of the grammar exists in the runner, per
  DFC115. The command path must be a command name (a stage may not start
  with `-`), and `run` inside a document is refused.
- **Execution reuses serve's chain.** `startStageChain` became a thin
  wrapper over a shared `startChain` (explicit `os.Pipe`s, parent closes
  its copies so an early-exiting consumer EPIPEs its producer, shell
  status semantics on wait). Nested pipelines run as their own chain
  writing into a pipe whose read end the stage receives as `/dev/fd/3+k`
  via `ExtraFiles`: process substitution without a shell.
- **`-print` renders the shell form verbatim**, quoted: it means exactly
  what the document means, because both hand the same elements to the
  same parser. The runner does not insert `-arg`; a document that needs
  it and lacks it is wrong in both forms (the builder's job, §5.5).
- **`serve -readonly` refuses `run`**: a document can hold any stage and
  the readonly check cannot see inside a file argument.

Tests: `TestRunDocument` uses the document's own shell rendering, run by
bash, as the oracle (byte-identical output on a hostile fixture with a
nested join and a `-param` attack string), plus -check refusing before a
sink runs, a failing stage named, early exit not a failure, generation
mode passing through. Two doc-layer unit tests against a small fake
root. Nothing new was found this time; the two earlier units had already
walked the argv re-readers.

Document → SQL/Go directly (Ross: "should we have `-json file` for
`generate`?"): `generate go|sql|ssql|json -json FILE` runs the document
through the runner under the generation mode and generates from the
fragments; one `generateFragmentSource` helper serves all four and the
existing `-pipeline`/`-script` (shell text through bash) alongside.
Building it exposed that `ssql frob` printed the banner and exited 0, so
`Check` accepted a misspelt command: autocli v4.20.0 makes a bare unknown
word an unknown command whether or not the root has a handler.

Text → document (Ross, same day: "do we have a `generate json`?"):
`generate json` builds the document from each fragment's `Op.Argv`, the
stage's own argv, so no shell text is parsed; a func fragment (process
substitution) becomes the nested pipeline in the `/dev/fd` argument it
fed. With `run -print` the round trip document → shell → document is
byte-identical (pinned), which is DFC118's bijection for the document
form. A fragment without an `Op` (an older ssql over SSH) is refused
rather than parsed. Not done: a JSON Schema for the document generated
from `-spec-json` (§5.5); policy (§5.4).

**5.2 Close option injection: `-arg`, a flag form for every positional
(DECIDED, Ross 2026-09-22).** Positional arguments stay as the
convenience people type. Alongside them every command gains one reserved
flag that carries a positional *by arity*:

```
ssql include name -generate                  # human form; "-generate" is read as a flag
ssql include -arg name -arg -generate        # safe form; both are field names
ssql sort -arg -desc                         # sorts by a column called "-desc"
ssql sort -arg dept - -arg salary -desc      # clauses still work: bare "-" separates, "-desc" is the flag
```

`-arg VALUE` means exactly "append VALUE to the current clause's
positionals, without looking at it". It takes one argument, so §3.1's
result applies unchanged: once `-arg` has claimed its element, the content
is never syntax. Repeating it is the project's own `.Accumulate()` rule
(never in-argument delimiters) applied to positionals. Bare positionals and
`-arg` may be mixed; order of appearance is the order of the slot.

Why this and not the conventional `--` (which the first draft of this
section proposed):

- **One mechanism, already proven.** Flag slots are safe because they bind
  by count. `-arg` puts positionals under the same rule. `--` is a
  different mechanism, a mode switch, and a serializer that forgets it once
  reopens the hole silently. A forgotten `-arg` is visible in the document:
  a bare element where none is allowed.
- **`--` cannot coexist with clauses.** `-` and `+` are clause separators.
  After "no more flags", is a later `-` a separator or a column called `-`?
  Either answer breaks something: multi-clause commands become
  unwritable in the safe form, or the safe form is not safe. `-arg` is per
  element, so the question never arises.
- **`--` is taken.** It already means "what follows is the pushed-down
  pipeline" for `from ssh` and `from catalog` (§3.2 correction).
- **It makes the document grammar regular.** In canonical form, after the
  command path every element is either a declared flag name or is consumed
  by the preceding flag's arity. There are no bare data elements at all, so
  a validator needs no "could this be a flag?" heuristic: anything else is
  rejected outright.

Where it lives: **autocli, once** (Ross, 2026-09-22), not per command.
Sketch: in `Command.Parse`, before the flag branch, `arg == "-arg"` takes
`args[i+1]` verbatim into `currentClause.Positional` (error if there is no
next element); the name is reserved, so the builder refuses a command
that declares its own `-arg` (exact match only; `group-by -arg-max` /
`-arg-min` are different names and unaffected). `-spec-json` advertises it
so consumers need not know it by folklore. Completion after `-arg` is the
completion of the positional slot it fills. `-generate` fragments and
`generate ssql` keep emitting the human form unless a value begins with
`-` or `+` or is a separator, in which case they MUST emit `-arg` (no
such pipeline can be written today, so this is new ground, not a
regression risk).

What stays bare: the **command path** (`from`, `csv`, `group-by`). It is
not data; it comes from the closed vocabulary in `-spec-json` and the
validator checks it by lookup. A program must never take a command name
from untrusted input without that lookup, which is policy (§5.4), not
syntax.

The document runner (§5.1) emits only the `-arg` form. Tests: the §3.2
probes become permanent cases (a CSV with columns `-generate`, `-desc`,
`-`, `+`, `--` through `include`, `sort`, `exclude`, `group-by`), and
DFC133's random tester gains hostile column names, which exercises every
command's positional slot for free.

**5.2a What building it found (2026-09-22).** autocli's half was a day's
work and went as designed; ssql's interpreter needed *no* change (it reads
parsed positionals from autocli). Everything below was found by writing the
hostile-column equivalence cases (`hostile.csv`: columns `-generate`,
`-desc`, `+x`, `-`) and watching lanes disagree. Each is a consumer that
re-reads argv by hand, which is DFC115's thesis restated as a security
property: **every second reading of the arguments is a place where data
can become syntax.**

1. **§3.1 was not entirely true.** `ssql where -if name eq -shell-init`
   printed the bash completion script: `main` scanned *every* argument for
   its root flags. A flag slot is safe from autocli's parser, but not from
   code that looks at `os.Args` before the parser does. Fixed (first
   argument only); `TestArgumentsAreNotSyntax` now feeds every root flag,
   `-generate`, `-arg`, `--` and both separators through a flag slot.
2. **`generate ssql`'s optimiser produced a wrong result.** Its sort reader
   took the KEY `-desc` for the direction flag and fused `sort -arg name
   -arg -desc | limit 2` (ascending, two keys) into `top 2 -field name`.
   Because the optimiser runs ahead of `generate go`, four of five lanes
   were wrong. The reader now understands `-arg` and fuses only what `top`
   can express (one key, one clause, descending). Gate:
   `hostile_names_two_key_sort_is_not_top`, watched failing.
3. **`generate sql` silently dropped the column and the ORDER BY.** Its
   translators tell a positional from a flag by a leading dash. `-arg` with
   an ordinary value is collapsed to the bare form (so the program-emitted
   form costs no backend: `arg_form_equals_bare_form`, all lanes, and the
   random tester now writes half its positionals as `-arg`); a value that
   would be misread is **refused**. Expressing it needs the translators to
   take parsed positionals from the `Op` (the DFC115 legacy exception).
4. **Generation mode corrupted its own record.** The `-generate` stripper
   removed the *value* in `exclude -arg -generate`, leaving a dangling
   `-arg` in the fragment. One `lib.StripGenerateFlag` now serves `Op` and
   command string. Still open: a flag ARGUMENT spelled `-generate` (`where
   -if name eq -generate`) is dropped from the record, because telling it
   apart needs arities; the clean fix is for autocli to hand back the
   classified argv rather than ssql re-deriving it.

Also a bonus: `ssql from -arg csv` reads a file called `csv` (bare, that is
the `csv` subcommand), so `-arg` removes the data-vs-command-name ambiguity
as well.

Not done yet: the UIs that write pipeline text (the explore builder, serve's
grid) still emit bare positionals, so a hostile column name chosen in a UI
produces a wrong command line; they should emit `-arg` when the name needs
it. TODO.md.

**5.3 Parameters for expressions: real variables, clause scope, declared
types (DECIDED, Ross 2026-09-22).**

```
ssql update -set-expr total 'price * rate' -param rate float 1.1
ssql where  -if-expr 'name == who'         -param who string 'x" || true || "'
ssql where  -if-expr 'age > lo' -param lo int 18 + -if-expr 'age > lo' -param lo int 65
```

- **Binding, not substitution.** The expression text is fixed by the
  program's author and compiled once. A parameter is a variable in
  expr-lang's environment, exactly as a field is. The value never passes
  through the expression grammar, so there is nothing to escape. (The first
  draft wrote `$rate`, which read as textual substitution; that spelling is
  withdrawn.) The second example above is §3.3's attack string, now inert:
  it is compared, not parsed.
- **Clause scope.** An expression sees the `-param`s written in its own
  clause, all of them, wherever in the clause they appear. One clause can
  hold several expressions (`-if-expr` twice, `-set-expr` for several
  fields) and they share the clause's parameters. Another clause may reuse
  a name with a different value (third example). This is the scope autocli
  already gives `.Local()` flags, so nothing new is invented, and a command
  without clauses has one clause, so for it the scope is the command.
  Pipeline-wide scope is rejected: stages are separate processes, so it
  would have to travel in the environment, which is state outside the
  command text (DFC115).
- **Declared type: `-param NAME TYPE VALUE`.** TYPE is from the wire
  format's closed set (`string`, `int`, `float`, `bool`, `time`). Inferring
  the type from the value's spelling is precisely the zero-padded bug
  DFC133 fixed (`02134` is a postcode, not 2134), and a parameter is the
  slot built to hold untrusted text, so it is the last place to guess.
  `string "12"` stays a string. A VALUE that is not of TYPE is a parse-time
  error via `ssql.CastValue` (strict, as of v4.103.0).
- **Collisions are errors.** Names stay bare because `price * rate` reads
  as intended. If a parameter has the same name as a field of the input,
  the command fails, naming both; silent precedence either way would let
  data (a new column upstream) change what an expression means. A
  parameter no expression uses is also an error (a typo'd name must not
  pass quietly). Names must be expr identifiers.
- **Lowering, all lanes.** exec: an entry in the expr env. Record and
  typed codegen: a typed Go variable, and because generated programs
  already lift literals into runtime flags, the parameter becomes a **flag
  of the compiled binary**: a prepared statement in the full sense, built
  once and re-run with new values. The expr→Go transpiler treats the name
  as an identifier of the declared type (no guessing, which typed mode
  needs anyway). `generate sql`: a literal rendered by the declared type
  through `sqlLiteralFor`'s quoting; host-language placeholders (`$1`) are
  a possible later option for the DFC132 harnesses. `generate ssql`: the
  `-param` triple verbatim.
- **Where it lives.** The commands own the flag (where, update, group-by
  `-expr`, anything marked `.Expression()`), sharing one helper that
  builds the env and the codegen declarations; autocli needs nothing new
  beyond what `.Local()` and three-argument flags already give.
  `-spec-json` shows `-param` like any flag. Tests: equivalence cases with
  the attack string as a value in every lane including DuckDB, and
  `TestExprGoDifferential` entries for each parameter type.

Rule for generated pipelines, restated: **values go in flag slots; where
an expression is unavoidable, values go in `-param`; nothing untrusted is
ever concatenated into expression text.** With §5.2 that leaves no slot in
which data can become syntax.

**5.3a What building it found (2026-09-22).** Built as designed on `where`
and `update` (the two commands with `-if-expr` / `-set-expr`; `group-by`'s
aggregation expressions are a follow-up, TODO). Shape: one
`runtime.Params` thunk read on first evaluation, because a generated
program's package-level vars initialise before `flag.Parse`; the same
`paramBinding` serves the interpreter, record VM, and typed Tier-V lanes,
and the native transpiler takes the bindings as typed variables resolved
BEFORE fields (collision loud, time quiet → VM). The equivalence cases
(`param_*`) pass in every lane including DuckDB, and disabling the SQL
rendering fails all four in the duckdb lane. Found on the way, all
pre-existing, all fixed, none about parameters:

1. **A malformed CSV row ended the read silently, exit 0.** The injection
   fixture had a bare `"`; `ReadCSVFromReader` returned false on the row
   error. Now a panic → `Error:`, as `*CellError` already was. Also
   caught: a ragged row.
2. **`generate sql` rendered a later `update` clause as unconditional**
   (first-match-wins lost when a field is set only in the else-clause).
   Each field's CASE now carries NOT(earlier) for earlier clauses that
   did not set it; when they did, CASE order suffices and no guard is
   emitted.
3. **Three copies of one table, three drifts.** `from csv -type COL time`:
   record codegen's type-name copy lacked `time` (→ FieldTypeAuto, text
   column, comparisons matched nothing); the SQL `from` translator's
   flag-arity copy said `-type` takes one argument (→ `time` read as a
   second file); typed's CSV decoder accepted RFC 3339 only (→ date-only
   column failed in typed mode alone). DFC115 again: each copy of a
   command's grammar drifts independently, and the equivalence gate is
   what finds them.

Pattern worth naming: the two units of this DFC found nine defects, none
in the feature being built. Writing a case that must agree across five
lanes on a hostile fixture is a bug-finding instrument in its own right
(DFC133's thesis), and the hostile fixture is the discriminating input.

**5.4 A policy layer over the document.** Allowed commands; file
arguments confined to given roots (the spec already marks which arguments
are files); no `ssh`/`catalog`/`-run`; row and time budgets. `serve`'s
`-readonly` and `-dir` become two entries in this list rather than
special cases.

**5.5 Derived artefacts, all generated from `-spec-json`:** a JSON Schema
for the document (so any validator in any language can check one); typed
builder libraries (`p.Where("age", Gt, 25).Sort(Desc("amount"))`) whose
method signatures *are* the spec; and the text ⇄ document round trip the
bijective builder (DFC118) already needs.

Rough size: 5.1 and 5.2 are days, and together they make the central
claim true for flag-form pipelines. 5.3 is a unit of its own and is what
extends the claim to expressions. 5.4–5.5 can follow demand.

Order taken: 5.2 (autocli v4.18.0, hostile-column tests), 5.3, then 5.1,
all on 2026-09-22.

## 6. Open questions

- **Positional or named?** `["where","-if","age","gt","25"]` is the
  ground truth and needs no new vocabulary. `{"where":{"if":[["age","gt",25]]}}`
  is friendlier and typed (25 is a number), but it is a second spelling of
  every command, which DFC115 warns against. Leaning: argv is the
  canonical form; the named form, if wanted, is *generated* from
  `-spec-json` and compiles to argv, never the reverse. With §5.2 the
  canonical argv has no bare data elements, which is what makes that
  compilation (and its validation) mechanical.
- **Process substitution** (`join <(ssql from …)`) is a shell feature. In
  a document it becomes a nested pipeline in an argument position —
  cleaner than the text form, and it removes the last reason a runner
  would need a shell.
- **Is the comparison with SQL fair?** Partly. A good SQL query builder
  is also injection-safe. The difference is where the safety lives: there
  it is the builder's correctness over someone else's grammar, re-proved
  per dialect; here it would be the absence of a grammar, with one
  generated schema as the only contract. The honest pitch is not "SQL is
  unsafe" but "**in ssql the safe form is the native form** — there is no
  unsafe string underneath it to get wrong".
- **Does `generate sql` undo it?** It emits SQL text from pipeline
  arguments, so it is itself a SQL builder and must quote correctly
  (`escapeSQL`, `quoteIdent`, `quoteFile`, `escapeLike`). **Answered
  2026-09-22 (Ross: "so can we create safe SQL from our json?")**: the
  random tester now draws injection strings as cell values and in every
  literal slot, plus SQL-hostile column names, and runs the SQL in
  DuckDB against exec. First run found four defects, none of them an
  injection in the sense of a value becoming a statement, all of them a
  value or name mis-rendered: `1st` emitted bare (`SELECT 1st` = `1 AS
  st`); DuckDB's sniffer taking `'` as the CSV quote character (fixed by
  pinning the dialect in `read_csv`); LIKE patterns escaped without an
  `ESCAPE` clause (DuckDB has no default, so `contains '%'` matched a
  backslash); and, from the equivalence case, a `-set` value containing
  `*/` ending the generated Go program's header comment so the rest
  compiled as code. After the fixes: 6,500 pipelines over four seeds,
  no disagreement. The answer is yes, with the property resting on those
  four quoting functions, which the fuzz now exercises at volume. The
  identifier half is bounded by §5.2a.3 (names beginning with `-`/`+`
  are refused by the SQL lane, not mis-rendered).

## 7. References

- [DFC115](./dfc115_commands_are_the_authority.md) — commands own their
  grammar; consumers ask through a protocol. The spec is that protocol.
- [DFC118](./dfc118_bijective_query_builder.md) — `-spec-json`, and
  "argv per stage, parsed by the real parser" as the builder's model.
- [DFC123](./dfc123_pipeline_ir.md) — the structured `Op` IR that already
  replaced re-parsing command strings inside the code generators.
- [DFC133](./dfc133_finding_unknown_bugs.md) — unknown fields are errors
  everywhere (the closed-vocabulary half of §2); the fuzzing that §6's
  last question would extend.
- `doc/research/ssql-serve-proposal.md` — `-readonly`, `-dir` and the
  "not a sandbox" caveat that §5.4 would turn into a policy.
