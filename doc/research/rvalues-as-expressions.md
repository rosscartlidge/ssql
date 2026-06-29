# Should all rvalues be expressions? (and why the structured flags stay)

**Status:** Design rationale / decision record, 2026-06-28. Captures a design
review of a radical idea — *remove the `…-expr` flags and make every rvalue in
the system a potential expression* — and why ssql keeps the structured
`FIELD OP VALUE` forms instead. Written so future-us doesn't re-litigate it
from scratch.

## The idea

Today ssql has a **split** rvalue model:

- **Structured flags** for the common case: `where -if FIELD OP VALUE`,
  `update -set FIELD VALUE`, `group-by -sum FIELD NAME`.
- **`…-expr` escape hatches** for the powerful case: `-if-expr`, `-set-expr`,
  `-expr`, `-stream-expr` (expr-lang expressions).

The radical proposal: delete the `…-expr` family and make **every rvalue a
potential expression** — a bare field is a trivial expression, a literal is a
trivial expression, `price * qty` is a richer one. One uniform model.

## Why it's appealing (the instinct is right)

"rvalue = expression" is the mainstream design for data languages — SQL
(`SELECT salary*1.1 WHERE age > 18+x`), Polars/pandas, dplyr, jq, AWK
(`$1 + $2`). ssql's structured `FIELD OP VALUE` is the *unusual* choice. A
uniform model would:

- kill the `…-expr` suffix proliferation (a genuine wart);
- compose naturally (`-set total "price*qty"`, `-if "a>1 and b=='x'"`);
- give codegen **one lowering** for rvalues instead of two paths.

So the unification instinct is sound, and the `…-expr` flags are not something
to be proud of.

## The deep tension (why we don't go pure-expr)

The structured form is not just verbose ceremony — it is **ergonomic sugar that
buys three things ssql is specifically good at**, and pure-expr trades all three
away:

1. **Shell-quoting of string literals — the decisive one.** `-if dept eq sales`
   works because an unquoted token in *value position* is a **string literal**.
   In an expression, `sales` is an **identifier** (a field reference) — so
   you'd write `-if "dept == \"sales\""`, fighting shell quoting on the single
   most common operation (string equality). It's a small, constant tax on the
   thing people type most. SQL pays it (`WHERE name = 'foo'`); ssql currently
   doesn't, by design. Easy to underweight until you've typed it fifty times.

2. **Completion.** The field/value/function completion (Ctrl-O fields, value
   sampling, Alt-h functions — shipped v4.50–v4.51) leans on the structured
   grammar: `-if dept eq <TAB>` knows `dept` is a field and samples its values
   *because of the positions*. Free-form `-if <expr>` is an opaque string;
   completing inside it needs **expression-aware completion** (parse the
   partial, know "on an identifier → offer fields+functions; after `==` → offer
   values"). That's a large new piece; without it the interactive experience
   regresses.

3. **Guaranteed-native codegen for the common case.** `-if age gt 25` lowers to
   `r.Age > 25` — trivially, always native. `-if "age > 25"` is native *only if
   the transpiler covers it*, else it falls to the per-row VM (a map + closures
   per row — see `expr-codegen-transpilation.md`). So **pure-expr presupposes
   the transpiler**; without it you push the 90% simple case off the fast path.

Two lesser costs:

- **Validation / "fail loudly."** The structured form validates `OP` against a
  known set and `FIELD` against the schema at parse time. An opaque expression
  defers more to runtime / expr-compile.
- **`generate sql`.** `FIELD OP VALUE` maps cleanly to SQL `WHERE field OP
  value`; an arbitrary expression needs an expr→SQL translator for the SQL
  backend (another lowering target).

## The disambiguation insight

The structured grammar resolves "is `total` a field, a string literal, or a
trivial expression?" by **position** — value position means literal, position 1
means field. Lose the grammar and you lose the disambiguation, which is exactly
what forces the quoting in (1). The `…-expr` flags are, in effect, the explicit
"this argument is an expression, parse it as such" signal. So the current design
is already a **sensible hybrid**; the radical version removes the hybrid's
*better* half (the ergonomic, completion-friendly, shell-friendly default).

## Recommendation: pursue the 80% that's pure win

Don't collapse to pure-expr. Chase the unification without the regressions:

1. **Transpiler first, regardless** (`expr-codegen-transpilation.md`). It's the
   prerequisite for "expressions anywhere" to be fast, and its native subset is
   exactly what the structured form already expresses — so simple cases stay
   native.
2. **Make the *value* and *aggregation* slots expression-capable**, which folds
   `-set-expr` / `-expr` / `-stream-expr` away (`-set total "price*qty"`,
   `-sum "salary*bonus" total`), while **keeping `-if FIELD OP VALUE`
   structured** so bare strings, completion, and native lowering survive. The
   structured form becomes *recognized sugar over the expr model*, not a
   separate thing.
3. Reframe the goal from "get rid of the flags" to "**make the rvalue slots
   expression-capable so the `…-expr` flags become unnecessary**" — same end
   (no `…-expr` proliferation, expressions wherever they add power) without
   forcing quotes-and-lost-completion on the common case.

## Closing the structured-form gaps without going pure-expr

Two specific gaps in the structured form keep forcing people into `…-expr`.
Each has a targeted fix that preserves the bash-friendly, completable, native
properties — and weighing them sharpens *why* the structured form pays off in
some places but not others.

### Gap 1: `field OP field` — the `@field` value sigil ✅ recommended

`-if FIELD OP VALUE` can't compare two fields, because an unquoted token in
value position is a **string literal** (`-if dept eq sales` means the string
`"sales"`). Field-vs-field forces `-if-expr "a > b"` (with quoting).

Fix: a leading **`@`** in any value position means "this value is a field
reference":

```bash
ssql where -if salary gt @budget      # field > field
ssql where -if dept eq sales          # field == literal "sales"  (unchanged)
ssql update -set mirror @source       # copy a field's value (bonus: -set too)
```

It adopts *one atom* of expression syntax (the field reference — ssql's
`$1`/`.col`) into the structured grammar:

- **bash-friendly** (`@` needs no quoting);
- **completable** — `-if salary gt @<TAB>` completes *field names* from the
  upstream schema (reuse the existing field completion), bare `<TAB>` still
  samples *values*;
- **native codegen** — `r.Field1 OP r.Field2`, no transpiler needed;
- **`generate sql`** — drop the `@`, emit `field1 OP field2`;
- **predictable** — `@budget` validates `budget` is a real field (fail loudly).

The one wrinkle: `@` collides with leading-`@` string *literals*
(`-if user eq @bob`). Rare in comparison values; escape with `@@bob` or quote
`'@bob'`. (`@` beats the alternatives: `#`/`$` are shell-special, `.`/`/` are
common in path-like values.)

Rejected alternatives: **auto-detect** ("treat the value as a field if it
matches a known field name") is silently wrong the day someone has a `sales`
field — violates fail-loudly; **type-based** ("numeric field + non-numeric
token ⇒ field ref") is subtle and string-ambiguous; a **`-if-fields` flag** is
flag proliferation.

### Gap 2: computed `set` (`field OP value`) — lean *no* on a structured flag

`-set FIELD VALUE` sets a literal; computing `price = price * 1.1` forces
`-set-expr`. Tempting to add a symmetric `-set-op RESULT FIELD OP VALUE`
(arith op ∈ `mul/div/add/sub/mod/pow`). But there's a **filter↔set
asymmetry**:

- For **filters**, a single binary `FIELD OP VALUE` covers most of the real
  need (AND/OR come from clause separators), so `-if`'s structured form earns
  its keep.
- For **computed sets**, binary covers a *much smaller* fraction — real
  arithmetic wants more than two operands and precedence: `price * qty *
  (1 - discount)`, `(gross - tax) / months`, `round(price * 1.1)`. A binary-only
  `-set-op` hits that wall immediately and drops back to `-set-expr` anyway.

So `-set-op` buys `price * 1.1` quote-free at the cost of a **third** set-flag
(`-set` / `-set-op` / `-set-expr`) that overlaps `-set-expr` and covers a narrow
slice. Coverage-to-cost is poor — arithmetic outgrows binary far faster than
filters do.

Better: make `-set-expr` the *one good path* for computed values by removing its
two pains — (1) the **transpiler** makes it native (handles binary, N-ary,
precedence, functions — no wall), and (2) the **Alt-h function reference**
(shipped v4.51.2) makes it discoverable while typing. Plus **`@field`** covers
the trivial field-ref case. The residual cost is the quoting on
`-set-expr "..."` — more tolerable for sets (often scripted) than for the
rapid-interactive `-if`.

If interactive quoting on the simple binary case really bites, do it as a
**fixed-arity** `-set-op RESULT FIELD OP VALUE` (clean per-arg completion,
`@field`-aware value), *not* a variadic richer `-set` (the alternating
operand/op variadic is fiddly to complete) — eyes open that it's binary-only and
overlaps `-set-expr`.

**Takeaway:** structured-binary is a great fit for `-if` (filters are usually
one comparison) and a weak fit for computed `-set` (arithmetic usually isn't
one operation). That asymmetry is the rule for deciding when a structured form
is worth it versus when to lean on a (fast, discoverable) expression.

## Bottom line

The unification instinct is correct and worth pursuing — but the structured
`FIELD OP VALUE` earns its keep precisely in the shell + completion context ssql
optimizes for. Keep it as the fast default; let expressions *grow into* the
value slots on top of the transpiler, rather than replacing the grammar
wholesale. The single thing that most argues against going fully pure-expr is
string-literal quoting.

If we ever want to feel out the pure-expr end, the cheap experiment is to add
expression acceptance to **one** value slot (e.g. `-set`) behind the transpiler
and live with it for a while — that surfaces the quoting/completion reality with
almost no commitment.

## Related
- `doc/research/expr-codegen-transpilation.md` — the transpiler (the
  prerequisite; makes expressions fast).
- `doc/EXPRESSIONS.md` — the expr-lang reference.
- `doc/research/codegen-ir-evolution.md` — the structured `Op` IR direction,
  which would carry an expression as a first-class node either way.
- v4.51.2 — Alt-h shows the function reference on expression args, which
  softened the "writing expressions blind" pain that originally motivated
  rethinking the model.
