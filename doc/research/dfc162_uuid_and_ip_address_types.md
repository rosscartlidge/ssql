# UUIDs and IP Addresses: Functions, Types, or Neither

Reference: DFC162
Created: 2026-11-03
Last modified: 2026-11-04

[Back to Index](./README.md)

Status: **survey, 2026-11-03. The UUID functions are built (2026-11-04, §8); the IP part awaits a decision.**

## 0. The prompt

Ross asked "do we have a uuid function". The answer was no. He then
said: "nothing for the moment - write up on uuid alternatives and also
whether we should support a new type - ipv4 and ipv6 and the functions
to go with them".

This doc covers both questions. They are really one question asked
twice: does a value that is text on the wire but has its own order,
equality and operations get a **type**, or **functions over strings**?
The answers differ:

- **UUIDs** are fine as strings.
- **IP addresses** are not, and why they are not decides the options.

Every engine fact below was measured on 2026-11-03:

- DuckDB 1.5.0, with its `inet` extension;
- PostgreSQL 16.15 on the rig;
- DataFusion 55 through its Python bindings;
- ssql v4.137.1.

## 1. Where ssql stands (v4.137.1)

### What exists

- No `uuid` function, no IP function, and nothing about either in
  TODO.md.
- The nearest functions are `hash(n[, seed])` and the hex digests
  `md5`, `sha1`, `sha256`.
- `hash` is the precedent that matters here. It is defined to give the
  same value in every lane and on every machine. The digests translate
  to each engine's own function, and `sha1` is refused on Postgres
  (pgcrypto) and DataFusion (it has no sha1).

### IP addresses as strings sort wrong

```
$ printf 'ip\n9.0.0.1\n10.0.0.9\n192.168.0.2\n' | ssql from csv | ssql sort ip
10.0.0.9
192.168.0.2
9.0.0.1
```

String comparison also gets these wrong:

- equality across spellings: `::1` vs `0:0:0:0:0:0:0:1`, and `2001:DB8::1`
  vs `2001:db8::1`;
- containment ("is this address in 10.0.0.0/8");
- ranges and `min`/`max`.

`where -if ip lt 10.0.0.0` is quietly wrong. That breaks the "never
silently wrong" rule, but nothing can catch it, because nothing in the
pipeline knows the column holds addresses.

### Two Parquet bugs found while probing (independent of any decision)

**Both fixed 2026-11-03.** The record and typed readers now map fields
to leaves through `internal/pqcols`. A nested column reads as `json`, a
map as an object as DuckDB writes it. A UUID column reads as its
canonical text. The equivalence cases `parquet_nested_uuid` and
`parquet_past_nested_columns` read a DuckDB-written file.

1. **A Parquet `UUID` column reads as base64.** DuckDB writes `UUID` as
   `FIXED_LEN_BYTE_ARRAY(16)` with the UUID logical type. ssql reads it
   as raw bytes and prints `a6e4EJ2tEdGAtADAT9QwyA==` where the value
   is `6ba7b810-9dad-11d1-80b4-00c04fd430c8`. The fix is in the reader
   whatever we decide here: a UUID logical type, or Arrow's
   `arrow.uuid` extension (arrow-go has `arrow/extensions/uuid.go`),
   reads as the canonical string.
2. **A struct column shifts or drops columns, silently.**
   `readParquetFromReaderWithColumns` (`parquet.go`) passes top-level
   *field* indices to `pqarrow`'s `ReadRowGroups`, which takes *leaf
   column* indices. A struct has one field but several leaves, so:

   | File | ssql reads |
   |---|---|
   | `{x:1, y:'q'} t` | `t = {"x":1}` (y lost) |
   | `{x:1, y:'q'} t, 7 n` | `t` whole, `n` gone |
   | `7 n, {x:1, y:'q'} t` | `n`, `t = {"x":1}` |
   | `{a,b,c} s, {x,y} t` | `s = {a,b}`, `t` gone |

   This is data loss with no error, on any Parquet file that has a
   nested column. `parquetChunkRanges` beside it already maps fields
   to leaves by `ColumnPath()[0]`; the read should do the same, or ask
   pqarrow's manifest. It needs a test with a struct column in the
   middle of the file.

   DuckDB's own INET in Parquet goes through this path. It writes
   `INET` as `struct(ip_type UTINYINT, address DOUBLE, mask USMALLINT)`,
   and ssql reads `{"ip_type":2}`.

## 2. What the engines support

### UUIDs

| | DuckDB 1.5 | Postgres 16 | DataFusion 55 | Go (in ssql's deps) |
|---|---|---|---|---|
| Type | `UUID` (128-bit; sorts like the canonical text) | `uuid` | none: `uuid()` returns `Utf8` | none needed; `[16]byte` |
| v4 random | `uuid()`, `gen_random_uuid()`, `uuidv4()` | `gen_random_uuid()` (PG13+) | `uuid()` | `crypto/rand`, ~10 lines |
| v7 time-ordered | `uuidv7()`, `uuid_extract_timestamp`, `uuid_extract_version` | `uuidv7()` only from PG18; 16 has none | none | ~15 lines |
| v5 name-based (SHA-1) | no function; **buildable** from `sha1(BLOB)` and string slicing (§3 U1, verified against Python) | `uuid_generate_v5` in the `uuid-ossp` extension only | no sha1 at all | `crypto/sha1`, ~10 lines |
| Parquet | `FIXED_LEN_BYTE_ARRAY(16)` + UUID logical type | n/a | n/a | arrow-go reads the logical type; ssql shows base64 (§1) |

Notes:

- DuckDB's `uuid()` is evaluated per row.
- DataFusion's `uuid()` is volatile per row.
- DataFusion re-evaluates a CTE each time it is read. The traverse work
  of 2026-11-02 met this as `row_number() OVER ()` numbering differently
  per read. A random UUID read twice downstream on DataFusion can
  therefore be two different UUIDs.

### IP addresses

| | DuckDB 1.5 (`inet` extension) | Postgres 16 | DataFusion 55 | Go stdlib |
|---|---|---|---|---|
| Type | `INET`: one type for address and prefix, v4 and v6 | `inet` (address with optional mask) and `cidr` (network; rejects host bits) | none | `net/netip.Addr` and `netip.Prefix`: comparable value types, no allocation; **already in the slim build's dependencies** |
| Order | v4 before v6, then numeric | the same | — | `Addr.Compare`: the same |
| Containment | `<<=`, `>>=` | `<<`, `<<=`, `>>`, `>>=`, `&&` | — | `Prefix.Contains`, `Prefix.Overlaps` |
| Functions | `host`, `family`, `netmask`, `network`, `broadcast`, `inet + int` | those plus `masklen`, `set_masklen`, `hostmask`, `inet_merge`, `inet_same_family`, `abbrev`, `-` between addresses | — | `Masked`, `Bits`, `Is4`, `Is4In6`, `Unmap`, `Next`, `Prev`, `IsPrivate`, `IsLoopback`… |
| Host bits | kept: `'10.1.2.0/16'::INET` stays as written | `inet` keeps them, `cidr` refuses them | — | `Prefix` keeps them; `Masked()` clears them |
| v4-mapped v6 | `::ffff:1.2.3.4` is family 6 | the same | — | `Is4In6`, `Unmap` |
| Zones (`fe80::1%eth0`) | no | no | — | `Addr` carries a zone |
| Parquet | the struct above: **a DOUBLE address, so a v6 address is rounded** (`2001:db8::1` stored as `-1.276e38`) | n/a | n/a | — |

Notes:

- DuckDB's `inet` is an extension. It autoloaded here because it was
  already installed (`~/.duckdb/extensions`); a machine that has never
  fetched it needs network access on first use.
- No engine's columnar file form keeps an address exactly, apart from a
  string. Interchange is text whatever ssql does inside.

## 3. UUIDs: the options

A UUID needs no type. The canonical form (lowercase hex, fixed hyphen
positions) is fixed-width, so string order **is** the 128-bit order;
DuckDB's `UUID` sorts the same way. Equality is string equality once the
spelling is canonical. A type would save space (16 bytes rather than 36)
and validate input, which `cast` can do on a string. The question is
only which functions to add.

### U1. `uuid5(namespace, name)`: deterministic, the same in every lane

- RFC 9562 §5.5: SHA-1 of the namespace's 16 bytes followed by the
  name's UTF-8 bytes, with version nibble 5 and variant `10`.
- The same input gives the same UUID in ssql, Python's `uuid.uuid5`,
  Go's `google/uuid`, Postgres's `uuid-ossp`, Java and .NET. That is the
  point: a stable key derived from data, which joins across systems and
  dedupes. It is also what an equivalence case can check against an
  independent oracle (Python's `uuid`).
- The namespace argument takes either a UUID, or the RFC names `dns`,
  `url`, `oid`, `x500`. `uuid5(name)` with one argument could default
  to a fixed ssql namespace. That is a decision, not a fact (§6 Q2).
- **Lanes:**
  - **exec / VM / typed:** `crypto/sha1`, about ten lines in `exprfn`.
  - **DuckDB:** built from `sha1(unhex(ns_hex) || encode(name))` by
    slicing the hex. The variant nibble maps through a 16-character
    lookup string. Measured against Python for `python.org`, `ssql` and
    `héllo`, all identical:
    ```sql
    substr(h,1,8)||'-'||substr(h,9,4)||'-5'||substr(h,14,3)||'-'||
    substr('89ab89ab89ab89ab', strpos('0123456789abcdef', substr(h,17,1)), 1)||
    substr(h,18,3)||'-'||substr(h,21,12)
    ```
    It is long, like `hash`'s SQL, and for the same reason.
  - **Postgres:** refuse, naming `uuid-ossp`. This matches `sha1()`
    there.
  - **DataFusion and rust:** refuse, because there is no sha1.

### U1′. A SHA-256 name-based UUID (version 8)

RFC 9562 Appendix B.2 gives this as its example of a v8 UUID.

- Every engine has sha256 (Postgres builtin on `bytea`, DataFusion
  `sha256`/`digest`), so it can translate in **all three** dialects,
  as `hash` does.
- It interoperates with nothing outside ssql, because nobody else
  computes it. v5's whole value is that Python and Postgres agree with
  it.
- Use it as a fallback only if all-engine parity matters more than
  interoperability. I would not ship both on day one.

### U2. `uuid()` (v4) and `uuid7()`: random, different every run

- This is what "give every row an id" usually means. Every engine has
  v4. v7 (a time-ordered prefix, so ids sort by creation and index
  well) is DuckDB-only today; Postgres 16 must refuse it, and so must
  DataFusion.
- It is the second nondeterministic function, after `now()`. The
  equivalence gate cannot compare values; it can compare **shape**:
  - the count of rows equals the count of distinct ids;
  - each id matches `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab]…$`;
  - for v7, the embedded millisecond lies within the run's start and
    end.

  That needs a per-case normaliser hook in the harness: replace the
  column with its shape check before comparing. `now()` has no
  equivalence case, so this would be the first.
- **The DataFusion hazard.** A random column referenced twice
  downstream (a self-join, `union` of a filtered copy) may be generated
  twice. That is the traverse `row_number() OVER ()` lesson again. The
  translator can materialise it (DataFusion has no `MATERIALIZED` hint
  on CTEs), or refuse when the column is read more than once.
- **Parallel Go lanes:** each shard's ids come from `crypto/rand`, so
  order does not matter. For v7, the timestamp prefix sorts across
  shards only to the millisecond, as in every engine. The RFC's
  monotonic counter is per generator, so it is not a cross-shard
  guarantee.

### U3. A seeded "random-looking" UUID built from `hash`

- `uuid4(n, seed)`, the same in every lane, made from several `hash`
  words.
- This is what `sample -seed` lacks in SQL (refused, DFC110).
- 122 random bits from 32-bit words means four `hash` calls, and the
  SQL is four times `hash`'s already long form.
- Only useful for fixtures. `uuid5(ns, string(n))` gives the same
  determinism with real interoperability. **Not recommended.**

### U4. A `uuid` type

As argued above: no. The only things a type buys are smaller storage
and Parquet `UUID` round-trips. The reader fix in §1 gets the read half
without a type. A writer could tag a column whose every value is a
canonical UUID string with the UUID logical type, but that is an option
for `to parquet`, not a type.

## 4. IP addresses: the options

### I0. Status quo, documented

- Strings, with a troubleshooting note: sort and compare are textual.
- Costs nothing; leaves `sort ip` wrong.

### I1. Functions over strings, no type

The column stays a string. Functions parse it on each call and return strings, ints or bools.
`netip.ParseAddr` measured 22 ns for a v4 address and 65 ns for a v6
one on this machine.

| Function | Result | Example |
|---|---|---|
| `ip(s)` | the canonical text, or an error | `ip("2001:DB8:0::1")` → `"2001:db8::1"` |
| `isip(s)` | bool, never an error | a `where` guard |
| `ipfamily(s)` | 4 or 6 | |
| `ipint(s)` | int64 for v4, int128 for v6 | the sort key: `update -set-expr k 'ipint(ip)' \| sort k` |
| `ipkey(s)` | one sortable key for mixed v4 and v6: family, then address, as a fixed-width hex string | `sort` on a mixed column |
| `ipfromint(n[, family])` | the text | |
| `ipnet(s, bits)` | the network's text, host bits cleared | `ipnet("10.1.2.3", 16)` → `"10.1.0.0/16"`; a `group-by` key |
| `ipin(s, prefix)` | bool: s is within prefix | `where -expr 'ipin(src, "10.0.0.0/8")'` |
| `ipbits(p)`, `iphost(p)`, `ipbroadcast(p)` | the parts of a prefix | |
| `ipprivate(s)`, `iploopback(s)` | bool (netip's classifications) | |

Lanes:

- **exec / typed:** `net/netip` wrappers in `exprfn`. They are already
  in the slim build's dependency set, so the wasm budget is unchanged.
- **DuckDB:** `CAST(x AS INET)` plus `host`/`network`/`<<=`. Two
  caveats:
  - DuckDB keeps host bits, so `ipnet` must wrap `network(...)` to
    clear them;
  - DuckDB has no `INET → integer` cast (measured: a binder error),
    so `ipint` is built from `split_part` arithmetic for v4, and v6
    needs care.
- **Postgres:** `::inet` with its operators; `ipint` as
  `x::inet - '0.0.0.0'::inet` for v4 (measured: bigint, 167772161 for
  10.0.0.1). Text must go through `host()`: `'2001:db8::1'::inet::text`
  is `2001:db8::1/128`, where DuckDB's cast to VARCHAR and netip both
  print `2001:db8::1`.
- **DataFusion / rust:** no inet. v4 can be lowered to
  `split_part`/`CAST` integer arithmetic (four octets). v6 parsing in
  SQL is not reasonable. Refuse with the reason, as `sha1` does, or
  translate v4 only and fail at run time on a v6 value. A refusal is
  the rule's spirit.

What I1 does **not** fix:

- `sort ip` and `where -if ip lt …` stay textual. The user has to know
  to sort on `ipint(ip)`.
- `min`/`max` and `top` likewise.
- That is the case for I2.

### I2. An `inet` type

`inet` is the wire type, after DuckDB's and Postgres's single type. Its
Go form is `netip.Prefix`, or `netip.Addr` with a separate prefix
type; one type is DuckDB's choice and the simpler one.

**What it buys.** It sorts, compares, groups and keys correctly
everywhere a value is compared: `sort`, `top`, `where -if … lt`,
`group-by` keys, `join` keys, `min`/`max`, `window` order, `distinct`.
Equality is on the parsed value, so `::1` and `0::1` join. A bad
address fails at `cast` or `-type ip inet`, with the row, rather than
being a string that never matches.

**What it costs.** DFC155's census for the next type, measured on two
real types:

- a `FieldType`, a wire name, `ParseFieldType`, `cast` arms, the
  `-type` flag on the readers, the `_schema` header, and the sidecar
  `ssqlType`;
- every switch with a stringifying `default`, where a new type silently
  disappears (§7.6 lists `applyValueToRecord`,
  `convertRecordValue(ForJSON)`, the generated `-set-expr` switch,
  `inferJSONType`, `setScalar`, `aggValue`);
- `isSimpleValue`, or a group key is dropped without a word;
- the comparison helpers; hash keys (by canonical text);
- the VM's comparison arms (expr-lang's `Less` panics on a struct);
- the transpiler lattice; typed struct fields (`netip.Prefix` is not
  `cmp.Ordered`, so `.Compare` is emitted inline, as int128 does);
- the Arrow and Parquet writers (as text: no engine keeps the type,
  §2);
- the SQL kind and casts per dialect (DataFusion: a string, with
  comparisons lowered to the v4 integer key or refused);
- the browser (explore and the playground show text, which is fine).

Unlike int128 and decimal it has **no arithmetic**: no overflow rules,
no widening, no sum. That removes the largest part of C and E. Both of
those took about two days each with gates; `inet` would be nearer one.

**Inference.** It should **not** be inferred. A column of `10.0.0.1` is
a string unless declared, which is decimal's rule for the same reason:
inference retypes columns and changes `sort` silently. Note the
asymmetry, though. A column that infers wrong today sorts wrong today,
so declaring it fixes it; nothing that works now stops working.

**Semantics to decide** (each a measured engine difference, §2):

- host bits: keep them, as DuckDB, Postgres `inet` and netip do;
- v4-mapped v6: family 6, equal to nothing in v4, as both engines do;
- zones: refused, because neither engine has them;
- one type, or address and prefix separately: one, as DuckDB does.

### I3. I1 now, I2 when it is wanted

The functions are needed with or without the type: `ipin`, `ipnet` and
`ipfamily` are operations, not comparisons. If I2 lands later, they
gain an `inet` overload and keep their string form. Nothing is thrown
away.

## 5. Recommendation

**UUIDs**

- Add `uuid5(ns, name)` first. It is deterministic, interoperable, and
  testable against Python as an independent oracle; it translates on
  DuckDB and is refused by name elsewhere.
- Then `uuid()` (v4) and `uuid7()`. They need the harness's
  shape-normaliser and a decision on the DataFusion
  read-twice hazard (§3 U2).
- No type.
- Fix the Parquet UUID reader regardless (§1).

**IP addresses: I3.**

- Ship the I1 functions on strings, with `ipint`/`ipkey` documented as
  the way to sort.
- Defer the `inet` type until a real dataset hurts. When one does, it
  is a one-day type, because it has no arithmetic.
- Refuse on DataFusion rather than translate v4 only.

**Either way, fix the Parquet struct bug first** (§1). It is silent data
loss on any nested Parquet file. It is unrelated to this proposal and
was found by it.

## 6. Questions for Ross

1. UUIDs: `uuid5` first, or the random `uuid()` first? Which do you
   actually want: an id for each row, or a key derived from the data?
2. Should `uuid5` with one argument use a fixed ssql namespace, or
   always require one, as Python does?
3. IP: is I3 (functions now, the type later) right, or do you want
   `sort ip` to be correct from day one, which is I2?
4. DataFusion: refuse the IP functions, or translate v4 and fail at run
   time on v6?
5. Fix the Parquet struct and UUID read bugs now, as a patch release?

## 8. Decision and build, 2026-11-04: uuid4, uuid5 and uuid7

Ross: "lets do uuid 4, 5 and 7". All three, at once, with no type
(§3 U4 stands).

- **Names.** `uuid4()`, `uuid5(ns, name)`, `uuid7()`: the version in
  the name, so a reader does not have to know that `uuid()` means
  random. There is no bare `uuid()`.
- **The namespace is required** (§6 Q2), as in Python: `dns`, `url`,
  `oid`, `x500` in either case, or a UUID with or without hyphens.
  Inventing an ssql namespace would make a key nobody else can
  reproduce, which is the opposite of what v5 is for.
- **One implementation**: `exprfn/uuid.go`. The VM binds it in
  `expr_uuid.go` (with the alias, so a column named `uuid4` does not
  shadow the call), and transpiled code calls it. `crypto/rand` for
  the random bits. A bad namespace is an error in the VM, a panic with
  the same error in generated code (as `exprfn.Add`), and a
  translation-time error when it is a literal.
- **uuid7** is the RFC's plain form: 48 bits of Unix milliseconds,
  then random. There is no per-millisecond counter: a counter is a
  shared state per generator, a lock on every row in the parallel
  lanes, and it would still not order across shards. Ids sort by
  time to the millisecond, as DuckDB's `uuidv7()` does.
- **SQL** (`generate_sql_uuid.go`):
  - `uuid4()`: `CAST(uuid() AS VARCHAR)` on DuckDB,
    `gen_random_uuid()::text` on Postgres, `uuid()` on DataFusion.
  - `uuid7()`: `CAST(uuidv7() AS VARCHAR)` on DuckDB; refused on
    Postgres (18 only) and DataFusion.
  - `uuid5()`: the §3 U1 sha1 form on DuckDB, a column namespace
    through a `CASE` that maps the four names; refused on Postgres
    (`uuid-ossp`) and DataFusion (no sha1).
- **The read-twice hazard** (§3 U2) did not appear. The `update`
  chain translates to nested subqueries, which every engine evaluates
  once. The two translations that read a CTE twice, fold and
  traverse, are DuckDB-only, or carry no input column through the
  second read. The equivalence case copies the id, filters on it and
  compares the copy, so an engine that evaluated the call per reading
  would fail it.
- **Testing a random function** needed no harness hook. The pipeline
  checks its own shape: a regex on the version and variant nibbles,
  `count-distinct` equal to the row count, the copy equal to the id,
  and for v7 the leading 48 bits a millisecond between 2022 and 2039.
  It reduces to one deterministic row, `{ok: true, n: 50, ids: 50}`,
  which is a Golden for every lane. uuid5 has a Golden from Python's
  `uuid.uuid5`.
- **Found on the way:** `+` on strings had no SQL translation. It was
  emitted as a numeric `+`, which DuckDB rejects (`'x' + CAST(n AS
  VARCHAR)`). Now a literal, a text column or a string-valued function
  on either side makes it `||` (`sqlStringOperand`), with an
  equivalence case of its own (`expr_string_concat`).

## 7. Related

- DFC155 §7.6 and §8.6: the census of places a new scalar type touches.
- DFC144: non-scalar values; the JSON type a Parquet struct reads into.
- DFC153: the six places a new expression function touches.
- DFC110: `sample -seed` refused in SQL; the precedent for refusing
  what an engine cannot make deterministic.
- DFC161 §11: DataFusion re-evaluating a CTE per read
  (`row_number() OVER ()`).
