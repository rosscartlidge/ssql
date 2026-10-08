# Nested tables: the one structured value is a relation, and commands scope into it

Reference: DFC145
Created: 2026-10-08
Last modified: 2026-10-08

[Back to Index](./README.md)

Status: **decision record, not a plan.** Ross, 2026-10-08, after v4.113.0
shipped DFC144 Levels 0–2: *"what do you think of the idea of having the
only structured data a real jsonl table — which would support real ssql
commands on it?"* and then *"let's write it up in a DFC and update DFC144
to point to it."* This is the write-up: what the idea is, why it is the
natural end-state of DFC144 rather than a different direction, the
grammar that fits ssql's philosophy, what it costs, and what it would
take, so that a future session can build it from here instead of
rediscovering it.

## 1. The idea

Today a nested value is one `json` field holding its text (DFC144 Level
0), opened by expressions (Level 1) and by `explode`/`flatten`/dotted
paths (Level 2). Those are the right floor. The idea is a tier above it:
**the one structured value ssql takes seriously is a table** — a list of
objects with the same keys, which is exactly a JSONL stream inside a
field — and **every ssql command can run on it**, one level down, with
the same flags and the same semantics it has at the top level.

```
{"order_id":1001,"customer":"alice","items":[{"sku":"PEN","qty":3,"price":1.5},{"sku":"INK","qty":1,"price":12}]}
```

`items` is a table. One should be able to say "keep the items with
qty > 1", "sort each order's items by price", "the dearest item of each
order", "total qty per sku within each order" without exploding first,
and get an order row back with its `items` table changed — then explode
when the flat shape is wanted.

This is the **nested relational model** (NF², the nested relational
algebra of Jaeschke and Schek): relation-valued attributes, closed under
the usual operators applied at depth, plus two structural operators,
**nest** and **unnest**, that move a sub-relation into and out of its
parent. ssql already has most of it in disguise:

| Nested relational model | ssql today |
|---|---|
| a relation-valued attribute | `group-by`'s `_group` field, an `iter.Seq[Record]` the aggregates consume (`GroupByFields`, `sql.go`); `Value` admits `iter.Seq[Record]` and `Record` |
| nest | `group-by -collect` (a one-column case); `group-by` itself before `Aggregate` |
| unnest | `explode` (lists), `flatten` (one-row tables, i.e. objects); the library's `DotFlatten`/`CrossFlatten` (`core.go`) over `iter.Seq` fields |
| the table's schema | the `_schema` header, which a nested JSONL stream carries just as well |

A list of scalars is a one-column table (`[{"value":"go"},…]`); an
object is a one-row table; a list of objects is a table outright. The
model covers everything Levels 0–2 handle, and names the case that
deserves commands.

## 2. Why it fits ssql, and why now is not the moment

**Fits.** One semantics: a stage reads rows and writes rows; scoped, it
reads the rows of a field and writes them back. No new verbs beyond
`nest` (the missing inverse of `flatten`, DFC144 follow-up) and the
existing `explode`. The five lanes already have the operators; scoping
is a lowering, not a new implementation. The typed lane gets what Level
3 of DFC144 reached for — a `[]ItemRow` field in the generated struct —
with a reason: it is a table with a schema, not arbitrary JSON. And it
is the argument of DFC144 §6a carried through: scalar in the operators
(the operators do not change), honest about the shape (the table stays a
table until the user unnests it).

**Not now.** Nobody has asked for an in-row operation that `explode |
… | -collect` does not express; the Structured Data codelab
([cli-nested-data](../cli-nested-data.md)) covers every case we have
with the three moves. The cost (§5) is real: a second type tier in the
header, inference that must decide "table or json", and `-in` lowered
in every lane. Build it when a workload shows the explode-and-collect
round trip is the bottleneck — in keystrokes or in rows.

## 3. The grammar: scoping, not a sub-language

The question Ross asked in DFC144 was how to name nested things while
keeping arguments plain, completable words. For operating on a nested
table the answer is **one scoping flag on every row command**:

```bash
ssql where -in items -if qty gt 1                 # keep items with qty > 1, per order
ssql sort -in items -desc price                   # each order's items by price
ssql top -in items 1 -field price                 # the dearest item of each order (items keeps one row)
ssql group-by -in items sku -sum qty total        # per order: items rolled up by sku
ssql update -in items -set-expr line 'qty * price'
ssql include -in items sku qty                    # project the sub-table's columns
ssql explode items                                # unnest, when the flat shape is wanted
ssql nest items sku qty price                     # the inverse: columns into a one-row table per row
```

- `-in FIELD` is one token, completable from the nested schema
  (`SSQL_MODE=schema` already lists `items.sku`, `items.qty`; a table
  field would list its columns). It carries no internal structure.
- Field names inside the scoped command resolve in the sub-table
  (`qty`, not `items.qty`), exactly as the command resolves them at the
  top level. The parent's fields are not visible; an expression that
  needs the parent uses the top-level form (`update -set-expr n
  'len(items)'`) or explodes.
- A scoped command is row-local: it changes one row's table and emits
  the row. Commands that are not row-local at the top level (`sort`,
  `group-by`, `top`, `distinct`) are row-local when scoped, which is what
  makes them cheap: no barrier, no spill, the sub-table is one row's
  worth of data.
- The alternatives were weighed and lose. A quoted sub-pipeline
  (`ssql with items 'where … | sort …'`) is a second grammar inside an
  argument, the thing the philosophy forbids. Expressions over lists
  (`filter(items, {.qty > 1})`) work today but re-implement `where`,
  `sort`, `group-by` in a second dialect, which is the "parallel family
  of list functions" DFC144 §6a argued against. `-in` reuses the
  commands people already know.

## 4. The type tier

A `json` value is a **table** when every element is an object with the
same keys and each key has one scalar (or table) type — DuckDB's
`read_json` rule for inferring `STRUCT[]`, and the rule `flatten` already
enforces for a single row's object. The header spelling, reserved by
DFC144 §8 decision 7 and now with a name that says what it means:

```
"items": "table{sku:string,qty:int,price:float}"
"tags":  "table{value:string}"          -- a list of scalars: one column, value
"addr":  "struct{city:string,zip:string}" -- an object: a one-row table, kept as DFC144 spelled it
"meta":  "json"                          -- ragged or mixed: the untyped fallback stays
```

Inference happens where types are inferred today (the sample in schema
mode and typed codegen; `InferTypeString` for the wire); a column that is
a table in some rows and not in others is `json`. `cast -type items
table` asserts it and fails on the first row that does not fit, as
`cast` does.

## 5. What it costs, per lane

| | exec | record codegen | typed codegen | SQL | library form |
|---|---|---|---|---|---|
| `-in FIELD` | open the field's table (lazy, once per row), run the command's existing filter over it, close it back to a `json`/table value | the same `Filter[Record,Record]` applied inside a per-row closure | a `[]ItemRow` field; the scoped command's typed template over the slice (serial per row; the parallel form is the parent stream) | DuckDB: `list_filter`/`list_sort`/`list_aggregate` or a `LATERAL UNNEST … GROUP BY` round trip; Postgres/DataFusion refuse most | unchanged (rows in, rows out) |
| `nest FIELD COL…` | `struct_pack` per row; drop the columns | same filter | synthesised sub-struct | `struct_pack` (DuckDB), `jsonb_build_object` (PG) | unchanged |
| the `table{…}` type | inference rule; `json` fallback | advisory types | `[]T` and nested struct types; the fast JSONL decoder's slice path (exists, falls back to `encoding/json`) | STRUCT[] assumed on DuckDB | row types gain slices |

The equivalence gate carries it as it carried DFC144: a nested fixture,
one case per scoped command, the `go-lib` lane included.

## 6. Order of work, when the time comes

1. `nest FIELD COL…` — small, stands alone, closes the DFC144 follow-up
   (flatten's inverse); DuckDB `struct_pack`.
2. The `table{…}` type in the header and `SSQL_MODE=schema`, with
   `json` as the fallback; `cast -type F table`.
3. `-in FIELD` on `where`, `sort`, `top`, `include`, `exclude`, `update`
   in exec and record codegen — a per-row closure around the existing
   filters; the typed lane falls back to record mode at first, as
   dotted paths do.
4. `-in` on `group-by` and `distinct`; the SQL lowerings that DuckDB can
   express; refusals by name elsewhere.
5. Typed: `[]T` fields and scoped typed templates — the speed on nested
   data, last, once the semantics are pinned by the gates.

## 7. What the nested relational literature and the systems do about the problems

Ross asked for research on nested relational models and how they handle
the problems. Seven problems recur across forty years of it; for each,
what the literature found, what the systems do, and where ssql stands.

### 7.1 Nest and unnest are not inverses

Jaeschke and Schek introduced NF² relations with set-valued attributes
and the nest/unnest operators (PODS 1982); Schek and Pistor (VLDB 1982)
gave the algebra its first full treatment. The first thing everyone
found is that `unnest ∘ nest` is the identity but `nest ∘ unnest` is
not: unnesting an empty sub-relation produces no tuples, so the parent
tuple vanishes and cannot be re-nested; unnesting and re-nesting also
merges parents that differ only in their sub-relations. Roth, Korth and
Silberschatz (TODS 1988) gave the condition under which the round trip
holds — **Partitioned Normal Form**: the scalar attributes at each level
form a key — and Fischer and Van Gucht (VLDB 1985) showed nested
relations are closed under nest but not under unnest, and that the
RKS/Abiteboul–Bidoit assumptions make same-level nests commute but
exclude nulls. Tansel and Garnett (TODS 1992) found the RKS algebra and
calculus do not coincide because of exactly these keying problems.

*Systems:* SQL's `UNNEST` drops the empty-list rows (DuckDB, BigQuery,
Postgres), and offers a `LEFT JOIN LATERAL UNNEST` to keep them with
NULL; Dremel (Melnik et al., VLDB 2010) and therefore Parquet encode the
difference between "list absent", "list empty" and "element null" in
**definition levels**, so columnar storage never loses it.

*ssql:* `explode` drops the row by default and `-keep-empty` keeps it
with no value — the two SQL forms. `-collect` after `explode` is the
re-nest and merges parents by the group key, which is PNF's condition
stated as a flag: the user names the key. The one place ssql cannot
tell absent from empty is the typed lane's string field for a `json`
value (`JSONOrNull`: `""` is "no value"; `has(addr)` is true there and
false in exec — the DFC144 note), which is the Dremel problem in
miniature and which a `table{…}` field with a real slice type would fix.

### 7.2 Does nesting add expressive power?

Paredaens and Van Gucht (PODS 1988; JCSS 1992) proved the nested algebra
is **conservative** over the flat one: a query with flat input and flat
output that uses nest/unnest internally can always be written without
them. Van den Bussche (TCS 2001) gave a direct simulation proof; Wong
extended it to any fixed nesting depth with a terminating normalisation
algorithm, which is the basis of the nested relational calculus (NRC)
compilers in Kleisli, Links and Ferry. Adding a powerset operator
breaks conservativity (transitive closure becomes expressible); nest
alone does not.

*Consequence for ssql:* for a flat result, `explode | … | -collect`
loses nothing. That is the theorem behind DFC144's choice to build the
structural commands before a typed nested tier, and behind this
document's "not now": scoping (`-in`) buys convenience and performance,
not expressiveness, for flat answers. Where it buys expressiveness is
**nested results**: Cheney, Lindley and Wadler's query shredding (SIGMOD
2014) exists because a flat engine cannot return a nested collection
without either one correlated query per parent or a fixed number of
flat queries stitched afterwards; a `-in` stage that returns the row
with its sub-table changed produces the nested result directly.

### 7.3 How to query inside a nested value

Three answers, in historical order.

1. **Operators at depth** (Schek and Scholl's NF² algebra, 1986; the
   DASDBS and AIM-P prototypes): the relational operators are allowed
   inside a projection, so `π[A, σ[q>1](items)]` selects within the
   sub-relation and keeps the parent. This is the algebra ssql's
   `-in FIELD` lowers to — one operator, applied one level down.
2. **Unnest, operate, re-nest** (SQL, from SQL:1999 collection types
   through `LATERAL`/`CROSS JOIN UNNEST` in Postgres, BigQuery, DuckDB):
   explode to rows, use the flat operators, `GROUP BY` the parent key
   with `ARRAY_AGG`. Correct by the conservativity theorem, and needs
   the parent key (PNF again); verbose and, naively planned, quadratic.
3. **Higher-order functions on collections** (Spark 2.4's `transform`,
   `filter`, `aggregate`, `exists` with lambdas, 2017–2018; DuckDB's
   `list_filter`/`list_transform`/`list_reduce`; Snowflake's `FLATTEN`
   plus VARIANT functions): a second, functional dialect for the inside
   of a value, added because option 2 was "cumbersome" (Databricks'
   own word).

*ssql:* option 3 exists today through the expression language (Level
1). Option 2 is `explode | … | -collect` (Level 2). DFC145 proposes
option 1 as the primary form because it reuses the commands and their
flags rather than a dialect; options 2 and 3 remain for what scoping
does not cover (a result that joins the sub-table to another source,
say).

### 7.4 The cost of the round trip

The unnest/re-group idiom is, in relational terms, a dependent join:
for each parent, run a query over its sub-relation. Neumann and Kemper
("Unnesting Arbitrary Queries", BTW 2015) showed every correlated
subquery can be rewritten to a dependent-join-free form, which is how
HyPer, DuckDB and Umbra avoid nested-loop evaluation; it is still a join
plus a group-by with a hash table keyed by the parent. A scoped operator
has no join at all: the parent row carries its own sub-relation, the
operator runs over that small set and writes it back, and the parent's
identity is positional. This is why §3 notes that `sort -in items` and
`group-by -in items` are row-local — the barrier that makes the
top-level versions expensive does not exist one level down.

### 7.5 Heterogeneous and schema-less data

NF² assumed a fixed schema at every level. Real JSON is ragged: keys
present in some rows, lists of mixed types, depth that varies. Every
system that met real JSON kept **two tiers**: a typed nested tier
(BigQuery `STRUCT`/`ARRAY`, DuckDB `STRUCT`/`LIST`, Spark `StructType`/
`ArrayType`, Parquet groups) and an untyped escape (BigQuery and DuckDB
`JSON`, Snowflake `VARIANT`, Spark strings), with schema-on-read
inference deciding per column (DuckDB's `maximum_depth` fallback,
`union_by_name`). The lesson is settled: do not try to type everything.

*ssql:* `json` is the untyped tier (Level 0) and `table{…}`/`struct{…}`
would be the typed one (§4), inferred where the sample is consistent and
declined where it is not — the same shape.

### 7.6 Sets versus lists, and order

NF² sub-relations are sets: unordered, no duplicates. JSON arrays, and
the data people actually have, are lists: ordered, duplicates allowed.
SQL:2003 distinguishes `ARRAY` (ordered) from `MULTISET` (unordered,
with duplicates) and gives `UNNEST … WITH ORDINALITY` to recover
position; Dremel's **repetition levels** preserve list order in
columnar form; Spark and DuckDB arrays are ordered and 1-indexed in SQL,
0-indexed in their JSON path syntax.

*ssql:* a nested list is ordered (`tags.0`, `tags.-1`, `explode`
preserves element order within a row), and the row model has no
duplicate elimination unless asked (`distinct`). A scoped `distinct -in
items` would be the set operation when wanted. The 0/1-based split is
real: ssql indexes from 0 everywhere (expressions, paths) and the SQL
translator shifts by one.

### 7.7 Storage and the typed lane

Dremel's contribution was that nested data can be stored **columnar**
without flattening — one column per leaf path plus the two level
columns — and reassembled with a finite-state machine; Parquet is that
design and is why Parquet carries nested types natively. ssql's typed
lane is a struct-per-row runtime, so a nested table there is a slice
field (`[]ItemRow`), not a striped column; it would read Parquet's
nested columns through the existing Arrow/Parquet readers (which today
stringify them, DFC144 §1) and gain the speed on nested data that
DFC144 Level 3 promised. That is step 5 of §6 and the last thing to
build.

### Sources

- Jaeschke, Schek — Remarks on the algebra of non first normal form relations, PODS 1982 ([ACM](https://www.doi.org/10.1145/588111.588133)); Schek, Pistor — Data structures for an integrated data base management and information retrieval system, VLDB 1982 ([vldb.org](https://vldb.org/dblp/db/conf/vldb/SchekP82.html)).
- Fischer, Van Gucht — Determining when a structure is a nested relation, VLDB 1985 ([PDF](https://www.vldb.org/conf/1985/P171.PDF)).
- Roth, Korth, Silberschatz — Extended algebra and calculus for nested relational databases, TODS 13(4) 1988 ([ACM](https://www.doi.org/10.1145/49346.49347)); Tansel, Garnett — On Roth, Korth and Silberschatz's extended algebra and calculus, TODS 17(2) 1992 ([Bilkent](https://repository.bilkent.edu.tr/items/691499db-cab3-48b0-a960-3e82091e59f9)); Garani — Nest and unnest operators in nested relations, Data Science Journal 2008 ([CODATA](https://datascience.codata.org/articles/319)).
- Paredaens, Van Gucht — Possibilities and limitations of using flat operators in nested algebra expressions, PODS 1988 ([PDF](https://legacy.cs.indiana.edu/~vgucht/p65-paredaens.pdf)); Converting nested algebra expressions into flat algebra expressions, TODS 1992 ([PDF](https://legacy.cs.indiana.edu/~vgucht/p29-paredaens.pdf)); Wong's conservativity theorem as summarised in [Mixing set and bag semantics](https://arxiv.org/pdf/1905.02069).
- Cheney, Lindley, Wadler — Query shredding: efficient relational evaluation of queries over nested multisets, SIGMOD 2014 ([arXiv](https://arxiv.org/pdf/1404.7078)).
- Neumann, Kemper — Unnesting arbitrary queries, BTW 2015 ([PDF](https://btw-2015.informatik.uni-hamburg.de/res/proceedings/Hauptband/Wiss/Neumann-Unnesting_Arbitrary_Querie.pdf)).
- Melnik et al. — Dremel: interactive analysis of web-scale datasets, VLDB 2010 (repetition and definition levels; the Parquet encoding).
- Databricks — Working with nested data using higher-order functions in SQL (2017) ([blog](https://databricks.com/de/blog/2017/05/24/working-with-nested-data-using-higher-order-functions-in-sql-on-databricks.html)); Introducing new built-in and higher-order functions for complex data types in Apache Spark 2.4 (2018) ([blog](https://databricks.com/de/blog/2018/11/16/introducing-new-built-in-functions-and-higher-order-functions-for-complex-data-types-in-apache-spark.html)).
- DuckDB nested types, UNNEST and JSON documentation (read for DFC144 §2).

## 8. Related

- [DFC144](./dfc144_non_scalar_values.md) — the survey, Levels 0–2
  (built, v4.113.0), §6a the position this document carries forward,
  §8 decision 7 (the reserved spelling).
- [DFC052](./compound-types-investigation.md) — the first survey
  (superseded by DFC144).
- [DFC129](./dfc129_groupby_aggregates.md) — `-collect`, the one-column
  nest.
- [DFC115](./dfc115_commands_are_the_authority.md) — why scoping is a
  flag on the command and not a parser elsewhere.
- [Structured Data codelab](../cli-nested-data.md) — what the three moves
  cover today; the yardstick for whether this tier is needed.
- Jaeschke & Schek, "Remarks on the algebra of non first normal form
  relations" (PODS 1982) — nest/unnest and the closure of the algebra.
