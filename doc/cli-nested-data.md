# Structured Data with ssql

A guided tour of ssql on data that has structure inside a field — a list
of tags, an address object, an order's list of line items — the shape
JSON APIs, logs and document stores produce. ssql's rows are flat; this
codelab is about the four moves that let flat tools work on nested data:
**look inside** a value (dotted paths), **compute** on it (expressions
over lists and objects), **explode** a list into rows, and **flatten** an
object into columns.

It is a branch of the [learning path](README.md#learning-path) after the
[CLI Codelab](cli-codelab.md), which introduced the same ideas in one
section ([Nested JSON](cli-codelab.md#nested-json-lists-and-objects-in-a-field));
it assumes that codelab's vocabulary — `from … | … | to table`, `where`,
`group-by`, `update -set-expr` — and the prompt's Tab / Ctrl-O. Every
block *is* run by the codelab runner
(`./codelab-run.sh doc/cli-nested-data.md` beside the data `ssql codelab`
writes, or [`doc/codelab-data/codelab-run.sh`](codelab-data/codelab-run.sh)
`doc/cli-nested-data.md` in the repository), so what you read is what
happens.

## Table of Contents

1. [Setup and the data](#1-setup-and-the-data)
2. [One representation: a nested value is a `json` field](#2-one-representation-a-nested-value-is-a-json-field)
3. [Looking inside: dotted paths](#3-looking-inside-dotted-paths)
4. [Computing on lists and objects: expressions](#4-computing-on-lists-and-objects-expressions)
5. [Explode: a list becomes rows](#5-explode-a-list-becomes-rows)
6. [Flatten: an object becomes columns](#6-flatten-an-object-becomes-columns)
7. [The whole move: line items to revenue](#7-the-whole-move-line-items-to-revenue)
8. [Collect: the way back](#8-collect-the-way-back)
9. [Text that holds JSON: cast](#9-text-that-holds-json-cast)
10. [The same pipelines as Go and as SQL](#10-the-same-pipelines-as-go-and-as-sql)
11. [When to explode and when not to](#11-when-to-explode-and-when-not-to)
12. [Reference](#12-reference)

---

## 1. Setup and the data

`ssql` on your PATH, as in the [CLI Codelab's Setup](cli-codelab.md#1-setup).
Work in an empty directory and write the codelab data set there (the
runner has already done this for you):

```bash
[ -f events.jsonl ] || ssql codelab . >/dev/null
head -2 events.jsonl
head -1 orders_nested.jsonl
```

Two files are nested. `events.jsonl` is six rows with a `tags` **list**,
an `addr` **object** and a `scores` list — one row has an empty list and
one has no `addr` at all, on purpose. `orders_nested.jsonl` is five
orders whose `items` field is a **list of objects**, each with `sku`,
`qty` and `price`: the shape every e-commerce export has, and the one
flat tools struggle with.

## 2. One representation: a nested value is a `json` field

Read a file and look at it. A nested value travels through the pipeline
as one field whose type is `json`, holding the value as JSON text, and
is written back exactly as it came:

```bash
ssql from events.jsonl | ssql sort id | ssql to table
```

The schema header says what each field is. Ask for it the way
completion does, and notice the nested names it lists for the object
field — `addr.city`, `addr.zip` — which is why Tab (via Ctrl-O) can
complete them at the prompt:

```bash
(export SSQL_MODE=schema; ssql from events.jsonl) | ssql generate schema -data | ssql to table
```

Nothing is parsed that you do not use. A pipeline that only sorts and
writes never opens the JSON; a stage that names something inside it
does, once per row, for that field only.

## 3. Looking inside: dotted paths

Wherever a command takes a **field name**, a dotted path names something
inside a nested value: `addr.city` reads a key, `tags.0` the first
element of a list (`tags.-1` the last), `a.b.c` walks down. There is no
new flag and no quoting — a path is one word:

```bash
ssql from events.jsonl | ssql where -if addr.city eq NYC | ssql to table user addr.city tags.0 -only
```

```bash
ssql from events.jsonl | ssql where -if-expr 'addr != nil' | ssql sort addr.zip | ssql include id user addr.zip | ssql to table
```

```bash
ssql from events.jsonl | ssql where -if-expr 'addr != nil' | ssql group-by addr.city -count n | ssql sort -desc n | ssql to table
```

A path that leads nowhere is an unknown field, and ssql says so rather
than returning empty columns:

```bash
ssql from events.jsonl | ssql where -if addr.country eq AU 2>&1 | head -1 || true
```

If a field's *name* contains a dot — a CSV header `a.b` — that field
wins over a path into a field `a`; nothing you already have changes
meaning.

## 4. Computing on lists and objects: expressions

Inside an expression a nested value **is** the list or object it holds,
so everything in [EXPRESSIONS](EXPRESSIONS.md) applies to it: `len`,
indexing, membership, `sort`, `filter`, `map`, `uniq`, `join`, and
member access with a dot:

```bash
ssql from events.jsonl | ssql where -if-expr '"go" in tags and addr != nil' | ssql update -set-expr n 'len(tags)' -set-expr city 'addr.city' | ssql include user n city | ssql to table
```

```bash
ssql from events.jsonl | ssql where -if-expr 'len(scores) > 0' | ssql update -set-expr best 'sort(scores)[-1]' -set-expr mean 'sum(scores) / len(scores)' | ssql include user best mean | ssql to table
```

A result that is itself a list or an object is stored as a `json` value,
not as text, so the next stage can open it again or explode it:

```bash
ssql from events.jsonl | ssql update -set-expr big 'filter(scores, {# >= 9})' | ssql include user big | ssql to table
```

Two things to know. `sort(scores)[-1]` on an empty list is an error
(there is no last element), which is why the block above filters first:
ssql stops and names the expression rather than inventing a value. And
a row with no `addr` has `addr` equal to `nil` in an expression, so
`addr != nil` is the test for "has an address" that works everywhere.

The same holds in `group-by -expr`, where aggregates follow SQL's rule
and do not nest: the outer `sum`, `max`, `avg` or `count` aggregates
over the group, and inside its argument the same names are the ordinary
list functions over one row's own list. So `sum(sum(scores))` totals
every score in the city, `max(len(tags))` is the widest tag list, and a
closure's `#` is the closure's own element:

```bash
ssql from events.jsonl | ssql where -if-expr 'addr != nil' | ssql group-by addr.city -expr 'sum(sum(scores))' total -expr 'max(len(tags))' widest -expr 'len(filter(scores, sum(#) > 10))' big | ssql sort -desc total | ssql to table
```

A bare list field outside an aggregate is the group's array of lists
(`len(filter(scores, …))` above counts rows), inside one it is the row's
list (`sum(scores)` would add lists and stop with an error — write
`sum(sum(scores))`).

## 5. Explode: a list becomes rows

`explode FIELD` is SQL's UNNEST: one output row per element of the list,
the field now holding the element, every other field repeated. It is
the move that turns nested data into the flat rows every other command
is good at:

```bash
ssql from events.jsonl | ssql explode tags | ssql include id user tags | ssql to table
```

```bash
ssql from events.jsonl | ssql explode tags | ssql group-by tags -count n | ssql sort -desc n | ssql to table
```

A row whose list is empty or missing produces **no row** — the UNNEST
rule, and the one `group-by -count` wants. When you need to keep such a
row (the way a left join keeps an unmatched one), `-keep-empty` gives it
once with no value in the field:

```bash
ssql from events.jsonl | ssql explode tags -keep-empty | ssql where -if-expr 'tags == nil' | ssql include id user | ssql to table
```

Exploding something that is not a list is an error, not a guess: a
scalar has no elements, and an object is `flatten`'s job.

## 6. Flatten: an object becomes columns

`flatten FIELD` is SQL's `s.*`: the object's keys become sibling fields
named `FIELD.key`, in the object's own key order, and the object itself
is gone (`-keep` keeps it):

```bash
ssql from events.jsonl | ssql flatten addr | ssql include id user addr.city addr.zip | ssql to table
```

A row that has no object passes through with those columns empty. The
**key set is fixed by the first row's object**, the way a DuckDB STRUCT
has the same keys in every row: a later row with a key the first did not
have is an error that names the key, rather than a column that silently
exists for some rows and not others. Nested objects inside the object
stay as JSON unless you ask for more levels with `-depth N`.

Once flattened, the columns are ordinary fields — and expressions reach
them by the same dotted name, `addr.city`, even though no field called
`addr` remains:

```bash
ssql from events.jsonl | ssql flatten addr | ssql where -if-expr 'addr.zip != nil and addr.zip > "20000"' | ssql include user addr.city addr.zip | ssql to table
```

## 7. The whole move: line items to revenue

Orders with a list of item objects: explode the list (one row per line
item), flatten the item (its keys become columns), compute, aggregate.
Four stages, each of which you have now seen:

```bash
ssql from orders_nested.jsonl | ssql explode items | ssql flatten items | ssql update -set-expr line_total 'items.qty * items.price' | ssql group-by items.sku -sum line_total revenue -sum items.qty units | ssql sort -desc revenue | ssql to table
```

Per customer, with the group-by expression form — the flattened
columns are reachable as `items.qty` there too:

```bash
ssql from orders_nested.jsonl | ssql explode items | ssql flatten items | ssql group-by customer -expr 'sum(items.qty)' units -expr 'max(items.price)' dearest | ssql sort customer | ssql to table
```

Order 1004 has no items and so contributes no line; if a report must
list every order, explode with `-keep-empty` before aggregating.

When the question is about whole orders rather than line items, the
lists can stay in place: aggregate over each order's list with a nested
aggregate, no explode or flatten needed. Here Dan's empty order counts
as an order with nothing in it, which explode would have dropped:

```bash
ssql from orders_nested.jsonl | ssql group-by customer -expr 'sum(len(items))' lines -expr 'sum(sum(map(items, #.qty)))' units -expr 'avg(sum(map(items, #.qty * #.price)))' avg_order | ssql sort customer | ssql to table
```

## 8. Collect: the way back

`group-by -collect FIELD RESULT` builds a list per group — the inverse of
`explode`. Exploding and collecting round-trip, and the list `-collect`
writes is a real `json` value a later stage can open:

```bash
ssql from events.jsonl | ssql explode tags | ssql group-by user -collect tags all_tags | ssql update -set-expr n 'len(all_tags)' | ssql sort user | ssql to table
```

This is the normalise/denormalise pair. A list inside a row is a small,
nameless table; when you want to filter it, count it or join on its
elements, explode it and use the commands that exist. When you want one
row per entity again, collect.

## 9. Text that holds JSON: cast

A CSV cell can hold JSON text. It is a string until you say otherwise;
`cast -type FIELD json` checks that it is well-formed JSON and makes it a
nested value, after which everything above applies:

```bash
printf 'id,payload\n1,"{""a"":1,""b"":[1,2]}"\n2,"{""a"":2,""b"":[]}"\n' > blobs.csv
ssql from blobs.csv | ssql cast -type payload json | ssql update -set-expr n 'len(payload.b)' | ssql to table
```

A cell that is not JSON stops the pipeline and names it, as every cast
does; `-invalid missing` leaves such cells empty instead.

## 10. The same pipelines as Go and as SQL

Everything above runs in every lane. `generate go` compiles the revenue
pipeline into a program (a typed program reads the nested fields as
text and switches to record mode for the stages that open them; the
`-explain` output says so):

```bash
ssql generate go -run -pipeline 'ssql from orders_nested.jsonl | ssql explode items | ssql flatten items | ssql group-by items.sku -sum items.qty units | ssql sort items.sku | ssql to csv'
```

`generate sql` translates what DuckDB can express natively — DuckDB
reads a JSON file's lists and objects as LIST and STRUCT, so `explode`
is `unnest`, member access is `struct_extract`, `len(tags)` is
`length`, `"go" in tags` is `list_contains` — and refuses, by name, what
it cannot (`flatten`, whose keys are data; `-keep-empty`; a dotted path
in a field position, for now):

```bash
ssql generate sql -pipeline 'ssql from events.jsonl | ssql where -if-expr '"'"'"go" in tags'"'"' | ssql explode tags | ssql include user tags | ssql to csv'
```

```bash
# codelab: skip — needs duckdb on PATH
ssql generate sql -run -pipeline 'ssql from events.jsonl | ssql explode tags | ssql group-by tags -count n | ssql sort tags | ssql to csv'
```

The equivalence gate in the test suite runs each of these pipelines
through the interpreter, the generated programs and DuckDB and insists
the rows agree, which is how the lanes stay honest about nested data.

## 11. When to explode and when not to

The position behind this design ([DFC144 §6a](research/dfc144_non_scalar_values.md)):
normalised at rest, scalar in the operators, honest about the shape at
the edges. A nested value that is just passing through should pass
through untouched — flattening everything on the way in would make the
output differ from the input for a user who never asked. A nested value
you want to *work on* should become rows (explode) or columns (flatten)
early, because every other command — joins, windows, aggregates, sorts —
is built for flat rows and does the job better than a parallel family of
list functions would. Expressions cover the in-row cases (`len(tags)`,
`addr.city`, a filter on a list) without changing shape. Typed nested
schemas — `list<int>`, `struct{city:string}` in the header, struct fields
in generated programs — are deliberately not here yet; `json` is the one
nested type, as DuckDB's `JSON` is the fallback for what its reader
cannot type.

## 12. Reference

| You want | Write |
|---|---|
| a key of an object, an element of a list, as a field | `addr.city`, `tags.0`, `tags.-1`, `a.b.c` — wherever a field name goes |
| the same inside an expression | `addr.city`, `tags[0]`, `tags[-1]`; also `len(tags)`, `"go" in tags`, `sort`, `filter`, `map`, `uniq`, `join` |
| "has an address" | `-if-expr 'addr != nil'` |
| one row per list element | `explode FIELD` (`-keep-empty`: a row with no value for an empty or missing list) |
| an object's keys as columns | `flatten FIELD` (`-depth N`, `-keep`); columns are `FIELD.key`, keys fixed by the first row |
| a total or best over every row's list, per group | `group-by KEY -expr 'sum(sum(scores))' total -expr 'max(max(scores))' best` — aggregates do not nest; the inner one is the row's list function |
| a list per group | `group-by KEY -collect FIELD RESULT` |
| JSON text in a CSV cell as a nested value | `cast -type FIELD json` |
| the schema, with nested names | `(export SSQL_MODE=schema; ssql from f.jsonl) \| ssql generate schema -data` |
| what `generate sql` will and will not translate | `explode`, member access, `in`, `len`, list functions on DuckDB; `flatten`, `-keep-empty`, paths in field positions are refused by name |

Where to next: [Signal Processing](cli-signal-processing.md) if your data
is a time series; the [Getting Started Guide (Go)](codelab-intro.md) to
see what `generate go` emits; [EXPRESSIONS](EXPRESSIONS.md) for the full
list and map function set.
