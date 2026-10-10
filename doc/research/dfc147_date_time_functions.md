# Date and time: the function set, and where syntax earns its place

Reference: DFC147
Created: 2026-10-10
Last modified: 2026-10-10

[Back to Index](./README.md)

Status: **proposal, for comment.** Nothing built. The semantics in §5
were probed against DuckDB v1.5.0 on 2026-10-10; the Postgres and
DataFusion columns are from their documentation and are pinned by the
oracle lanes when built.

## 1. The question

Ross, 2026-10-10: the DuckDB comparison ([DFC136 §4.1](./dfc136_duckdb_feature_comparison_2026_09.md))
left date and time functions as the largest remaining gap. Should the
missing operations be expression functions, or should they come into
the command syntax as first-class members?

## 2. Where ssql stands

One type and five functions. A `time` column (from `cast -type F time`,
`from csv -type F time`, a sidecar, or a `_schema` header) is a Go
`time.Time`; `ParseTime` (`time_parse.go`) is the one reader of its
string forms. The expression language has `now()`, `date(v)` and
`date(str, layout[, zone])` with a Go layout, `duration(str)`,
`timezone(str)` and `bucket(ts, "5m")`. `bucket` is the only one with a
flag form (`update -set-bucket NAME SOURCE WIDTH`) and the only one with
a transpiler case and a SQL translation (`time_bucket` / `date_bin`
over a time column, epoch arithmetic over numbers). `resample` and the
window frames consume times.

Everything else is reached through Go methods on the value, and the
docs say so: `ts.Year()`, `ts.Hour()`, `ts.Weekday()`,
`ts.Sub(other).Hours()`, `ts.In(timezone("…"))`. Three things are wrong
with that as the public surface:

- **The methods exist in one lane.** The transpiler's type lattice is
  int, float, string, bool (`expr_go.go:36`); a `time.Time` field is
  refused ("has no native Go emission"), so any expression touching a
  time runs a typed program through the VM fallback. The SQL translator
  has no form for member access at all, so `ts.Year()` refuses in
  `generate sql`. One semantics, five backends, and four of them do not
  have it.
- **The parts are evaluated in the value's own offset.** Go's `Hour()`
  on a value parsed from `2026-01-05T10:30:00+11:00` is 10. DuckDB reads
  the same CSV cell as `TIMESTAMP WITH TIME ZONE` and gives `hour()` in
  the session zone, which defaults to the machine's (Sydney here: 10
  for that row, 21 for a `Z` row that Go reports as 10). Neither is
  wrong; they disagree, and the disagreement is invisible until a
  user compares.
- **Formatting is `string(ts)`**, which is RFC 3339 Nano, and nothing
  else. "Give me `2026-01-05` in the CSV" has no answer short of
  `ts.Format("2006-01-02")`, which turns the column into text.

The missing operations (DFC136 §4.1): strftime-style formatting,
calendar truncation (`bucket` is fixed-width only: there is no
"month"), timezone conversion, and calendar intervals (a month is not a
duration).

## 3. Functions, not syntax

**Functions are the foundation; a few flags sit on top; no new
operators.** The reasons:

1. **Every flag lowers to a function anyway.** The convergence rule from
   the flag-expression work (DFC121's `-set-bucket` desugars to
   `bucket(...)` at `update.go:1039`) means the function has to exist
   with its five emissions before any flag can. The function is the
   thing; the flag is a spelling with Tab completion.
2. **Functions translate one to one.** DuckDB, Postgres and DataFusion
   each have a date function family; `year(ts)` is `year(ts)`,
   `EXTRACT(year FROM ts)` and `date_part('year', ts)`. Member access
   (`ts.Year()`) is Go's spelling and translates to nothing.
3. **The grammar is full.** The dot became a path separator in DFC144
   (`addr.city`), and `-` on two times is already a duration. An
   `INTERVAL '1 month'` literal or a `ts + 1 month` form would need
   its own parse in the VM, the transpiler, the SQL translator and the
   `-explain` renderer, for a thing a function call says as clearly:
   `add(ts, 1, "month")`.
4. **The named-function set is small and closed.** A dozen functions
   cover what DuckDB's calendar offers that a user meets. Each is a
   table row (§4), not a design.

Where syntax does earn its place is where a command already owns the
operation and a flag can complete, optimise and translate without the
expression escape (§7). Three cases qualify; a fourth was considered
and dropped.

## 4. The function set

Every function takes a time as its first argument: a `time` column, or
anything `ParseTime` reads (RFC 3339 and the SQL datetime forms, a bare
date, Unix seconds as an int) — the rule `bucket` already follows, so an
untyped RFC 3339 string column works in the VM without a `cast`. The
typed lane transpiles natively only over a `time.Time` column (§8.2);
the SQL lane only over a column it knows is a time (§8.3). Nil in, nil
out. Calendar readings are in the local zone (`TZ`, then
`/etc/localtime`) unless a `tz()` says otherwise (§5.1).

### 4.1 Parts

| Function | Result | Go | DuckDB | Postgres | DataFusion |
|---|---|---|---|---|---|
| `year(ts)` | int | `Year()` | `year(ts)` | `EXTRACT(year FROM ts)::BIGINT` | `date_part('year', ts)` |
| `month(ts)` | int 1–12 | `Month()` | `month(ts)` | `EXTRACT(month …)` | `date_part('month', ts)` |
| `day(ts)` | int 1–31 | `Day()` | `day(ts)` | `EXTRACT(day …)` | `date_part('day', ts)` |
| `hour(ts)` | int 0–23 | `Hour()` | `hour(ts)` | `EXTRACT(hour …)` | `date_part('hour', ts)` |
| `minute(ts)` | int 0–59 | `Minute()` | `minute(ts)` | `EXTRACT(minute …)` | `date_part('minute', ts)` |
| `second(ts)` | int 0–59 | `Second()` | `second(ts)` | `FLOOR(EXTRACT(second …))` | `date_part('second', ts)` |
| `dow(ts)` | int, Sunday = 0 | `Weekday()` | `dayofweek(ts)` | `EXTRACT(dow …)` | `date_part('dow', ts)` |
| `doy(ts)` | int 1–366 | `YearDay()` | `dayofyear(ts)` | `EXTRACT(doy …)` | `date_part('doy', ts)` |
| `week(ts)` | int 1–53, ISO | `ISOWeek()` | `week(ts)` | `EXTRACT(week …)` | `date_part('week', ts)` |
| `quarter(ts)` | int 1–4 | `(Month()-1)/3+1` | `quarter(ts)` | `EXTRACT(quarter …)` | `date_part('quarter', ts)` |
| `epoch(ts)` | int, Unix seconds | `Unix()` | `epoch(ts)::BIGINT` | `EXTRACT(epoch …)::BIGINT` | `date_part('epoch', ts)` |

Sunday = 0 is Go's, DuckDB's and Postgres's convention, so no
translation step. `week` is the ISO week in all four (DuckDB's `week()`
of 2026-12-31 is 53, as Go's `ISOWeek()`); the ISO year that goes with
it is not offered (`year(trunc(ts, "week"))` is wrong at the boundary,
and nobody has asked). `epoch(ts)` is the inverse of `date(int)`; its
millisecond form is `epoch(ts) * 1000`, not another function.

### 4.2 Truncation

`trunc(ts, UNIT)` for `year`, `quarter`, `month`, `week`, `day`,
`hour`, `minute`, `second`. Result is a time. `week` truncates to
Monday (ISO), as DuckDB's and Postgres's `date_trunc('week')` do.

| Go | DuckDB | Postgres | DataFusion |
|---|---|---|---|
| per unit: `time.Date(y, m, 1, …)` etc.; week: back `(Weekday()+6)%7` days | `date_trunc('UNIT', ts)` | `date_trunc('UNIT', ts)` | `date_trunc('UNIT', ts)` |

`bucket` stays for fixed widths (`5m`, `90s`, `6h`) and keeps its
epoch-aligned semantics, zone-free; `trunc(ts, "day")` and
`bucket(ts, "24h")` agree only under `TZ=UTC`. The unit is a string literal in all lanes (as `bucket`'s
width is), so a bad unit is a compile error, not a per-row one.

### 4.3 Formatting and parsing

`format(ts, PATTERN)` returns a string. The pattern language is
**strftime**, because DuckDB, DataFusion (chrono) and Python read it
and users know it; Go layouts are the thing nobody outside Go knows.
Internally the pattern is converted once, at compile time, to a Go
layout where a directive has one (§6), so the VM and the typed lane
call `Format` and never interpret `%` per row.

`date(str, PATTERN[, zone])` gains the same language: a pattern
containing `%` is strptime; one without is a Go layout, as today, so no
expression that works stops working. Postgres's `to_char` has its own
pattern vocabulary; §6 gives the mapping. Directives with no
counterpart in a lane are refused in that lane at generation time, by
name.

### 4.4 Zone conversion

`tz(ts, ZONE)` is new (today the only form is the Go method
`ts.In(timezone("…"))`, VM-only). It returns the same instant with its
parts in `ZONE` (`time.In`). On its own it changes nothing a sink prints (RFC 3339 of
the same instant, different offset); it exists to compose:
`hour(tz(ts, "Australia/Sydney"))`, `trunc(tz(ts, "Europe/Zurich"),
"day")` (local midnight, as an instant), `format(tz(ts, "America/New_York"),
"%H:%M")`. The zone is a literal.

| Go | DuckDB | Postgres | DataFusion |
|---|---|---|---|
| `ts.In(loc)` with `loc` hoisted | `(ts::TIMESTAMPTZ AT TIME ZONE 'ZONE')`, the session zone pinned by the prologue (§5.1) | `(ts::TIMESTAMPTZ AT TIME ZONE 'ZONE')`, same pin | `to_local_time(ts AT TIME ZONE 'ZONE')` if the oracle lane confirms, else refuse |

The existing `timezone(str)` (a `*time.Location` value) stays for
`ts.In(...)` compatibility and is dropped from the docs' front table.

### 4.5 Calendar arithmetic

`add(ts, N, UNIT)` returns a time; `diff(a, b, UNIT)` returns an int.
Units as for `trunc`. Fixed durations keep working with `+`/`-` and
`duration()`; these two exist for the units a duration cannot say.

Decisions a probe forced (§5.3, §5.4): `add` clamps to the month's
end (Jan 31 + 1 month = Feb 28, as DuckDB and Postgres; Go's `AddDate`
would say Mar 3); `diff` counts **unit boundaries crossed**, which is
DuckDB's `date_diff` (Jan 31 → Feb 1 is one month; 00:59 → 01:00 is one
hour), not elapsed whole units.

| | Go | DuckDB | Postgres | DataFusion |
|---|---|---|---|---|
| `add` | `exprfn.AddCalendar(ts, n, unit)` (clamping) | `ts + INTERVAL (n) UNIT` | `ts + (n \|\| ' UNIT')::INTERVAL` | `ts + INTERVAL 'n UNIT'` (literal n only) |
| `diff` | `exprfn.DiffCalendar(a, b, unit)` (boundaries) | `date_diff('UNIT', a, b)` | per unit: `(b::date - a::date)` for day; `(EXTRACT(year FROM b)*12+EXTRACT(month FROM b)) - (…a)` for month; `FLOOR(EXTRACT(epoch FROM date_trunc('UNIT', b) - date_trunc('UNIT', a)) / UNIT_SECONDS)` for hour, minute, second; year/quarter/week by the same truncation | refuse (no `date_diff`; subtraction gives an interval) |

### 4.6 Not in the set

- A duration-valued `age(a, b)` or `a - b` as an interval type:
  `a - b` is already a Go duration in the VM; `diff` covers the
  calendar case.
- `today()`, `yesterday()`: `trunc(now(), "day")`.
- Locale-dependent names: `%a %A %b %B` are English in every lane.
- A `date` type distinct from `time`, or a time-of-day type.
- ISO year, `last_day`, `make_date`, `strftime` on a duration.

## 5. Semantics decisions

These are the places where lanes disagree today or would without a
rule. Each one is a Golden equivalence case when built.

### 5.1 Calendar readings are local, honouring `TZ`; `tz()` overrides

Decided with Ross 2026-10-10 (the first draft said UTC-only; §10.1 has
the reversal). A `time` is an instant. Reading its calendar — a part,
`trunc`, `format`, and `hour(now())` — needs a zone, and the zone is the
**local** one: the `TZ` environment variable, then `/etc/localtime`,
exactly as `date(1)`, Go's `time.Local` and DuckDB resolve it (probed:
DuckDB's session `TimeZone` is `UTC` under `TZ=UTC` and `Australia/Sydney`
under `TZ=Australia/Sydney`). `tz(ts, ZONE)` picks a zone for one
expression. `TZ=UTC ssql …` pins a run.

Why not UTC-only, which is deterministic without the environment:
`hour(now())` at two in the afternoon in Sydney would say 3, and every
expression would grow a `tz()` wrapper, which is the hidden state moved
into the pipeline text a hundred times over. Why not the value's own
offset, which is what Go's `ts.Hour()` does today: SQL cannot see it
(a `TIMESTAMPTZ` is an instant; the `+11:00` the cell carried is gone
once read), so no SQL lane could agree.

Where the zone lives — §5.7 has the full argument — is the stream's
`_schema` header: the source command resolves it once (its `-tz`, else
`TZ`, else `/etc/localtime`) and writes it beside `fields`; every later
command reads the header and never the environment. Per lane:

- **exec and the VM:** the source stamps `"tz"` into the header; a
  downstream command's expression environment loads that location once
  per process and evaluates parts on `ts.In(loc)`. A stream whose
  header has no `tz` (older `tee` output, a plain JSONL file through
  `from jsonl`) resolves as a source would.
- **generated Go:** the planner reads the zone from the `from` stage
  (its `-tz`, else resolved at generation time) and emits one
  package-level `time.LoadLocation` literal; the program does not read
  `TZ` at run time, so it is reproducible like the SQL.
- **generated SQL:** the prologue pins the zone **by name in the text**
  — `SET TimeZone = 'Australia/Sydney'` (DuckDB), `SET timezone TO
  '…'` (Postgres), `datafusion.execution.time_zone` (DataFusion) —
  from the same source. Resolution is `TZ`, else the `/etc/localtime`
  symlink's path after `zoneinfo/`. When no name resolves (a copied
  rather than linked `/etc/localtime`, a `TZ` such as `EST5EDT`), the
  source refuses with "set -tz or TZ to an IANA zone name" rather than
  guessing. The generated SQL and Go therefore give the same answer
  wherever they run, and say which zone they assumed.
- **`generate json` and `generate ssql`** write the resolved `-tz` onto
  the `from` stage, so a serialised pipeline is portable.
- **`from ssh`** ships `-tz` in the remote command text, so a UTC server
  never leaks its zone into a Sydney pipeline.
- **`-explain`** prints the zone a pipeline resolved to.
- **the equivalence harness** sets `TZ=UTC` for every lane and runs
  `tz_hour` a second time under `TZ=Australia/Sydney`, so both the
  honouring and the pins are proven; a third run gives `-tz` on `from`
  under a different `TZ` and expects the flag to win in every lane.

Consequence to document: the same pipeline on a laptop in Sydney and a
server in UTC gives different `hour()` values, as `date` does; the fix
is the same, set `TZ`. `hour(date("2026-01-05T10:30:00+11:00"))` is 10
in Sydney and 23 under `TZ=UTC`, and `from … -tz Australia/Sydney`
says 10 anywhere. `ts.Hour()` keeps Go's own-offset
answer in the VM for anyone who used it, but the docs stop showing it.
`bucket` and `resample` stay zone-free (epoch-aligned), as today.

### 5.2 Week starts Monday

ISO everywhere: `trunc(ts, "week")` is the preceding Monday, `week(ts)`
the ISO week number. Matches DuckDB and Postgres `date_trunc('week')`
and `week()`/`EXTRACT(week)`. `dow(ts)` stays Sunday = 0 because all
three engines and Go agree on it; the two conventions coexist in SQL
too.

### 5.3 Adding months clamps

`add(date("2026-01-31"), 1, "month")` is 2026-02-28 (DuckDB, Postgres,
probed), `add(date("2024-02-29"), 1, "year")` is 2025-02-28. Go's
`AddDate` normalises overflow instead (Mar 3), so the Go lanes get a
clamping helper in `exprfn`. The differential corpus includes the five
month-end days and a leap day.

### 5.4 Differences count boundaries crossed

`diff(a, b, "month")` is `(year(b)*12 + month(b)) - (year(a)*12 +
month(a))`: Jan 31 → Feb 1 is 1, Jan 1 → Jan 31 is 0. Same rule for
every unit (`diff(a, b, "hour")` of 00:59 → 01:00 is 1), which is
DuckDB's `date_diff` and the SQL-side emulation for Postgres above.
`week` is `diff(…, "day") / 7` truncated, as DuckDB does (Sunday →
Monday is 0 weeks, probed). Elapsed whole units, when wanted, are
`int((b - a) / duration("1h"))`, already available. Negative when `b`
precedes `a`.

### 5.5 Strings are accepted in the VM, not in the typed lane

A `where -if-expr 'year(created) == 2026'` over an untyped CSV column
must work in exec, as `bucket` does, because most CSVs are not typed
and the first question is always this one. In a typed program the
column is a `string`; the transpiler refuses (`year() over a string
column: cast -type created time, or the VM runs it`) and the planner
takes the record fallback, exactly today's path for `bucket` on a
string. In SQL the same column is `VARCHAR`; the translator refuses
unless `sqlTimeColumns`/`sqlColumnKinds` says time. The remedy in
every message is the same: `cast -type F time` (or `-type F time` on
the reader, or a sidecar), after which all five lanes are native.

### 5.6 Result types

Parts, `epoch` and `diff` are `int64`. `trunc`, `tz`, `add` are
`time.Time` (wire `time`; a `-set-expr` result of this type stores as
`time`, which `applyValueToRecord` already handles for `bucket` over a
time). `format` is `string`. In a typed program a time result needs
`exprvm.MustCoerceTime` (new) so a `-set-expr` into an existing time
column stays native; a new column of type time is synthesised as
`time.Time` by the `-set-expr` schema op as `bucket` does now.

### 5.7 Where the zone lives: chosen at the source, carried in the stream

Ross, 2026-10-10: what about the environments that have no `bash` —
the WASM playground, `serve`, a JSON pipeline document? Should every
command take a `-tz`, or a generic `-env NAME VALUE` for any variable
we add?

The tension is between the Unix convention (§5.1: the environment
decides) and the authority principle (DFC115: any state that changes
what the user sees must be expressible as pipeline text; a bare `TZ`
is exactly the hidden state it exists to stop). One rule satisfies
both:

> **Consult the environment once, at the source, and carry the answer
> in the stream as text.**

- **`-tz ZONE` on the source commands only** — `from` (every form),
  `from ssh`, catalog sources. Default `TZ`, then `/etc/localtime`. No
  other command needs the flag: only the stream's origin resolves
  anything. (If a downstream re-stamp ever matters, `-tz` on a command
  that rewrites the header is a small later addition, not a design
  now.)
- **The zone travels in the `_schema` header**, as a `"tz"` key beside
  `"fields"`. `lib.ParseSchemaHeader` reads only the keys it knows and
  tolerates siblings (checked 2026-10-10), so an older binary
  downstream ignores it. `tee` preserves it for free; `tz()` stays the
  per-expression override; `now()` reads the header zone like
  everything else, so a pipeline's clock and its calendar agree.
- **Every serialisation freezes it into text**: `generate sql` the
  `SET` by name, `generate go` a `LoadLocation` literal, `generate
  json`/`generate ssql` a `-tz` on the `from` stage, `from ssh` a `-tz`
  in the remote command. The environment is read in one process, once.
- **Two streams, two zones** (`join`, `union`, `merge` of inputs whose
  headers name different zones): refuse with the hint to put `-tz` on
  one source, rather than pick silently.

Each environment Ross named then has one answer:

| Environment | Where the zone comes from |
|---|---|
| shell | `-tz`, else `TZ`, else `/etc/localtime`, at `from` |
| WASM playground, `to explore -wasm` | no environment; the shim passes the browser's zone (`Intl.DateTimeFormat().resolvedOptions().timeZone`) as `from`'s default; the pipeline text shows `-tz` when the user picks one |
| `serve` | the source runs on the server; the console is an editor of pipeline text (DFC115 matured), so it writes `-tz` onto the `from` stage rather than sending a header or a cookie; the server's environment is only the fallback |
| JSON pipeline document (`ssql run`, DFC134) | portable when its `from` stage states `-tz`; `generate json` materialises the resolved zone so a document written on one machine runs the same on another |
| `from ssh`, catalog shards | the local `from` resolves; the remote command text carries `-tz` |

**Not a generic `-env NAME VALUE`.** It would reimplement the shell
inside every command and make hidden state an unbounded feature. The
inventory does not need it either: ssql reads three variables.
`SSQL_MODE` changes what runs, not results, is observable (the output
is fragments, not data), already has its text forms (`-generate`,
`generate -pipeline … -mode`, DFC139) and is legitimately
inter-process plumbing — the thing an environment is for.
`SSQL_MODULE_DIR` is a developer build knob like `GOFLAGS`.
`SSQL_SCHEMA_SAMPLE` changes the inferred header, so it changes
results, and is the one that fails the rule; it should gain a flag on
`from` with the variable as default. The rule for the next variable
anyone is tempted to add:

> An environment variable that changes a pipeline's **results** must
> have a flag form and travel in the stream. One that changes what
> runs, or how fast, may stay in the environment.

The audit (that flag, deprecating `-generate` and the `SSQLGO` alias at
v5) is in TODO.md under "Environment variables"; Ross: a later cleanup.

## 6. The strftime subset

Supported directives, their Go layout, and the Postgres `to_char`
pattern. The compile-time converter refuses anything else by name
("`%U` has no equivalent; supported: …") in every lane, so a pattern
that works in exec works in SQL.

| Directive | Meaning | Go | Postgres |
|---|---|---|---|
| `%Y` | year, 4 digits | `2006` | `YYYY` |
| `%y` | year, 2 digits | `06` | `YY` |
| `%m` | month 01–12 | `01` | `MM` |
| `%-m` | month 1–12 | `1` | `FMMM` |
| `%d` | day 01–31 | `02` | `DD` |
| `%-d` | day 1–31 | `2` | `FMDD` |
| `%H` | hour 00–23 | `15` | `HH24` |
| `%I` | hour 01–12 | `03` | `HH12` |
| `%M` | minute | `04` | `MI` |
| `%S` | second | `05` | `SS` |
| `%f` | microseconds, 6 digits | `.000000` (then strip the dot) | `US` |
| `%p` | AM/PM | `PM` | `AM` |
| `%b` | Jan | `Jan` | `Mon` |
| `%B` | January | `January` | `FMMonth` |
| `%a` | Mon | `Mon` | `Dy` |
| `%A` | Monday | `Monday` | `FMDay` |
| `%j` | day of year 001–366 | computed | `DDD` |
| `%z` | `+1100` | `-0700` | `OF` (gives `+11`; refuse) |
| `%Z` | zone name | `MST` | `TZ` |
| `%%` | literal `%` | `%` | `%` |
| `%G`, `%V`, `%u` | ISO year, week, weekday | computed | `IYYY`, `IW`, `ID` |

`%j`, `%G`, `%V`, `%u` have no Go layout token; the converter marks the
pattern as needing the slow path (a per-row `strings.Builder` over the
parsed directives) rather than a layout, in both the VM and generated
code. Refused everywhere: `%U`, `%W`, `%w`, `%e`, `%c`, `%x`, `%X`,
`%n`, `%t`, `%s` (DuckDB refuses `%w` and `%e` too, probed). Postgres
`to_char` quotes literal text differently (`"…"`), so the converter
emits literal runs quoted and refuses a pattern containing `"`.

For `date(str, PATTERN)` the same table runs the other way, producing
a Go layout for `time.Parse`; the slow-path directives are refused on
the parse side (no `%j` parsing), as is `%f` beyond what Go reads
(fractional seconds are accepted on any `%S`).

## 7. Where syntax earns its place

A flag is justified when (a) the operation is a daily pipeline step,
(b) a command already owns the operation, and (c) the flag can be
completed and translated without the expression escape. Three cases.

### 7.1 Calendar units on `-set-bucket`

`update -set-bucket month ts month` beside `-set-bucket minute ts 1m`.
The width parser already distinguishes a Go duration from anything
else; a `trunc` unit name desugars to `trunc(ts, "month")` the way a
duration desugars to `bucket`. Same completion (`month`, `week`, … are
a `StaticCompleter`), same SQL (`date_trunc`). `resample -every` stays
fixed-width: resampling onto a calendar grid is a different algorithm
(uneven bins) and is not asked for.

### 7.2 A `-format PATTERN` on the readers and `cast`

`from csv -type when time -format '%d/%m/%Y'` and `cast -type when
time -format '%d/%m/%Y'`, with the same strftime subset. Closes the
DFC146 follow-up: a sidecar's `format` (`dd/MM/yyyy` in XSD, `%d/%m/%Y`
in Frictionless) becomes honoured rather than refused, through one
XSD-to-strftime map (`yyyy`→`%Y`, `MM`→`%m`, `dd`→`%d`, `HH`→`%H`,
`mm`→`%M`, `ss`→`%S`). Implementation is a per-column layout in
`CSVConfig` (the `parseTimeCell` parser takes it), the typed sampler's
`time.Time` inference reading with it, and the SQL side passing
`dateformat`/`timestampformat` to DuckDB's `read_csv` (one format per
read; two columns with different formats refuse) and `to_timestamp(col,
'pattern')` for Postgres.

### 7.3 A `-time-format PATTERN` on the sinks

`to csv`, `to tsv`, `to table`, `to markdown`: render every time column
with the pattern instead of RFC 3339 Nano. The column's type does not
change (a `-sidecar` still says `datetime`), which is the reason this
is a sink flag and not an `update -set-expr`: formatting as a pipeline
stage turns the column into text and loses the type for everything
downstream. `to json` and `to jsonl` do not take it; their time form is
the wire form. Sinks have no SQL translation to add. `resample` already
has a `-time-format` with a Go layout; it accepts the strftime form
too (the `%` rule) and its help moves to the shared wording.

### 7.4 Considered, dropped

A `where -if ts after 2026-01-01` / `before` operator pair. `gt`/`lt`
already parse time literals against a time field (`helpers.go:496`),
so this is spelling only, and the comparison words would need SQL and
typed handling for no new capability. `where -if-expr 'ts >=
date("2026-01-01")'` and `-param since time 2026-01-01` cover the
expression side.

## 8. Lanes and gates

### 8.1 VM

`cmd/ssql/lib/runtime/env.go` registers the functions beside `bucket`
and `now`; the implementations live in the root package next to
`ParseTime` (`time_calendar.go`: `TruncTime`, `AddCalendar`,
`DiffCalendar`, `FormatStrftime`, `StrftimeLayout`) so the CLI, the VM
and generated code call one implementation. `ExprCompiledFunctions`
grows the names that could collide with field names (`day`, `month`,
`year`, `week` are plausible column names: compile-time binding as for
`date`, so `month(month)` works). The zone: `from` gains `-tz` (a
`StaticCompleter` over the IANA names from `$ZONEINFO`/`/usr/share/zoneinfo`),
`lib.Schema` a `TZ string` written and read with the header, and the
expression environment builders (`helpers.go`, `expr_agg.go`) take the
location from the stream's schema; `ssql.ResolveZone(flag string)
(name string, loc *time.Location, err error)` in the root package is
the one resolver (flag, `TZ`, `/etc/localtime` symlink, else error).

### 8.2 Transpiler

`exprGoTime` joins the lattice; a `time.Time` field emits natively
instead of refusing. Each function has a case in `exprToGo`: parts to
`ts.In(ssqlZone).Year()` etc. over the package-level location the
planner emits from the `from` stage's zone (§5.7), `trunc`/`add`/`diff`/`format` to `exprfn`
helpers that call the root functions, `tz` to `.In(loc)` with the
location hoisted as a package-level `var` (`time.LoadLocation` once,
panic at init on a bad zone, which codegen already validated). A time
compared with `<`/`>` emits `Before`/`After`; `==` emits `Equal`;
`-` on two times is a duration (int64 nanoseconds, as the VM). `now()`
transpiles (`time.Now()`). Every case lands in `TestExprToGo` and the
differential corpus (`expr_go_differential_test.go`) with a time fixture
including month ends, a leap day, DST transitions in two zones, and
values at `+11:00`, `Z` and `-05:00`.

### 8.3 SQL

`exprFuncs` cannot hold these (the argument shape differs per dialect),
so `generate_sql_expr.go` gets a `timeFuncToSQL(name, args)` switch
beside `bucketToSQL`, dispatching on `sqlDialectCur`, refusing with
`dialectRefuse` where §4 says refuse, and refusing a non-time argument
(`sqlColumnKinds`) with the `cast` remedy. The prologue gains the
session-zone `SET` from the `from` stage's resolved zone (§5.7); the
`-tz` arrives through the fragment's argv as `-type` does today. A `-set-expr` whose result is a time marks
the column in `sqlTimeColumns` so a later `bucket`/`trunc` chooses the
time form.

### 8.4 Gates

- `TestExprGoDifferential`: every function × the time fixture, VM vs
  transpiled, plus the string-argument refusals.
- `TestPipelineEquivalence` (duckdb lane on; Postgres and DataFusion
  lanes when the rigs are present): `time_parts_all_lanes` (`year`,
  `month`, `dow`, `week` over `from csv -type ts time`, grouped),
  `trunc_month_groupby`, `trunc_week_monday` (Golden, dates around a
  Sunday/Monday boundary), `add_month_clamps` (Golden: the five
  month-end rows), `diff_month_boundaries` (Golden), `format_strftime`
  (Golden), `tz_hour` (Golden: the `+11:00`/`Z` rows of §2 give 23 and
  10 under `TZ=UTC`, 10 and 21 under `TZ=Australia/Sydney`; run both
  ways), `from_tz_flag_wins` (`from … -tz Australia/Sydney` under
  `TZ=UTC`, every lane), `tee_carries_tz` (a `tee`'d stream read back
  on a `TZ=UTC` run keeps Sydney), `join_tz_mismatch_refused`,
  `set_bucket_month_flag`,
  `from_csv_format_dmy` (the §7.2 reader), `cast_format_dmy`,
  `to_csv_time_format` (exec/record/typed; the sink has no SQL lane).
- Watch-it-fail: before the prologue `SET`, generate the SQL under one
  `TZ` and run DuckDB under another and `tz_hour` follows the runner; before the clamping helper,
  `add_month_clamps` diverges in the Go lanes.
- `TestFieldCompletionConfiguration` for the new flags; `ssql functions
  date` lists the set; `make doc-check` and `make doc-test` over the
  codelab section.

### 8.5 Docs

`doc/EXPRESSIONS.md` Date Functions table replaces the Go-method
paragraph; `doc/cli-codelab.md` gains a "Dates" section (read a d/m/Y
file with `-format`, `trunc` to month, `group-by`, `format` on the way
out through `-time-format`); `doc/api-reference.md` for the root
functions; `generate sql` dialect table rows; DFC136 §4.1 and §6 marked
built; DFC146 §5 format refusal retired; `ssql conventions -category
data` (the time-zone rule, added 2026-10-10 ahead of the build);
`functions.go`
`writeDateFunctions`; CHANGELOG Added.

## 9. Build order

Three batches, each shippable:

- **A. Functions** (§4, §5, §8.1–8.4 for the functions): `from -tz`,
  the header's `tz`, `ResolveZone`; VM, transpiler with `exprGoTime`,
  SQL per dialect, prologue zone, differential and
  equivalence cases, docs. The bulk of the work and the whole of the
  DFC136 gap.
- **B. Patterns** (§4.3, §6, §7.2): the strftime converter, `format`,
  `date` with `%`, `-format` on readers and `cast`, the sidecar format
  map, DuckDB `read_csv` formats, Postgres `to_timestamp`.
- **C. Flags** (§7.1, §7.3): `-set-bucket` units, sink `-time-format`,
  `resample -time-format` accepting `%`.

A is a day of work with the gates; B and C half a day each. One release
(minor) at the end of A, or after all three if they land in one
sitting.

## 10. Open questions for Ross

1. ~~**UTC by default (§5.1).**~~ **Resolved 2026-10-10: local, honouring
   `TZ`.** Ross asked what to do with the `TZ` variable; the answer is
   the Unix one. The first draft preferred UTC-only for determinism;
   §5.1 records why that loses (`hour(now())`); §5.7 (same day) records
   where the zone lives — resolved once at the source, carried in the
   `_schema` header, frozen by name into every serialisation — which
   is what keeps the generated SQL and Go reproducible and answers the
   no-shell environments.
2. **Names.** `trunc`/`add`/`diff` are short; `date_trunc`/`date_add`/
   `date_diff` are what SQL users type. The expression language's
   existing style is short (`bucket`, `len`, `uniq`), and `trunc` is
   not otherwise taken (`floor`/`ceil`/`round` are the numeric ones).
   I lean short.
3. **`-format` on `cast`, or only on the readers.** A `cast` that
   parses `d/m/Y` text mid-pipeline is useful when the source is JSON
   or a `tee`; it costs little once the reader has it. Included.
