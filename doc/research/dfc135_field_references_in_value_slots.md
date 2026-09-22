# Field References in Value Slots: `-if-field`, `-set-field`, `-param-field`

Reference: DFC135
Created: 2026-09-22
Last modified: 2026-09-23

[Back to Index](./README.md)

Status: **BUILT 2026-09-22** (Ross: "I am convinced we should add all
three"); §9 records what building it found. The §7 open points are resolved as proposed:
the `-field` suffix names; all ten operators on `-if-field`; strict cast
on `-param-field`; an absent SOURCE leaves `-set-field`'s target absent;
DFC101's Gap 1 is superseded by this DFC, the rest of DFC101 stands.
Honest weighting (§3.5): `-param-field` is the one expressions cannot
replace; `-if-field` and `-set-field` are ergonomics (completion, no
quoting, guaranteed native).

## 1. The gap

A value slot in a structured flag is a literal. `where -if dept eq sales`
compares to the string `sales`; there is no way to compare two fields
without leaving the structured form for an expression:

```bash
ssql where -if-expr 'salary > budget'      # today: the only field-vs-field form
```

Expressions cost quoting, lose schema-aware completion, and go through
the transpiler. DFC101 (2026-06-28) proposed closing the gap with a
sigil, `-if salary gt @budget`, and recommended it. This DFC replaces
that recommendation and extends the idea to `-param`, which is where it
earns the most.

## 2. Why not the sigil (what changed since DFC101)

DFC134 built the case that an ssql pipeline is safe to construct from
untrusted data because **a slot's kind is fixed by the flag, never by
the value's spelling**. That rule is why `-param` declares a TYPE rather
than inferring one from the value, why `-arg` exists for positionals,
and why the injection fuzz (DFC134 §6) could run 6,500 hostile pipelines
with no escape rule to teach it.

A sigil breaks the rule in the slot that most often carries untrusted
data. With the DFC101 design:

```bash
ssql where -if token eq "$INPUT"                          # INPUT = @token  → always true
ssql where -if-expr 'token == who' -param who string "$INPUT"   # same
```

The failure is silent when the attacker names a real column, which is
the SQL-injection outcome exactly. DFC101's `@@` escape moves the burden
back onto every program that builds pipelines, the burden DFC134 exists
to remove; and every document builder, `run -print`, `generate json` and
the fuzz would need to know it. A rule with an escape is a grammar.

So: **a separate flag per slot, with the kind in the flag's name.** The
value slot of `-if`, `-set` and `-param` stays a literal, always.

## 3. The design

Three flags. Each is the existing flag with `-field` appended and the
value slot replaced by a field name.

### 3.1 `-if-field FIELD OP FIELD` (where, update)

```bash
ssql from orders.csv | ssql where -if-field amount gt budget
ssql from orders.csv | ssql where -if-field ship_date ge order_date -if status eq open
ssql from orders.csv | ssql update -if-field qty gt stock -set status backorder
```

- Same operators as `-if`: `eq ne gt ge lt le contains startswith
  endswith regex` (the string operators take the right field's value as
  the pattern).
- Same negation: `+if-field`.
- Same absent-value rule (DFC124/DFC128): if either side is absent the
  condition is false, and its negation true.
- Both names are validated against the first record; an unknown field
  is loud.
- Comparison follows the left field's runtime type, as `-if` does; a
  numeric left and text right is a loud type error rather than a string
  comparison (the `top`-by-string lesson, v4.55).

### 3.2 `-set-field FIELD SOURCE` (update)

```bash
ssql update -set-field previous_status status      # copy a column
ssql update -if-field a ne b -set-field a b         # reconcile
```

- The target takes SOURCE's value and type; an absent SOURCE leaves the
  target absent (DFC124: absence is preserved, not turned into `""`).
- Unknown SOURCE is loud.

### 3.3 `-param-field NAME TYPE FIELD` (where, update)

The one with the most reach. `-param NAME TYPE VALUE` binds a value;
`-param-field` binds a **column, per row, viewed as TYPE**:

```bash
# The expression text is fixed; the document builder chooses a literal or a column.
ssql where -if-expr 'price * rate > lo' -param rate float 1.1     -param lo int 15
ssql where -if-expr 'price * rate > lo' -param-field rate float discount -param lo int 15

# A typed view of a column whose name is not an expression identifier.
ssql where -if-expr 'n > 3' -param-field n int 1st
ssql where -if-expr 'code == want' -param-field code string a.b -param want string x

# Compare two columns of different declared types on equal terms.
ssql update -set-expr diff 'actual - planned' -param-field actual float actual_text -param-field planned float planned
```

- **Per-row binding.** NAME is a variable of the clause's expressions
  whose value is FIELD's value on that row, cast to TYPE with the
  strict rules `cast` uses (`ssql.CastValue`): a value not of TYPE is
  loud; an absent FIELD leaves NAME absent, so a condition on it is
  false and its negation true, and an assignment from it yields no
  value.
- **Everything else is `-param`'s design.** Clause scope; the same
  clause may mix `-param` and `-param-field`; NAME must not be a field of
  the input (collision is an error, so a new upstream column cannot
  change an expression's meaning); NAME must be used by some expression
  in the clause; NAME is an expression identifier; FIELD must exist.
- **Not a flag of the compiled program.** `-param` lifts to
  `-param-NAME` on the generated binary because a value is a runtime
  quantity. Which column an expression reads is the program's shape and
  is fixed at generation time. (If `-param-NAME` accepted a column
  reference, the sigil would be back, inside the binary.)

### 3.4 What stays as it is

- `-if FIELD OP VALUE`, `-set FIELD VALUE`, `-param NAME TYPE VALUE`
  keep their value slot as a literal, always. `@` is an ordinary
  character in them.
- `-if-expr` / `-set-expr` are unchanged; they remain the place for
  arithmetic and functions, with `-param` and `-param-field` as the
  slots that feed them.

### 3.5 What each one buys over `-if-expr` / `-set-expr`

Asked directly (Ross: "so the big win … is that we get the field
completion?"). For `-if-field` and `-set-field`, yes, and no quoting,
and a guaranteed native lowering; `-if-expr 'a > b'` with fixed text is
exactly as safe, so these two are convenience. `-param-field` is
different: a program holding fixed expression text can choose literal or
column at build time without editing the source (the injection door);
columns whose names are not expression identifiers (`1st`, `a.b`,
`x-y`, `select`) become reachable; and a typed view is declared once
instead of `float(x)` scattered through the text and re-derived per
lane.

## 4. Lowering, five lanes

| Lane | `-if-field a gt b` | `-set-field t s` | `-param-field n int c` |
|---|---|---|---|
| exec | `applyOperator` on the two record values, type from `a` | copy value | env[n] = CastValue(record[c], int) per row |
| record codegen | `condOpToExprGo` with a field RHS: `ssql.GetOr(r,"b",…)` typed by the advisory | `mut.Set("t", r.Get("s"))` | transpiler binding `Src: int64-typed GetOr` (a `cast` if the advisory type differs); VM tier reads the record |
| typed codegen | `r.A > r.B` (coercion per the §2 tables) | `r.T = r.S` (types must agree, else record fallback) | binding `Src: r.C` with a cast when needed; Tier V env gets the cast value |
| generate sql | `a > b` (both `quoteIdent`) | `s AS t` in the projection | identifier, `CAST(c AS BIGINT)` when TYPE differs from the column's kind (`sqlColumnKinds`) |
| generate ssql | verbatim (`Op.Argv`) | verbatim | verbatim |

Completion: the FIELD positions in all three flags are `FieldsFromFlag`
slots, so Tab after `-if-field a gt` offers field names with no sigil
trick. `-spec-json` shows them as ordinary flags with `fields` args.

## 5. Safety, restated

After this DFC the grammar of a value slot is: **a literal, of the type
the flag declares.** No slot's meaning depends on its value's spelling.
That is what lets a document builder (DFC134 §5) emit any string into
any value slot, and what the injection fuzz assumes. `-if-field`,
`-set-field` and `-param-field` add slots whose kind is *field*, checked
against the schema, loud when unknown; they add no rule to the value
slots.

## 6. Tests

- Equivalence cases (`TestPipelineEquivalence`, every lane incl.
  DuckDB), on a shuffled fixture with two numeric columns whose order
  differs and a text pair: `if_field_numeric`, `if_field_text_ops`,
  `if_field_absent_side`, `set_field_copy_and_absent`,
  `param_field_literal_vs_column` (the same expression with `-param` and
  `-param-field`, distinct goldens), `param_field_cast_view`,
  `param_field_collision_is_error`.
- `TestArgumentsAreNotSyntax` gains `@token` as a value in `-if` and
  `-param`: it is a literal, matches nothing, and the pipeline succeeds.
- The random tester draws `-if-field` and `-param-field` (a typed view
  of the string column as `string`, and of `k`/`n` as `float`).
- `TestExprGoDifferential` entries for the field binding.

## 7. Open points for review

1. **Names.** `-if-field` / `-set-field` / `-param-field` follow the
   existing `-set-expr` / `-set-bucket` pattern (suffix names the kind
   of the value slot). Alternatives: `-if-col`, `-cmp`. The suffix form
   completes well (`-if<TAB>` lists `-if -if-expr -if-field`).
2. **Operators on `-if-field`.** All ten, or only the six comparisons?
   `contains`/`regex` with a field as the pattern is legitimate (`-if-field
   path startswith prefix`) and cheap; regex compiles per row in exec.
3. **`-param-field` type mismatch.** Strict cast (loud on a value not of
   TYPE, as proposed) vs `-invalid missing` style opt-out. Strict
   matches `-param` and `cast`.
4. **`update -set-field` when SOURCE is absent.** Leave target absent
   (proposed) vs delete the target if it existed. Absent-preserving is
   DFC124's rule; deleting is a different verb.
5. **DFC101 Gap 1.** Mark it `Deprecated-by: DFC135` for the sigil only;
   DFC101's main argument (keep structured flags; expressions grow) is
   unaffected and stays the decision record.

## 9. What building it found (2026-09-22)

Built as designed, with two departures and four pre-existing defects.

Departures:
- **Record codegen leaves `-param-field` to the VM tier** rather than
  binding a typed `GetOr` natively: absence is a runtime property and the
  VM is where the absent-value rule is implemented once (`runtime.ErrAbsent`).
  Typed mode binds natively (a struct is never absent).
- **`-if-field`'s mixed-kind rule is "false", not "loud"** (§3.1 said
  loud): `-if` itself is silently false on `age gt abc`, and `FieldOp`
  follows `-if`. Loudness for a numeric/text pairing would be a change to
  `-if` too, and is a separate decision (TODO).

Found and fixed, none about the new flags:
1. **exec `update` zero-filled a new field on unmatched rows** (`false`,
   `0`, `""`); record codegen left it absent, SQL refused the case.
   Pre-DFC124 behaviour; exec now leaves it absent.
2. **`generate sql` ignored an `-if-expr` written after the `-set` it
   guards** in the same clause (conditions were snapshotted per `-set`).
3. **`generate ssql`'s where rewrites dropped unknown flags**: the
   canonicaliser turned `-if-expr 'k >= "b"' -param-field k string 1st`
   into `-if k ge b`. Its structured view models `-if`/`-if-expr`/`+`
   only; every rule using it now skips a `where` with anything else.
4. **An absent `-param-field` column compared as nil** (`l != r` true in
   exec, false in SQL), found by the random tester within 1,500 cases of
   adding the field forms. Now: no value → condition false, set nothing.

5. From the crash sweep (DFC133 instrument 2), on the new flags
   themselves: `update -set-field a nosuchfield` and `-if-field a eq
   nosuchfield` exited 0 (update validated no read fields at all), and
   `where -param-field x x nosuchfield` exited 0 because a clause with no
   expression was skipped before its parameters were parsed. Both loud
   now; the sweep's create-a-field exemption is per argument, so
   `-set-field`'s target may be new and its source must exist.

Gates: nine equivalence cases in every lane (sabotage-checked: a reversed
int comparison in `FieldOp` fails four lanes), `@name` and `@@name` as
literals in `TestArgumentsAreNotSyntax`, the random tester drawing
`-if-field` and `-param-field` (6,000 pipelines over four seeds agree).

## 8. References

- [DFC101](./rvalues-as-expressions.md) — the `@field` sigil this
  replaces; the structured-flags rationale it keeps.
- [DFC134](./dfc134_pipelines_as_data.md) — a slot's kind is fixed by
  the flag; `-arg`, `-param`, the document runner, the injection fuzz.
- [DFC124](./dfc124_missing_values.md) — absence rules the
  absent-side cases follow.
- [DFC128](./dfc128_json_interchange_and_time_type.md) — the absent-value negation
  rule (`NOT COALESCE`).
