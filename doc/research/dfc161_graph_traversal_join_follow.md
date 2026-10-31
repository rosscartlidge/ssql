# Graph Traversal: `join -follow`, a Join Run to a Fixpoint

Reference: DFC161
Created: 2026-10-31
Last modified: 2026-10-31

[Back to Index](./README.md)

Status: **exploratory, 2026-10-31. Nothing built.** Ross, after DFC160:
"I actually think our biggest gap is not being able to do recursive joins
so we can do graph traversal … we wouldn't need recursion — I think we
could do it with a loop inside join. What is your feeling on the best way
of adding this functionality." DFC160 §4 did not list it; it should have.
It is the one SQL query shape that ssql has no answer to at all: there
is no way to walk an edge list an unknown number of times, short of
writing out N joins.

## 1. The shape, and why a loop is the right reading of it

The motivating example: a table of flights (`src, dst, mins`), and every
route out of Sydney, however many legs:

```sql
WITH RECURSIVE trip(city, hops, mins, path) AS (
    SELECT 'SYD', 0, 0, ['SYD']                       -- the seeds
  UNION ALL
    SELECT f.dst, t.hops + 1, t.mins + f.mins, list_append(t.path, f.dst)
    FROM trip t JOIN 'flights.csv' f ON f.src = t.city   -- one more hop
    WHERE NOT list_contains(t.path, f.dst)            -- never revisit
)
SELECT * FROM trip;
```

Run 2026-10-31 on DuckDB 1.5.0 (list path) and DataFusion 55 (string
path, through `~/.venvs/df`): both return the 11 rows the example claims,
DRW three times at 3, 3 and 4 hops (fixture in §8).

Ross's instinct is right, and it is not a workaround: **every engine
evaluates `WITH RECURSIVE` as a loop.** "Recursive" is the notation; the
evaluation is semi-naive iteration: the working table holds the rows the
last round produced, each round joins only those against the edges, and
the loop stops when a round produces nothing. A join whose right side is
hashed once and whose left side is fed back round after round is exactly
that algorithm. ssql's `join` already hashes the right side (the file)
and streams the left (stdin); a fixpoint mode just keeps going.

What a loop *in ssql* gets that the SQL notation cannot easily express:

- **State across the whole traversal, not just along one path.** SQL's
  working table only knows the current round, so "visit each node once"
  or "keep the cheapest route to each node" needs either enumerating every
  path and picking afterwards (exponential on a dense graph) or an
  engine extension. Only DuckDB has one (`USING KEY`, §5). A Go loop
  keeps a visited map and runs BFS or Dijkstra in O(E log V).
- **Depth-first streaming.** All simple paths can be walked depth-first
  with memory proportional to path length, emitting as it goes. A
  recursive CTE materialises each round's frontier, which for path
  enumeration is the part that grows.

## 2. Where it should live: a mode of `join`

Three homes were considered.

| | form | for | against |
|---|---|---|---|
| **A** | `join EDGES -on NODE SRC -follow DST …` | the hashed side is the file and the fed-back side is the stream, which is join's shape exactly; reuses `-on`, `readAuxInput` (CSV/TSV/JSON edges, procsub for a computed edge list), field completion from the file, and the SQL translator's join pieces; `-asof` is the precedent for a join *mode* with its own flags | join's flag list grows again; several existing flags mean nothing in this mode and must be refused |
| B | `traverse -from SRC -to DST -start SYD` over edges on stdin | the graph is the stream, so a filtered edge list needs no procsub | seeds computed by a pipeline need a file; a second command that hashes a side and probes it, i.e. a second join implementation |
| C | a general `iterate` over a sub-pipeline | the most general fixpoint | ssql has no sub-pipeline grammar; DFC115 says state is pipeline text, and a pipeline inside a flag is the parse-it-elsewhere trap |

**Recommendation: A.** It is the least new surface, and it says what the
operation is: a join, repeated. `-follow` is the mode switch, the way
`-asof` is.

## 3. Proposed grammar

The seeds are the stream; each one is a row whose `-on` left field holds
a starting node. The edges are the file. Output rows have **the seed's
columns**, with the node field updated to the node reached, plus the
accumulator columns the flags name. Edge columns appear only through
accumulators, so the output schema is fixed and the loop's input and
output are the same shape (a recursive CTE requires this too).

```bash
# the default: each city once, by fewest hops (BFS)
printf 'city\nSYD\n' | ssql from csv - \
  | ssql join flights.csv -on city src -follow dst -depth hops -path path

# each city once, by the lowest total of mins (Dijkstra)
  … | ssql join flights.csv -on city src -follow dst -lowest-sum mins total

# every simple route out of SYD (the CTE above, 11 rows)
  … | ssql join flights.csv -on city src -follow dst -all-paths \
         -depth hops -sum mins mins -path path

# any other "best" over all paths — composed from what exists
  … | ssql window -partition city -order hops -order mins -row-number rn \
    | ssql where -if rn eq 1 -if city ne SYD
```

| flag | meaning |
|---|---|
| `-follow FIELD` | the edge's target column; its value becomes the node for the next round. Switches join into traversal mode |
| `-on NODE SRC` | (existing) the seed's node field and the edge's source field; equality is join's key equality, and an empty key never matches |
| `-depth NAME` | hop count; 0 on the seed row |
| `-sum FIELD NAME` | the running total of an edge field along the path; same spelling as `window -sum field result` |
| `-path NAME` | the nodes visited, seed first, as a list |
| `-max-depth N` | stop expanding at N hops |
| `-fewest-hops` | **the default** (accepted explicitly): each node once per seed, at the first round it is reached (BFS) |
| `-lowest-sum FIELD NAME` | each node once per seed, by the lowest total of edge FIELD, written to NAME (Dijkstra; a negative edge weight is an error, not a wrong answer) |
| `-all-paths` | every simple path (no node twice on one path), every prefix its own row: the recursive CTE's result, and exponential on a dense graph |

The three mode flags are mutually exclusive and an error together.
`-lowest-sum` declares its own sum rather than naming a `-sum`, so the
search key and the column cannot disagree; that departs from Ross's
`-lowest-sum NAME` and is proposed, not agreed.

`-lowest-sum` minimises a sum, and only a sum, on purpose. Dijkstra is correct only when extending a route never makes its value
better, and a sum of non-negative weights has that property, which is
why a negative weight is an error. `-fewest-hops` is the same search
with every edge weighing 1. Other accumulators with that property would
get their own modes later, each with a reason, named the same way:
`-highest-min FIELD NAME` (the route whose narrowest edge is widest, e.g.
bandwidth) or `-highest-product FIELD NAME` (the most reliable route over
probabilities). An average does
not have the property: a cheap leg can lower a route's average, so the
best route to a city need not extend the best route to the city before
it. Averages are never a search mode; compute one over the routes
afterwards.

Later (not v1), each needing a reason first: `-min`/`-max FIELD NAME`
(bottleneck routes), `-product FIELD NAME` (bill-of-materials quantity
explosion), `-last FIELD NAME` (a column of the final edge, e.g. the
airline of the last leg), `-set-expr` with left/right scoping.

Refused with `-follow`: `-type`, `-using`, `-as`, `-suffix`,
`-exclude-left/-right`, `-asof*`. They describe how two rows combine into
one, and a traversal does not combine its rows that way.

### Semantics that need to be pinned before any code

1. **The default mode is `-fewest-hops`** (decided 2026-10-31, Ross).
   The cheap mode is the default: `-fewest-hops` and `-lowest-sum` are
   linear and almost always what someone wants for "how do I reach X".
   All simple paths, which matches the CTE and terminates on a finite
   graph but can be exponential, has to be asked for with `-all-paths`.
   The plain form therefore differs from a recursive CTE's result, and
   the docs say so beside the SQL equivalent.
2. **The seed row is emitted** (depth 0), like the CTE's anchor.
3. **Ties are semantic, not order-dependent.** Every lane must choose the
   same row, so `-fewest-hops` breaks ties by fewest hops and then the path
   compared element by element, and `-lowest-sum` by least sum, then fewest
   hops, then path. Neither rule depends on file order, which the SQL
   lanes do not preserve.
4. **Output order**: seed order, then the traversal order (depth-first in
   edge-file order for `-all-paths`; by round for `-fewest-hops`; by settled distance
   for `-lowest-sum`). The SQL lanes are unordered, so equivalence compares
   a multiset unless the pipeline sorts.
5. **`-sum` is an integer sum, so it is int128** (DFC155 C: window and
   pivot sums already are, and a running total along a path is the same
   thing); floats stay float64. SQL casts to HUGEINT / NUMERIC(38) /
   DECIMAL(38,0) in the anchor.
6. **The seed's node value is overwritten.** To keep the origin, copy it
   first (`update -set-field origin city`) or read `-path`'s first element.

## 4. Backends

Six lanes, as always (CLAUDE.md "One Semantics, Many Backends").

- **Library first** (`ssql.Follow(edges, FollowSpec) Filter[Record,
  Record]`), with the CLI a thin layer over it. The edges are
  materialised into an adjacency map in file order, `map[key][]edge`,
  like the hash side of a join. `-all-paths` is a DFS per seed with an
  on-path set; `-fewest-hops` is a BFS per seed with a visited set; `-lowest-sum`
  is Dijkstra with a heap. Memory is O(E), plus O(depth) for `-all-paths` or
  O(V) for the visited set.
- **Record codegen**: one call per stage (`ssql.Follow(...)`), per the
  readability rule.
- **Typed**: `typed.Follow[S, E, O]` with key functions and a synthesized
  output struct (the group-by pattern). **Parallel over seeds**, since
  each seed's traversal is independent: shards are runs of seeds and the
  output is ordered by seed. One seed is serial; parallelising a single
  BFS frontier is a later measurement. **`-path` is a list column**, and
  the typed tier has no list columns (DFC145, deferred), so `-path`
  refuses typed and falls back to record codegen, loudly. `-depth`,
  `-sum`, `-fewest-hops` and `-lowest-sum` are all scalar and typed from day one.
- **`generate sql`**: a recursive CTE whose anchor is the query so far
  (the seeds, with a seed id from `row_number()` so several seeds stay
  apart), whose step joins the edges, and whose guard is the path list.
  DFC156 already put `WITH RECURSIVE` into the assembler.
  - DuckDB: `list_contains` guard. `-fewest-hops` and `-lowest-sum` use `USING KEY
    (seed, node)` with an in-round `QUALIFY row_number() … = 1` carrying
    the §3 tie rule. **The obvious `USING KEY` form is wrong**: without
    the in-round dedupe, two candidates for one node in the same round
    leave an arbitrary one, and the first form tried on 2026-10-31
    reported DRW at 565 min instead of 385. The QUALIFY form below gets
    385. Exactly the kind of bug the equivalence gate exists for.
  - Postgres 16: `<> ALL(path)` array guard (or the `CYCLE` clause). There
    is no `USING KEY`, so `-fewest-hops` and `-lowest-sum` refuse rather than
    enumerate every path and pick (exponential, where the exec lane is
    linear). `-path` output refuses because the dialect refuses nested
    values (DFC160); the guard array is internal and fine. Not run here
    yet: the rig needs the lane's connection settings.
  - DataFusion 55: recursive CTEs work (run above). `array_has` guard;
    same refusals as Postgres. The **rust** lane inherits this through
    the escape hatch (`ctx.sql`), as for every `sqlOnly` layer (DFC157).
- **Rust**: as above, nothing new.

## 5. DuckDB's shortest-path form, as run

```sql
WITH RECURSIVE best(city, hops, mins, path) USING KEY (city) AS (
  SELECT 'SYD', 0, CAST(0 AS HUGEINT), ['SYD']
  UNION ALL
  SELECT f.dst, b.hops + 1, b.mins + f.mins, list_append(b.path, f.dst)
  FROM best b JOIN 'flights.csv' f ON f.src = b.city
  LEFT JOIN recurring.best r ON r.city = f.dst
  WHERE r.mins IS NULL OR b.mins + f.mins < r.mins
  QUALIFY row_number() OVER (PARTITION BY f.dst
          ORDER BY b.mins + f.mins, b.hops + 1, list_append(b.path, f.dst)) = 1)
SELECT * FROM best;
```

This gives PER at 355 (via MEL, ADL) and DRW at 385 (via BNE, CNS), which
is what the example's prose says. This is Bellman-Ford-style relaxation,
not Dijkstra: it is correct for non-negative weights but can revisit a
node many times, so on a large graph the exec lane should win by a wide
margin. That belongs in the scale gate.

## 6. Tests

- Equivalence cases on a **shuffled** edge file with distinct weights,
  covering: cycles (SYD↔MEL), a diamond (several routes to one node),
  a self-loop, a disconnected node, several seeds sharing nodes, a seed
  with no out-edges, empty and integer keys, `-max-depth`, and each mode.
  Goldens come from the DuckDB recursive CTE (independent oracle) and are
  checked by hand once for the flights fixture.
- Tie tests that break on file order: two equal-cost routes, with the
  edge file written so that "first found" and "path-least" disagree.
- Watch it fail: drop the in-round QUALIFY from the DuckDB `-lowest-sum`
  form (the 565-minute bug), and drop the on-path guard (it must hit
  `-max-depth` or the timeout loudly, never hang the gate).
- Scale gate (DFC113): `-fewest-hops` and `-lowest-sum` on a ~1 M-edge random
  graph with a wall-time ceiling; `-all-paths` on a layered DAG with a
  known path count.
- Overflow: `-sum` past 2^63 is an int128, not an error, in every lane
  (`TestIntOverflowEveryLane` row).

## 7. Open questions for Ross

1. ~~Default mode~~ Decided 2026-10-31 (Ross): `-fewest-hops` is the
   default, all paths is `-all-paths`, and Dijkstra is `-lowest-sum` (was
   `-shortest`; `-fewest-hops` was `-first`, which read as "first in the
   file"). Still open: `-lowest-sum FIELD NAME` (declares its own sum,
   my proposal) or `-lowest-sum NAME` over a `-sum`.
2. Spelling of the mode switch: `-follow` (lean), `-to` or `-next`?
3. Are `-fewest-hops` and `-lowest-sum` refusals on Postgres and DataFusion
   acceptable for v1, or do we want the exponential enumerate-and-pick
   form there with a warning?
4. Is a single-seed `-start VALUE` convenience worth adding, so the
   `printf | from csv -` line goes away? It would be the first join flag
   that invents a row.
5. Undirected graphs: a `-both` flag, or tell users to `union` the
   reversed edge list (one line today)?

## 8. Fixture

```
src,dst,mins
SYD,MEL,90
MEL,SYD,90
SYD,BNE,95
BNE,CNS,140
MEL,ADL,75
ADL,PER,190
MEL,HBA,75
BNE,PER,330
CNS,DRW,150
PER,DRW,210
```

## References

- [DFC156](./dfc156_fold_sql_recursive_cte.md): `WITH RECURSIVE` in the
  assembler, and the per-step cost of recursion in DuckDB.
- [DFC160](./dfc160_duckdb_feature_comparison_2026_10.md): the gap list
  this belongs on.
- [DFC145](./dfc145_nested_tables.md): why `-path` cannot be typed yet.
- [DFC115](./dfc115_commands_are_the_authority.md): why not a
  sub-pipeline `iterate`.
- [DFC157](./dfc157_generate_rust.md): the rust lane's escape hatch.
