# Pipelines as Data: Safe Programmatic Construction, and Why SQL Cannot Have It

Reference: DFC134
Created: 2026-09-20
Last modified: 2026-09-20

[Back to Index](./README.md)

Status: **exploration — nothing built; three experiments run (§3).**
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

**5.2 Close option injection in positional slots.** Two complementary
fixes, both in autocli: honour `--` as "no more flags"; and have the
document runner *always* insert it before positionals, so a document can
never be misread whatever its values. Optionally, refuse at validation
time any positional element that matches one of the command's own flag
names unless the document marks it as a value.

**5.3 Parameters for expressions.** `-set-expr total 'price * $rate'
-param rate 1.1`: the expression text is fixed by the program's author,
values arrive in slots, and expr-lang receives them as environment
variables rather than source. Same shape as a prepared statement, needed
only for the expression arguments.

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
claim true. 5.3–5.5 are each a unit of their own and can follow demand.

## 6. Open questions

- **Positional or named?** `["where","-if","age","gt","25"]` is the
  ground truth and needs no new vocabulary. `{"where":{"if":[["age","gt",25]]}}`
  is friendlier and typed (25 is a number), but it is a second spelling of
  every command, which DFC115 warns against. Leaning: argv is the
  canonical form; the named form, if wanted, is *generated* from
  `-spec-json` and compiles to argv, never the reverse.
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
  (`escapeSQL`, `quoteIdent`; DFC133's fuzzing covers the expression
  translator, not yet the statement as a whole). Worth a targeted fuzz:
  adversarial field names and values through `generate sql`, executed.

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
