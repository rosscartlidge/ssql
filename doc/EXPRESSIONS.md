# Expression Language Reference

ssql supports powerful expression evaluation via the [expr-lang](https://expr-lang.org/) library, enabling computed fields, complex filters, and data transformations without writing Go code.

## Quick Start

**Commands with expression support:**
- `ssql update -set-expr <field> '<expression>'` - Set field to computed value
- `ssql where -if-expr '<boolean-expression>'` - Filter with complex conditions

**Quick examples:**
```bash
# Calculate derived fields
ssql from sales.csv | ssql update -set-expr total 'price * qty'

# Complex filtering
ssql from users.csv | ssql where -if-expr 'age >= 18 and status == "active"'

# Conditional logic
ssql from sales.csv | ssql update -set-expr tier 'revenue > 10000 ? "gold" : "silver"'

# String manipulation
ssql from data.csv | ssql update -set-expr email 'lower(trim(email))'
```

## Operators

### Arithmetic Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `+` | Addition | `price + tax` |
| `-` | Subtraction | `revenue - cost` |
| `*` | Multiplication | `price * qty` |
| `/` | Division | `total / count` |
| `%` | Modulo | `value % 10` |
| `**` | Power | `base ** exponent` |

**Integers are exact, and an overflow is an error.** An int64 `+`, `-`
or `*` whose result does not fit stops the pipeline with the operands in
the message (`integer overflow: 9223372036854775807 + 1 does not fit an
int64`), in every lane: exec, `generate go` and the SQL engines, which
raise the same way. It never wraps to a wrong number. Floats are exact
to 2^53 and carry on past it with rounding; `/` is always a float.

**Past int64 there is `int128`** (DuckDB's HUGEINT): a column is int128
because it was declared so — `cast -type COL int128`, a CSV or JSON
integer literal past int64 (which types the whole column), or an
integer sum — never because a value overflowed. In an expression,
`int128(x)` widens an integer, a whole float or a digit string:
`n * int128("100000000000000000000")`. That is also how a literal past
int64 is written, since the expression compiler refuses one. An int128
beside an int64 is an int128, beside a float a float; `+ - * %` are
exact to 2^127 and an int128 overflow is the same error (`… does not
fit an int128`); `/` and `**` are floats; comparisons between an int64
and an int128 are exact. `int(x)` of an int128 that does not fit an
int64 is an error, never a wrap. One gap: `x in [1, 2]` with an int128
`x` compares by Go equality, so a list of int literals does not match —
compare with `==`, or `int128()` the literals.

**Money is `decimal:S`** (DECIMAL(38,S) in every engine, Arrow and
Parquet): a fixed-point number with S digits after the point, exact to 38
digits, so a column of prices sums to the cent where a float says
`0.30000000000000004`. A column is decimal only because it was declared
so — `cast -type price decimal:2`, `-type price decimal:2` on the `from`
stage, a `_schema` header, a sidecar's `ssqlType`, a Parquet DECIMAL
column — never inferred from `12.34`, which stays a float. Reading text
into a declared scale rounds half away from zero (`12.345` → `12.35`,
`-2.5` → `-3` at scale 0), as DuckDB's `read_csv` does. In an
expression `decimal("0.10")` takes its scale from the text (the way to
write an exact literal), `decimal(x, 2)` brings any number to the scale.
The rules, DuckDB's: a decimal beside an integer of either width is a
decimal — `+ -` at the larger scale, `*` at the sum of the scales
(`price * rate` of `decimal:2` and `decimal:4` is `decimal:6`, exact;
`round(x, 2)` brings cents back), `%` at the larger — beside a float it
is a float; `/` and an average are floats; comparisons are exact across
scales (`1.25 == 1.250`); `round(d, n)` is a decimal of n places,
`floor`, `ceil` and `trunc` decimals of scale 0; `int(d)` truncates
toward zero as `int()` of a float does; `string(d)` keeps the scale's
digits (`"1.10"`); `-sum`, `window -sum`, `pivot -func sum` and `sum()`
of decimals are a decimal at the column's scale. A result past 38
digits, or a product whose scale would pass 38, is an error naming the
operands (`decimal overflow: … does not fit DECIMAL(38,2)`). A float
brought to a decimal rounds its shortest decimal form (`1.005` → `1.01`);
an engine casting a DOUBLE rounds the binary value its own way, so cast
from text where the digits matter. The `in` gap int128 has applies.

```bash
# exact to the cent: a declared decimal column, where the float column drifts
ssql from range 3 | ssql update -set-expr amt 'decimal("0.10")' | ssql group-by -sum amt total | ssql to table
```

**Every integer sum is an int128.** `group-by -sum` of an integer
column, `-avg` (a float of the exact sum), `window -sum`/`-avg`,
`pivot -func sum` and `sum()` over a list (`sum(xs)`, `sum(xs, # * 2)`,
and the `group-by -expr` aggregations built on it) accumulate exactly
in an int128 and emit one — DuckDB's `SUM(BIGINT) → HUGEINT` — so a sum
of int64 values never overflows. Unary minus is checked the same way.
The Fibonacci fold in int128 state is exact to fib(184), the largest
Fibonacci number in 128 bits:

```bash
ssql from range 183 | ssql window -stream-expr '{a:int128(0), b:int128(1)}' '{a:b, b:a+b}' 'b' fib
```

### Comparison Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `==` | Equal | `status == "active"` |
| `!=` | Not equal | `dept != "Sales"` |
| `<` | Less than | `age < 18` |
| `>` | Greater than | `salary > 50000` |
| `<=` | Less than or equal | `score <= 100` |
| `>=` | Greater than or equal | `age >= 21` |

### Logical Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `and` | Logical AND | `age >= 18 and status == "active"` |
| `or` | Logical OR | `dept == "Sales" or dept == "Marketing"` |
| `not` | Logical NOT | `not (email contains "@test.com")` |

### Special Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `? :` | Ternary conditional | `age >= 18 ? "adult" : "minor"` |
| `??` | Nil coalescing | `value ?? "default"` |
| `in` | Membership test | `status in ["active", "pending"]` |
| `contains` | Contains (string or array) | `email contains "@"` |
| `\|` | Pipe operator (chain functions) | `name \| trim \| upper` |

## Built-in Functions

### String Functions

| Function | Description | Example |
|----------|-------------|---------|
| `upper(str)` | Convert to uppercase | `upper("hello")` → `"HELLO"` |
| `lower(str)` | Convert to lowercase | `lower("WORLD")` → `"world"` |
| `trim(str)` | Remove leading/trailing whitespace | `trim("  text  ")` → `"text"` |
| `trimPrefix(str, prefix)` | Remove prefix from string | `trimPrefix("hello world", "hello ")` → `"world"` |
| `trimSuffix(str, suffix)` | Remove suffix from string | `trimSuffix("file.csv", ".csv")` → `"file"` |
| `split(str, sep)` | Split string into array | `split("a,b,c", ",")` → `["a", "b", "c"]` |
| `splitAfter(str, sep)` | Split, keeping separator attached | `splitAfter("a,b,c", ",")` → `["a,", "b,", "c"]` |
| `join(arr, sep)` | Join array into string | `join(["a", "b"], ",")` → `"a,b"` |
| `replace(str, old, new)` | Replace all occurrences | `replace("hello world", "world", "there")` → `"hello there"` |
| `replaceRegex(str, pat, repl)` | Regex replace with capture groups | `replaceRegex("abc 123", "[^0-9]", "")` → `"123"` |
| `repeat(str, n)` | Repeat string n times | `repeat("ab", 3)` → `"ababab"` |
| `indexOf(str, sub)` | First index of substring (-1 if missing) | `indexOf("hello", "ll")` → `2` |
| `lastIndexOf(str, sub)` | Last index of substring (-1 if missing) | `lastIndexOf("hello", "l")` → `3` |
| `hasPrefix(str, prefix)` | Check if starts with prefix | `hasPrefix("hello", "he")` → `true` |
| `hasSuffix(str, suffix)` | Check if ends with suffix | `hasSuffix("world", "ld")` → `true` |
| `str contains substr` | Check if contains substring (operator only) | `"hello" contains "ll"` → `true` |

**Note:** `contains`, `startsWith`, `endsWith` are infix **operators**, not functions — `contains(email, "@")` is a parse error; write `email contains "@"`.

**Examples:**
```bash
# Normalize email addresses
ssql update -set-expr email 'lower(trim(email))'

# Extract domain from email
ssql update -set-expr domain 'split(email, "@")[1]'

# Remove file extension
ssql update -set-expr base 'trimSuffix(filename, ".csv")'

# Regex replacement (use \\ for regex backslashes)
ssql update -set-expr clean 'replaceRegex(name, "[^a-zA-Z]", "_")'
ssql update -set-expr digits 'replaceRegex(code, "[^0-9]", "")'

# Capture group expansion ($1, $2, ${name})
ssql update -set-expr fmt_phone 'replaceRegex(phone, "(\\d{3})(\\d{3})(\\d{4})", "($1) $2-$3")'
```

### Math Functions

| Function | Description | Example |
|----------|-------------|---------|
| `round(num)` | Round to nearest integer, half away from zero | `round(2.5)` → `3`, `round(-2.5)` → `-3` |
| `round(num, places)` | Round at `places` decimals | `round(3.14159, 2)` → `3.14` |
| `floor(num)` | Round down | `floor(3.7)` → `3` |
| `ceil(num)` | Round up | `ceil(3.2)` → `4` |
| `trunc(num)` | Drop the fraction, toward zero | `trunc(-3.7)` → `-3` |
| `abs(num)` | Absolute value | `abs(-5)` → `5` |
| `sign(num)` | -1, 0 or 1 | `sign(-2.5)` → `-1` |
| `min(a, b)` | Minimum of two values | `min(10, 20)` → `10` |
| `max(a, b)` | Maximum of two values | `max(10, 20)` → `20` |
| `sqrt(x)`, `cbrt(x)` | Square root, cube root | `sqrt(16)` → `4` |
| `exp(x)` | e to the power x | `exp(1)` → `2.718281828459045` |
| `ln(x)` | Natural logarithm | `ln(exp(2))` → `2` |
| `log10(x)`, `log2(x)` | Logarithm base 10, base 2 | `log10(1000)` → `3` |
| `log(b, x)` | Logarithm of x in base b | `log(3, 81)` → `4` |
| `pow(x, y)` | x to the power y (also `x ** y`) | `pow(2, 10)` → `1024` |
| `pi()` | π | `pi()` → `3.141592653589793` |
| `sin(x)`, `cos(x)`, `tan(x)` | Trigonometry, x in radians | `sin(pi() / 2)` → `1` |
| `asin(x)`, `acos(x)`, `atan(x)` | Inverse trigonometry, in radians | `acos(1)` → `0` |
| `atan2(y, x)` | Angle of the point (x, y) | `atan2(1, 1)` → `0.7853981633974483` |
| `sinh cosh tanh asinh acosh atanh` | Hyperbolic functions and inverses | `tanh(0)` → `0` |
| `degrees(x)`, `radians(x)` | Between radians and degrees | `degrees(pi())` → `180` |
| `isnan(x)`, `isinf(x)`, `isfinite(x)` | Tests on a number, inside an expression | `isfinite(1 / 0.0)` → `false` |
| `gcd(a, b)`, `lcm(a, b)` | Greatest common divisor, least common multiple | `gcd(12, 18)` → `6` |
| `factorial(n)` | n!, exact to 20! | `factorial(5)` → `120` |
| `gamma(x)`, `lgamma(x)` | The gamma function, and the log of its absolute value | `gamma(5)` → `24` |

The names are DuckDB's, so a pipeline reads the same in `generate sql`,
with one deliberate gap: there is **no one-argument `log`**, because it
is base 10 in DuckDB and Postgres and natural in Go and Python. Say
`ln(x)`, `log10(x)`, `log2(x)`, or `log(b, x)`; `log(x)` alone says so.
Results are floats (`sqrt(4)` is `4.0`, written `4`); `sign`, `gcd`,
`lcm` and `factorial` are integers; an integer argument is promoted.

**A result that is not a number is absent.** The square root of a
negative, `ln(0)`, `acos(2)`, `exp(1000)` or a division by zero gives
NaN or an infinity, and the record has no value for that: the field is
absent on that row, in exec, in generated Go and in `generate sql`
(where DuckDB and Postgres would otherwise raise an error; the
translation guards them). `isnan`/`isinf`/`isfinite` see the value only
inside the same expression (`isfinite(ln(x))`).

**Examples:**
```bash
# Calculate discounted price (rounded)
ssql update -set-expr final_price 'round(price * 0.85)'

# Ensure non-negative values
ssql update -set-expr balance 'max(0, amount - fees)'

# Calculate absolute difference
ssql update -set-expr diff 'abs(actual - expected)'
```

### Array Functions

**Access:**

| Function | Description | Example |
|----------|-------------|---------|
| `len(arr)` | Length of array/string | `len([1, 2, 3])` → `3` |
| `first(arr)` | First element | `first([10, 20, 30])` → `10` |
| `last(arr)` | Last element | `last([10, 20, 30])` → `30` |
| `get(v, key)` | Safe access (nil if missing) | `get({"a": 1}, "z")` → `nil` |

**Transform:**

| Function | Description | Example |
|----------|-------------|---------|
| `take(arr, n)` | First n elements | `take([1, 2, 3, 4], 2)` → `[1, 2]` |
| `reverse(arr)` | Reverse array | `reverse([1, 2, 3])` → `[3, 2, 1]` |
| `sort(arr)` | Sort ascending | `sort([3, 1, 2])` → `[1, 2, 3]` |
| `sortBy(arr, pred)` | Sort by predicate | `sortBy([{"n":3},{"n":1}], {.n})` |
| `uniq(arr)` | Remove duplicates | `uniq([1, 1, 2, 3])` → `[1, 2, 3]` |
| `flatten(arr)` | Flatten nested arrays | `flatten([[1,2],[3,4]])` → `[1,2,3,4]` |
| `concat(a, b)` | Concatenate arrays | `concat([1,2], [3,4])` → `[1,2,3,4]` |
| `map(arr, fn)` | Transform each element | `map([1, 2, 3], {# * 2})` → `[2, 4, 6]` |

**Filter & Search:**

| Function | Description | Example |
|----------|-------------|---------|
| `filter(arr, pred)` | Filter elements | `filter([1, 2, 3], {# > 1})` → `[2, 3]` |
| `find(arr, pred)` | First matching element | `find([1, 2, 3], {# > 1})` → `2` |
| `findIndex(arr, pred)` | Index of first match | `findIndex([1, 2, 3], {# > 1})` → `1` |
| `findLast(arr, pred)` | Last matching element | `findLast([1, 2, 3], {# < 3})` → `2` |
| `findLastIndex(arr, pred)` | Index of last match | `findLastIndex([1, 2, 3], {# < 3})` → `1` |
| `groupBy(arr, pred)` | Group by predicate result | `groupBy([1,2,3,4], {# % 2 == 0 ? "even" : "odd"})` |

**Test:**

| Function | Description | Example |
|----------|-------------|---------|
| `all(arr, pred)` | All elements match | `all([2, 4, 6], {# % 2 == 0})` → `true` |
| `any(arr, pred)` | At least one matches | `any([1, 2, 3], {# > 2})` → `true` |
| `one(arr, pred)` | Exactly one matches | `one([1, 2, 3], {# == 2})` → `true` |
| `none(arr, pred)` | No elements match | `none([1, 2, 3], {# > 5})` → `true` |

**Aggregate:**

| Function | Description | Example |
|----------|-------------|---------|
| `count(arr)` | Count of elements | `count([1, 2, 3])` → `3` |
| `sum(arr)` | Sum of elements | `sum([1, 2, 3])` → `6` |
| `mean(arr)` | Average of elements | `mean([1, 2, 3, 4, 5])` → `3` |
| `median(arr)` | Median of elements | `median([1, 2, 3, 4, 5])` → `3` |
| `reduce(arr, pred, init)` | Reduce to single value | `reduce([1,2,3,4], {# + #acc}, 0)` → `10` |

**Note:** In predicates, `#` represents the current element, `#acc` the accumulator in reduce.

**Examples:**
```bash
# Check if all scores are passing
ssql where -if-expr 'all(scores, {# >= 60})'

# Average score
ssql update -set-expr avg_score 'mean(scores)'

# Top 3 scores
ssql update -set-expr top3 'take(sort(scores), 3)'

# Count high-value items
ssql update -set-expr high_count 'count(filter(prices, {# > 100}))'

# Check no failures
ssql where -if-expr 'none(scores, {# < 60})'
```

### Nested values: lists and objects in a field

A field that holds a JSON array or object (a nested value, wire type
`json`: a `tags` list from a JSON file, the list `group-by -collect`
builds, an `addr` object) **is that list or object inside an
expression** — not its text. Every array and map function above applies
to it, `tags[0]`, `tags[-1]` and `addr.city` read into it, and `"go" in
tags` tests membership:

```bash
ssql from jsonl events.jsonl | ssql where -if-expr '"go" in tags'
ssql from jsonl events.jsonl | ssql update -set-expr n 'len(tags)' -set-expr city 'addr.city' -set-expr top 'sort(scores)[-1]'
ssql from csv orders.csv | ssql group-by customer -collect product products | ssql update -set-expr n 'len(products)'
```

A result that is a list or map (`split(csv, ",")`, `filter(tags, …)`,
`{a: 1}`) is stored as a nested value too, so the next stage can open it
again (`len(parts)`), or `explode` it into rows. Only the fields an
expression names are parsed, once per row; a field it does not mention
costs nothing. In `generate sql` these translate to DuckDB's
`struct_extract`, `list_extract`, `list_contains` and the `list_*`
functions; the other dialects refuse them by name. (Before 2026-10-08,
`len(tags)` on `["go","rust"]` was 13 — the text's length — and the
workaround was `fromJSON(string(tags))`, which is no longer needed.)

### Type & Encoding Functions

**Type Conversion:**

| Function | Description | Example |
|----------|-------------|---------|
| `int(value)` | Convert to integer | `int("123")` → `123` |
| `int128(value)` | Widen to a 128-bit integer (an integer, a whole float, a digit string past int64) | `int128("123456789012345678901234567890") * 2` → `246913578024691357802469135780` |
| `float(value)` | Convert to float | `float("3.14")` → `3.14` |
| `string(value)` | Convert to string | `string(123)` → `"123"` |
| `type(value)` | Get type name as string | `type(42)` → `"int"` |

**JSON:**

| Function | Description | Example |
|----------|-------------|---------|
| `toJSON(value)` | Serialize to JSON string | `toJSON({"a": 1})` → `'{"a":1}'` |
| `fromJSON(str)` | Parse JSON string to value | `fromJSON('{"a":1}')` → `{"a": 1}` |

**Base64:**

| Function | Description | Example |
|----------|-------------|---------|
| `toBase64(str)` | Encode to Base64 | `toBase64("hello")` → `"aGVsbG8="` |
| `fromBase64(str)` | Decode from Base64 | `fromBase64("aGVsbG8=")` → `"hello"` |

**Map Conversion:**

| Function | Description | Example |
|----------|-------------|---------|
| `toPairs(map)` | Map to key-value pairs | `toPairs({"a":1})` → `[["a", 1]]` |
| `fromPairs(arr)` | Key-value pairs to map | `fromPairs([["a",1]])` → `{"a": 1}` |

**Examples:**
```bash
# Parse string fields to numbers
ssql update -set-expr age_num 'int(age_str)'

# Format numbers as strings with calculations
ssql update -set-expr label 'string(round(value * 100)) + "%"'

# Encode/decode
ssql update -set-expr encoded 'toBase64(email)'
ssql update -set-expr payload 'toJSON({"name": name, "age": age})'
```

### Date Functions

A `time` field — from `cast -type F time`, `from csv -type F time`, a
schema sidecar, or a `_schema` header that says so — is an **instant**.
Reading its calendar needs a zone, and the zone is the pipeline's
(see "The pipeline's zone" below): `hour(ts)` is the hour on the wall
clock there. Every function below also accepts what `ParseTime` reads
(an RFC 3339 or `2026-01-15 10:30:00` string, a bare date, Unix
seconds), so an untyped CSV column works in exec; typed programs and
`generate sql` need the column to be a `time`.

| Function | Result | Description | Example |
|----------|--------|-------------|---------|
| `now()` | time | The current instant | `hour(now())` → the local hour |
| `date(v)` | time | Parse a time: RFC 3339, `2026-01-15 10:30:00`, `2026-01-15T10:30:00` (UTC), `2026-01-15 10:30:00+00`, a bare date (midnight UTC), Unix seconds — the same forms `cast -type F time` reads | `date("2026-01-15")` |
| `date(str, pattern[, zone])` | time | Parse with a pattern — strftime when it contains `%`, else a Go layout — optionally in a named zone | `date("15/01/2026", "%d/%m/%Y")` |
| `year(ts)`, `month(ts)`, `day(ts)`, `hour(ts)`, `minute(ts)`, `second(ts)` | int | Calendar parts in the pipeline zone | `hour(ts)` → `10` |
| `dow(ts)` | int | Day of week, Sunday = 0 | `dow(date("2026-01-05"))` → `1` |
| `doy(ts)` | int | Day of year, 1–366 | |
| `week(ts)` | int | ISO week number, 1–53 | `week(date("2026-12-31"))` → `53` |
| `quarter(ts)` | int | 1–4 | |
| `epoch(ts)` | int | Unix seconds (zone-free; the inverse of `date(int)`) | `epoch(ts) * 1000` for milliseconds |
| `date_trunc(ts, unit)` | time | The start of ts's year, quarter, month, week (Monday), day, hour, minute or second | `date_trunc(ts, "month")` |
| `date_add(ts, n, unit)` | time | ts plus n units; months and years clamp to the month's end (Jan 31 + 1 month is Feb 28), days and weeks keep the wall clock across a DST change, hours and smaller are absolute | `date_add(ts, -1, "month")` |
| `date_diff(unit, start, end)` | int | Unit **boundaries crossed** from start to end (Jan 31 → Feb 1 is 1 month; 00:59 → 01:00 is 1 hour), negative when end is first; weeks are days ÷ 7 | `date_diff("day", created, now())` |
| `tz(ts, zone)` | time | The same instant, read in another zone by the functions around it | `hour(tz(ts, "America/New_York"))` |
| `strftime(ts, pattern)` | string | Format on the pipeline zone's wall clock with strftime directives (`%Y %m %d %H %M %S %f %p %b %B %a %A %j %z %Z %G %V %u %%`, and `%-d`/`%-m` unpadded) — DuckDB's and Python's language | `strftime(ts, "%Y-%m-%d %H:%M")` → `2026-01-05 10:30` |
| `strptime(text, pattern)` | time | Parse text with a strftime pattern; text with no zone of its own is a wall clock in the pipeline zone (an ISO export through `date(v)` is UTC) | `strptime(when, "%d/%m/%Y")` |
| `bucket(ts, "5m")` | same as in | Snap down to an epoch-aligned bucket of a fixed width (int/float epochs with the unit read from magnitude, RFC 3339 strings, or a `time`); zone-free | `bucket(ts, "1m")` |
| `duration(str)` | duration | Parse a Go duration (ns, us, ms, s, m, h) | `ts + duration("1h30m")` |

The names and argument orders are DuckDB's (`date_trunc`, `date_add`,
`date_diff(part, start, end)`), so what you type here is what you would
type in SQL, and `generate sql` emits the engine's own functions for all
of them. A field may share a function's name: bare `day` is the field,
`day(ts)` is the function.

`date_trunc` is for calendar units and `bucket` for fixed widths
(`90s`, `6h`); they agree on `day`/`24h` only under `-tz UTC`. The flag
form `update -set-bucket minute ts 1m` takes either: a duration is
`bucket`, a unit name (`month`) is `date_trunc`. Prefer the flag when it
says what you mean: it completes and translates like the function.

**The pipeline's zone.** Chosen once, where the stream starts, and
carried in the `_schema` header: the source command takes `-tz ZONE`
(an IANA name, `Australia/Sydney`, `UTC`), else the `TZ` environment
variable, else `/etc/localtime` — as `date(1)` and DuckDB resolve it.
Every later command reads the header's zone, never its own
environment, so a pipeline has one zone end to end, `tee` keeps it, a
remote `from ssh` stage cannot leak its machine's zone in, and
everything that serialises a pipeline freezes the name into its text
(`generate sql` pins the session to UTC and names the zone in every
calendar call; `generate go` sets it before reading; `generate json`
writes `-tz` onto the from stage). `TZ=UTC ssql …` pins a run. A
pipeline's two inputs naming different zones is refused: put `-tz` on
one source.

```bash
ssql from data.csv -tz Australia/Sydney | ssql update -set-expr h 'hour(ts)'   # say it
TZ=UTC ssql from data.csv | …                                                 # pin a run
```

**Patterns on the flags.** The same strftime language serves the
readers and the sinks, so a date that is not RFC 3339 never needs an
expression: `from csv -format when '%d/%m/%Y'` reads a day-first column
as a time (`-type when time` is implied; a schema sidecar's date format
does the same), `cast -format when '%d/%m/%Y'` parses one mid-pipeline,
and `to csv|tsv|table|markdown -time-format '%Y-%m-%d'` renders every
time column on the pipeline zone's wall clock without changing its type
(a `-sidecar` still says datetime). A pattern without `%` is a Go layout.

Inside `group-by -expr` every field is the group's array, so the array
builtins aggregate: `first(name)`, `last(name)`, `median(salary)`,
`len(uniq(city))`, `join(sort(name), ", ")`. The common ones have flags
that run in every lane, including `generate sql`: `-first`, `-last`,
`-any`, `-count-distinct`, `-string-agg FIELD SEP NAME`, `-median`,
`-percentile FIELD P NAME`, `-stddev`, `-variance`, `-mode`, `-arg-max
FIELD BY NAME`, `-arg-min`, `-min`/`-max` (strings and times too). Prefer the flag when one exists: flags translate to SQL as well;
`-expr` does not.

A result keeps its own type: an integer result is an int column, exact
to 2^63 (`sum` of ints, `count()`, a `-stream-expr` fold whose state
stays int — `'{a:0, b:1}' '{a:b, b:a+b}' 'b'` over `from range 91` is
fib(92) exactly), a float result is float, a string a string.

The aggregates themselves — `sum`, `count`, `avg`, `min`, `max`,
`first`, `last` — take a per-row expression and aggregate it over the
group: `sum(price * qty)`, `max(len(tags))`. Aggregates do not nest, as
in SQL: inside an aggregate's argument the same names are expr's
ordinary list functions over the row's own values, so `sum(sum(scores))`
totals every row's list, `max(max(scores))` is the best single score and
`avg(sum(scores))` the mean row total. A closure's `#` is the closure's
wherever it appears (`len(filter(scores, sum(#) > 10))` counts the rows
whose scores total more than 10). A list field inside an aggregate is
the row's list, so `sum(scores)` adds lists and fails; outside one it is
the group's array of lists, so `len(flatten(scores))` counts every score.

In `generate sql`, `bucket` over a `time` column becomes the engine's own
bucketing pinned to the Unix epoch (`time_bucket`, or `date_bin` on
Postgres and DataFusion), over a numeric epoch a `CASE` that detects the
unit by magnitude and snaps with `%`; string timestamps have no SQL
translation. The same holds for `resample`: `generate sql` translates it
over numeric epochs and over a time column (through epoch
microseconds), and refuses string timestamps — put `cast -type ts time`
in front.

A `time` value is still a Go `time.Time` in expressions, so `ts <
date("2026-02-01")`, `ts - other` (a duration) and `ts + duration("1h")`
work. Go's methods (`ts.Hour()`, `ts.Weekday()`) also still work, but
they read the value's own offset as it was parsed, not the pipeline
zone, and exist only in the VM lane — use the functions.

**Examples:**
```bash
# Per-minute averages (flag form, or -set-expr minute 'bucket(ts, "1m")')
ssql update -set-bucket minute ts 1m | ssql group-by minute -avg temp avg_temp

# Monthly totals in Sydney's calendar, whatever machine runs it
ssql from sales.csv -type when time -tz Australia/Sydney | ssql update -set-bucket month when month | ssql group-by month -sum amount total

# Business hours, age in days
ssql where -if-expr 'hour(ts) >= 9 && hour(ts) < 17 && dow(ts) in [1,2,3,4,5]'
ssql update -set-expr age_days 'date_diff("day", created, now())'

# Filter by date
ssql where -if-expr 'date(created) > date("2026-01-01")'
```

### Map Functions

| Function | Description | Example |
|----------|-------------|---------|
| `keys(map)` | Get all keys | `keys({"a":1, "b":2})` → `["a", "b"]` |
| `values(map)` | Get all values | `values({"a":1, "b":2})` → `[1, 2]` |
| `get(v, key)` | Safe access (nil if missing) | `get({"a":1}, "z")` → `nil` |

### Bitwise Functions

| Function | Description | Example |
|----------|-------------|---------|
| `bitand(a, b)` | Bitwise AND | `bitand(0xFF, 0x0F)` → `15` |
| `bitor(a, b)` | Bitwise OR | `bitor(0xF0, 0x0F)` → `255` |
| `bitxor(a, b)` | Bitwise XOR | `bitxor(0xFF, 0x0F)` → `240` |
| `bitnand(a, b)` | Bitwise AND NOT | `bitnand(0xFF, 0x0F)` → `240` |
| `bitnot(a)` | Bitwise NOT | `bitnot(0)` → `-1` |
| `bitshl(a, n)` | Left shift | `bitshl(1, 4)` → `16` |
| `bitshr(a, n)` | Right shift | `bitshr(16, 4)` → `1` |
| `bitushr(a, n)` | Unsigned right shift | |

**Examples:**
```bash
# Check permission flags
ssql where -if-expr 'bitand(permissions, 4) != 0'

# Mask bits
ssql update -set-expr masked 'bitand(flags, 0x0F)'
```

### Hash Functions

| Function | Description | Example |
|----------|-------------|---------|
| `hash(n)` | Reproducible 32-bit mix of an integer, in `[0, 2^32)` | `hash(1)` → `3531918615` |
| `hash(n, seed)` | The same with a seed: an independent stream per seed | `hash(1, 7)` → `558192479` |
| `md5(str)` | MD5 digest, 32 hex characters | `md5("1")` → `"c4ca4238…"` |
| `sha1(str)` | SHA-1 digest, 40 hex characters | |
| `sha256(str)` | SHA-256 digest, 64 hex characters | |

`hash` is for spread, not security: the same value in every lane
(exec, `generate go`, `generate sql` on DuckDB, Postgres and
DataFusion) and on every machine, so a fixture, a demo or a benchmark
needs no file — `from range` makes the rows and `hash` makes them
look like data. It takes an integer (a float with no fraction is
accepted) and a column or literal seed; it is not a hash of a string
(the digests are). The definition is lowbias32 over the input's low
word xor high word, with `(fold(seed) + 1) * 0x27d4eb2f` xored in
first; `generate sql` renders it as `&`, `>>`, `*` and xor so no
engine's 64-bit arithmetic overflows, which makes the SQL long (the
argument is repeated once per step) — write it over a column. The
digests translate to each engine's own function; Postgres has `sha1`
only through pgcrypto, so `generate sql -dialect postgres` refuses it.

```bash
# A fixture with spread and no file: 10 groups, amounts in [0, 500), a sparse flag
ssql from range 100000 | ssql update -set-expr key 'hash(n) % 10' -set-expr amount 'hash(n, 1) % 500' -set-expr flag 'hash(n, 2) % 7 == 0' | ssql group-by key -sum amount total -count rows

# A stable id from a string
ssql update -set-expr id 'sha256(name + ":" + email)'
```

## Helper Functions (ssql-specific)

ssql provides additional helper functions for safe field access:

| Function | Description | Example |
|----------|-------------|---------|
| `has(field)` | Check if field exists | `has("email")` → `true/false` |
| `getOr(field, default)` | Get field value or default | `getOr("age", 0)` → field value or `0` |

**Why use these?**
- Prevents errors when fields are missing or sparse
- Enables expressions to work gracefully with incomplete data
- Provides sensible defaults for missing values

**Examples:**
```bash
# Only process records with email
ssql where -if-expr 'has("email") and email contains "@"'

# Use default values for missing fields
ssql update -set-expr total 'getOr("price", 0) * getOr("qty", 1)'

# Conditional based on optional field
ssql update -set-expr status 'has("verified") ? "active" : "pending"'
```

## Finding a Function

- `ssql functions` — the concise list by category; `ssql functions -category date`
  one category in detail; `ssql functions bucket` one function's entry.
- **Alt-h while typing an expression** — inside an `-if-expr` / `-set-expr` /
  `-expr` argument the popup shows the concise list; with the cursor on a
  function name or inside its parentheses it shows that function's entry.

## Field Names That Match a Function

Columns are often called `date`, `len`, `type`, `max`, `min`, `count` —
names that are also expr built-in functions. The rule is **field when
bare, function when called**:

```bash
ssql from orders.csv | ssql where -if-expr 'date >= "2026-02-01"'            # the field
ssql from orders.csv | ssql where -if-expr 'date(date) > date("2026-02-01")'  # date(...) is the function, its argument the field
ssql from orders.csv | ssql update -set-expr month 'date[0:7]'               # slice the field
ssql from items.csv  | ssql where -if-expr 'len > 2 && len(name) > 3'        # field bare, function called
```

This holds in every lane — the interpreter, generated Go (record and
typed) and `generate sql` — and in aggregation expressions, where
`group-by -expr 'max(date)' latest` orders the group's dates: `max` and
`min` in an aggregation accept numbers, strings and times, `first` and
`last` take the group's first or last value in arrival order, all four
accept an expression (`max(price * qty)`), and an aggregation's result
may be a number, a string, a bool or a time. A field that
does not exist in the record is still reported as an unknown field even
when it shares a function's name. The ssql helper names (`has`, `getOr`,
`bucket`, `sha256`, `sha1`, `md5`, `replaceRegex`) are the one
exception: a field with one of those names is shadowed by the helper.

## Parameters: Values That Are Never Expression Text

An expression is source text, so a value spliced into it is parsed as
code: `-if-expr "name == \"$USER\""` with `USER='x" || true || "'` matches
every row. That is SQL injection with another grammar. `-param NAME TYPE
VALUE` binds NAME as a **variable** of the clause's expressions instead;
the value is data, whatever it contains:

```bash
ssql from orders.csv | ssql where  -if-expr 'name == who'          -param who string "$USER"
ssql from orders.csv | ssql update -set-expr total 'price * rate'  -param rate float 1.1
ssql from orders.csv | ssql where  -if-expr 'ts >= since'          -param since time 2026-03-01
```

- **Typed, never inferred.** TYPE is one of `string`, `int`, `float`,
  `bool`, `time`; a VALUE that is not of TYPE is an error. `string 12` is
  the text `"12"`, `int 007` is 7.
- **Clause scope.** Every expression in the clause sees every `-param`
  written in it (`-if-expr` and `-set-expr` share them); another clause
  may bind the same name to another value:
  `where -if-expr 'n > lo' -param lo int 5 + -if-expr 'n > lo' -param lo int 50`.
- **Errors, not precedence.** A parameter named like a field of the
  input is an error (a new upstream column must not change what an
  expression means), and so is a parameter no expression in the clause
  uses. One exception: a **field** parameter in a **closed** expression,
  one whose every identifier is a declared parameter, may be named like
  the column it views (`-if-expr 'price < cost * 1.1' -param-field price
  float price -param-field cost float cost`): no column is in play, so
  nothing upstream can change the meaning. A macro body is always closed.
- **Every lane.** The interpreter binds the value; `generate go` lifts
  each parameter into a flag of the compiled program, `-param-NAME`, so
  the binary is a prepared statement you re-run with new values
  (`./report -param-since 2026-06-01`); `generate sql` renders a literal
  of the declared type.

- **A column as a parameter.** `-param-field NAME TYPE FIELD` binds NAME
  per row to FIELD's value viewed as TYPE (cast with `cast`'s strict
  rules; a value not of TYPE is an error; an absent FIELD gives the
  expression no value, so a condition is false and a set assigns
  nothing). The expression text stays fixed whether `rate` is a literal
  (`-param rate float 1.1`) or a column (`-param-field rate float
  discount`), and a column whose name is not an identifier is reachable:
  `-param-field n int 1st`. A field parameter is part of the program's
  shape, so it is not a flag of the generated binary.

Rule for programs that build pipelines: **values go in flag slots
(`-if FIELD OP VALUE`, `-param`), never into expression text; a slot's
kind is fixed by the flag, never by the value's spelling** (`@name` is
the text `@name`; two fields compare with `-if-field`).

## Macros: Named Expressions With Field Arguments

An expression you keep retyping can be a **macro**: a named body with
typed parameters, defined once in `~/.config/ssql/macros` (or the file
`$SSQL_MACROS` names), one per line:

```
# ~/.config/ssql/macros
add3(x int, y int, z int) = x + y + z
margin(price float, cost float) = (price - cost) / price
cheap(price float, cost float) = price < cost * 1.1
```

A call names the macro and one field per parameter, in order. The
macro name completes on Tab and every argument is a field slot (Ctrl-O
completes it), which is what a quoted expression can never offer:

```bash
ssql from sales.csv | ssql update -set-macro s3 add3 f1 f2 f3            # s3 = f1 + f2 + f3
ssql from sales.csv | ssql update -set-macro m margin price unit_cost   # m = (price - unit_cost) / price
ssql from sales.csv | ssql where  -if-macro cheap price cost            # keep rows where price < cost * 1.1
ssql from sales.csv | ssql where  +if-macro cheap price cost            # the others
ssql from sales.csv | ssql update -if-macro cheap price cost -set tag thin
```

- **A call is a `-set-expr` / `-if-expr` with `-param-field`s.** The
  command expands `-set-macro s3 add3 f1 f2 f3` to `-set-expr s3 'x + y
  + z' -param-field x int f1 -param-field y int f2 -param-field z int
  f3` before anything else reads it, so the interpreter, `generate go`,
  `generate sql` and the optimiser all see an ordinary expression. What
  you generate carries the expansion and does not depend on the file;
  `generate ssql -as-written` shows the call as you typed it.
- **Typed parameters, like `-param-field`.** `string int float bool
  time`; the field is viewed as the declared type (an int column serves
  a `float` parameter), a value not of the type is loud, an absent field
  gives no value. A body sees its parameters and nothing else: `ssql
  macro check` rejects a body that names anything else, an unused
  parameter, a name that is an expression function, and a duplicate.
- **Parameter names may match column names.** A macro body is a closed
  expression, so `margin(price float, cost float)` applied to columns
  `price` and `cost` is fine (the rule above). Two calls in one clause
  must not declare the same parameter name; put them in separate
  clauses (`+`) or rename a parameter.
- **A keyboard convenience.** The file is read where the command runs:
  a `run` document or a `serve` workspace that keeps `-set-macro` uses
  that machine's file, as a shell alias would. `ssql macro list` shows
  what is defined; `SSQL_MACROS=/dev/null` turns macros off.

## Folds: `-stream-expr INIT EVERY FINAL NAME`

A fold is an aggregation written as a recurrence: a state, a step, a
result. `group-by -stream-expr` and `window -stream-expr` take the same
three expressions:

- `INIT` makes the state, an object literal: `'{s: 0, n: 0}'`
- `EVERY` makes the *next* state from the current one and the row — an
  object with the same keys. The row's fields and the state's keys are
  both in scope; a field shadows a state key of the same name. The whole
  object is computed from the old state and then replaces it, so
  `'{a: b, b: a}'` swaps
- `FINAL` reads the result from the state (state keys only): `'s / n'`

```bash
# An average as a fold
ssql from employees.csv | ssql group-by dept -stream-expr '{s:0, n:0}' '{s: s + salary, n: n + 1}' 's / n' avg_salary
```

The result keeps `FINAL`'s own type: an int fold is an int column, exact
to 2^63; a float fold a float; a string a string.

**In SQL a fold is a recursive CTE** ([DFC156](research/dfc156_fold_sql_recursive_cte.md)):
`generate sql` aggregates each partition once into an ordered list of its
rows, anchors the recursion at `INIT`, applies `EVERY` to the state and
the next row at each step, and reads `FINAL` per row (`window`) or at the
last row (`group-by`). The order is the window's `-order`, a `sort`
before the `group-by`, or a `from range` column — SQL has no arrival
order, so a fold without one of those refuses. A fold over a bounded
frame (`-preceding N`) restarts per row and is not a recurrence: it
refuses too. DuckDB runs it today (an `int128` state is its HUGEINT);
Postgres and DataFusion refuse with their reasons. The engine pays
50–200 µs per step, so this serves correctness and short partitions — a
few hundred rows per partition is instant, twenty thousand is seconds;
a long recurrence belongs in ssql, which folds a million rows in a
second.

In `group-by` the fold runs over each group: one row per group. In
`window` it runs over each row's *frame*: one value per row — and the
default frame (every row up to the current one) makes it a **running**
fold whose state carries from row to row, so a recurrence is one window
stage:

```bash
# Fibonacci: the state is (a, b); every row steps it; the row's value is b
ssql from range 10 | ssql window -stream-expr '{a:0, b:1}' '{a:b, b:a+b}' 'b' fib | ssql to table
```

A bounded frame (`-preceding N`, `-following N`, a RANGE frame) restarts
the fold at the frame's first row, so `'{s:0}' '{s: s + x}' 's'` over
`-preceding 2` is a three-row moving sum. In `window` a `nil` FINAL is
the absent value — what a fold that carries the previous row has at the
first one: `'{prev: nil, cur: nil}' '{prev: cur, cur: price}' 'prev'` is
`-lag price 1`.

Every lane runs a fold: exec and `generate go` record mode share the
same compiled fold; typed mode lowers the state to a Go struct and the
step to one simultaneous assignment (a `-stream-expr` makes a group-by
serial — fold state does not merge across shards). `generate sql` has no
fold and refuses loudly, so prefer a flag when one exists (`-sum`,
`-avg`, `-lag`, …): flags translate to SQL.

## Common Patterns

### 1. Data Validation

**Check email format:**
```bash
ssql where -if-expr 'has("email") and email contains "@" and email contains "."'
```

**Validate required fields:**
```bash
ssql where -if-expr 'has("name") and has("email") and has("age")'
```

**Check value ranges:**
```bash
ssql where -if-expr 'age >= 0 and age <= 120 and salary >= 0'
```

### 2. Data Cleaning

**Normalize strings:**
```bash
ssql update -set-expr email 'lower(trim(email))'
ssql update -set-expr name 'trim(name)'
ssql update -set-expr code 'upper(trim(code))'
```

**Provide defaults for missing values:**
```bash
ssql update -set-expr status 'getOr("status", "pending")'
ssql update -set-expr qty 'getOr("qty", 0)'
```

**Remove invalid data:**
```bash
ssql where -if-expr 'getOr("price", 0) > 0 and getOr("qty", 0) > 0'
```

### 3. Calculations

**Simple arithmetic:**
```bash
ssql update -set-expr total 'price * qty'
ssql update -set-expr profit 'revenue - cost'
ssql update -set-expr avg 'total / count'
```

**Conditional calculations:**
```bash
ssql update -set-expr discount 'total > 1000 ? total * 0.1 : 0'
ssql update -set-expr shipping 'weight > 10 ? 15.00 : 5.00'
```

**Complex formulas:**
```bash
ssql update -set-expr final_price 'round((price * qty) * (1 - discount / 100))'
ssql update -set-expr bmi 'round(weight / (height ** 2))'
```

### 4. Complex Filters

**Multiple conditions (AND):**
```bash
ssql where -if-expr 'age >= 18 and age <= 65 and status == "active"'
```

**Multiple conditions (OR):**
```bash
ssql where -if-expr 'dept == "Sales" or dept == "Marketing" or dept == "Support"'
```

**Nested conditions:**
```bash
ssql where -if-expr '(age >= 18 and status == "active") or role == "admin"'
```

**Pattern matching:**
```bash
ssql where -if-expr 'email startsWith "admin@" or email endsWith "@company.com"'
```

### 5. String Manipulation

**Create composite fields:**
```bash
ssql update -set-expr full_name 'first + " " + last'
ssql update -set-expr address 'street + ", " + city + ", " + state'
```

**Extract parts:**
```bash
ssql update -set-expr domain 'split(email, "@")[1]'
ssql update -set-expr first_char 'upper(name)[0:1]'
```

**Format data:**
```bash
ssql update -set-expr display 'upper(first) + " " + upper(last[0:1]) + "."'
```

### 6. Categorization

**Age categories:**
```bash
ssql update -set-expr category 'age < 18 ? "minor" : (age < 65 ? "adult" : "senior")'
```

**Performance tiers:**
```bash
ssql update -set-expr tier 'score >= 90 ? "A" : (score >= 80 ? "B" : (score >= 70 ? "C" : "F"))'
```

**Revenue brackets:**
```bash
ssql update -set-expr level 'revenue > 10000 ? "gold" : (revenue > 5000 ? "silver" : "bronze")'
```

### 7. Boolean Logic

**Complex business rules:**
```bash
# Eligible if: (age >= 18 AND has email) OR is admin
ssql where -if-expr '(age >= 18 and has("email")) or role == "admin"'

# Active customer: has recent order AND good standing
ssql where -if-expr 'days_since_order <= 90 and balance >= 0 and status != "suspended"'
```

### 8. Null/Missing Field Handling

**Safe navigation:**
```bash
ssql update -set-expr total 'getOr("price", 0) * getOr("qty", 1) + getOr("tax", 0)'
```

**Conditional on field existence:**
```bash
ssql where -if-expr 'has("premium") and premium == true'
ssql update -set-expr type 'has("premium") ? "premium" : "standard"'
```

### 9. Pipeline Composition

**Combine with other ssql commands:**
```bash
# Read, filter with expression, calculate fields, filter again
ssql from sales.csv | \
  ssql where -if-expr 'region == "West" and year == 2024' | \
  ssql update -set-expr commission 'sales * 0.05' | \
  ssql where -if-expr 'commission > 1000' | \
  ssql to csv high_performers.csv
```

### 10. Code Generation

**Generate optimized Go code from expressions:**
```bash
# The pipeline stages see SSQL_MODE and emit code instead of running;
# generate go compiles and runs the program (or > program.go to keep it)
(export SSQL_MODE=typed
 ssql from data.csv | \
   ssql where -if-expr 'price * qty > 1000' | \
   ssql update -set-expr total 'price * qty' | \
   ssql update -set-expr tier 'total > 5000 ? "premium" : "standard"' | \
   ssql to table) | ssql generate go -run
```

**Generated code:**
- Expressions **transpile to native Go** in generated programs — a
  predicate like `price * qty > 1000` becomes plain Go comparisons, not
  an interpreted evaluation (in typed and record modes alike)
- Zero allocations per row on the native path (~3ns/row typed,
  ~28ns/row record)
- An expression outside the native subset runs in the embedded
  expression VM **for that expression only** — the rest of the pipeline
  keeps its typed/parallel form. `generate go -explain` says which
  expressions became Go and which did not, and why
- Clean, readable Go code; full type safety

## Performance

**Interpreted execution (the `ssql` CLI itself):**
- Expressions compile once at startup (~100 microseconds), then
  evaluate at ~1-2 microseconds per record via the expr-lang VM

**Generated code (`generate go`):**
- Native-subset expressions cost single-digit nanoseconds per row with
  zero allocations; measured end-to-end on 5M rows, an expression
  filter + group-by pipeline runs ~19x faster (and in 3.6x less
  memory) than interpreting the same expressions in the generated
  program, and `-stream-expr` folds drop from gigabytes to megabytes of
  peak memory
- `group-by -expr` aggregations generate mergeable accumulators and
  keep the parallel group-by; `-stream-expr` folds generate typed
  accumulators (serial — folds don't merge)

**Best Practices:**
1. ✅ Use expressions for complex logic (vs. multiple commands)
2. ✅ Use code generation for production workloads
3. ✅ Check `generate go -explain` — it says which expressions became
   native Go and which run in the embedded VM, and names the construct
   to rewrite for the native path
4. ✅ In `group-by -expr`, prefer `count()` (native, parallel) over the
   `len(field)` group-size idiom, which runs in the VM

## Examples by Use Case

### Financial Calculations

```bash
# Calculate tax and total
ssql update -set-expr tax 'subtotal * 0.08' | \
  ssql update -set-expr total 'subtotal + tax'

# Apply tiered discount
ssql update -set-expr discount 'amount > 1000 ? 0.15 : (amount > 500 ? 0.10 : 0.05)'

# Calculate profit margin
ssql update -set-expr margin '((revenue - cost) / revenue) * 100'
```

### User Segmentation

```bash
# Active users with recent activity
ssql where -if-expr 'status == "active" and days_since_login <= 30'

# VIP customers
ssql where -if-expr 'total_purchases > 10000 or subscription == "premium"'

# At-risk customers
ssql where -if-expr 'days_since_purchase > 90 and lifetime_value > 1000'
```

### Data Quality

```bash
# Valid email addresses
ssql where -if-expr 'has("email") and email contains "@" and len(email) > 5'

# Complete profiles
ssql where -if-expr 'has("name") and has("email") and has("phone") and has("address")'

# Reasonable values
ssql where -if-expr 'age >= 0 and age <= 120 and salary >= 0 and salary <= 10000000'
```

### Text Processing

```bash
# Standardize names
ssql update -set-expr name 'upper(trim(name))'

# Extract initials
ssql update -set-expr initials 'upper(first[0:1]) + upper(last[0:1])'

# Create slugs
ssql update -set-expr slug 'lower(join(split(trim(title), " "), "-"))'
```

## Expression Syntax Reference

### Precedence (highest to lowest)

1. Function calls: `upper(name)`, `round(value)`
2. Member access: `obj.field`, `arr[0]`
3. Unary: `not`, `-`
4. Power: `**`
5. Multiplication: `*`, `/`, `%`
6. Addition: `+`, `-`
7. Comparison: `<`, `>`, `<=`, `>=`
8. Equality: `==`, `!=`
9. Logical AND: `and`
10. Logical OR: `or`
11. Ternary: `? :`
12. Pipe: `|`

### Literals

```bash
# Numbers
42              # Integer
3.14            # Float
1.5e6           # Scientific notation

# Strings
"hello"         # Double quotes
'world'         # Single quotes
"it's \"ok\""   # Escaped quotes

# Booleans
true
false

# Arrays
[1, 2, 3]
["a", "b", "c"]
[1, "mixed", true]  # Mixed types

# Nil
nil
```

### Comments

Expressions do **not** support comments. Keep expressions concise and use command descriptions for documentation.

## Integration with ssql Commands

### Flags vs expressions — which to use?

`-if FIELD OP VALUE` and `-if-expr 'FIELD OP VALUE'` produce **identical
results**, and both forms compile to the same code in generated
programs. The difference is ergonomics and analyzability:

**Prefer the flag form when it can express the condition:**
- **Tab completion** works on every part: field names from the live
  pipeline, operators, and *actual data values*. An expression is an
  opaque string to the completion system.
- **No shell quoting** — `-if region ne south` vs
  `-if-expr 'region != "south"'` — which compounds over SSH and in
  remote/pushdown pipelines.
- **The optimizer can reason about it**: `generate ssql`'s rewrites
  (range tightening, contradiction detection, catalog partition pruning,
  join pushdown) operate on flag conditions. So do the catalog `-if`
  pruning flags and multi-file `--` pushdown.

**Use the expression form for everything else**: arithmetic
(`price * qty > 1000`), functions (`len(name) > 3`), cross-field
comparisons (`price > cost`), OR within one condition, `has()`/`??`
missing-field handling.

**Best of both:** the optimizer *canonicalizes* trivial expressions —
`-if-expr 'pop > 9 && pop > 5'` is rewritten to `-if pop gt 9` (shown
under `generate ssql -explain` as `expr-canonicalization`), so simple
expressions inherit the flag form's optimizations automatically.
Float-literal comparisons and OR-expressions are deliberately left
alone.

### update command

**Syntax:** `ssql update -set-expr <field> '<expression>'`

**Features:**
- Set multiple fields with multiple `-set-expr` flags
- Combine with `-set` for literal values
- Use `-if` for conditional updates (first-match-wins)
- Clause separators: `+` and `-` both start a new clause; the first
  clause whose conditions match applies its updates and the rest are
  skipped (if-then-else)

**Examples:**
```bash
# Single expression
ssql update -set-expr total 'price * qty'

# Multiple expressions
ssql update -set-expr total 'price * qty' -set-expr tax 'total * 0.08'

# Conditional with expression
ssql update -if dept eq Sales -set-expr commission 'revenue * 0.05'

# If-else logic with clauses (a literal value is -set; -set-expr would
# read `minor` as a field name)
ssql update \
  -if age lt 18 -set category minor + \
  -if age ge 18 -set category adult
```

### where command

**Syntax:** `ssql where -if-expr '<boolean-expression>'`

**Features:**
- Expression must return boolean value
- Combine with `-if` conditions; everything in one clause must hold (AND)
- Multiple `-if-expr` within a clause use AND logic
- Clause separators: `+` (OR between clauses)

**Examples:**
```bash
# Single expression filter
ssql where -if-expr 'price * qty > 1000'

# Multiple expressions (AND within clause)
ssql where -if-expr 'age >= 18' -if-expr 'status == "active"'

# Multiple clauses (OR between clauses)
ssql where -if-expr 'dept == "Sales"' + -if-expr 'dept == "Marketing"'

# Combine with -if
ssql where -if verified eq true -if-expr 'age >= 18'
```

## Error Handling

**Compilation Errors:**
- Detected at startup before processing records
- Clear error messages with expression location
- CLI exits with error code

```bash
$ ssql where -if-expr 'age >'
Error: compiling expression "age >": unexpected end of expression
```

**Runtime Errors** stop the pipeline. An expression that cannot be
evaluated for a record — a conversion that fails, a field the record does
not have — is an error, never a default or an empty value, in the CLI and
in generated programs alike:

```bash
$ ssql from employees.csv | ssql update -set-expr x 'int("abc")'
Error: evaluating expression for field "x": execute expression: invalid operation: int(abc) (1:1)
$ ssql from employees.csv | ssql update -set-expr x 'nosuch + 1'
Error: evaluating expression for field "x": expression references unknown field(s): nosuch
```

**Type Errors:**
- `where -if-expr` must produce a boolean; anything else is refused
  before the first record: `Expression must return boolean, got int64`

**Best Practices:**
1. ✅ Test expressions on a sample first (`ssql from csv data.csv -sample 1000`)
2. ✅ Use `has()` and `getOr()` for fields that are genuinely optional
3. ✅ Inspect intermediate results with `ssql limit 5 | ssql to table` or `ssql describe`

## Further Reading

**expr-lang Documentation:**
- Official docs: https://expr-lang.org/docs/language-definition
- GitHub: https://github.com/expr-lang/expr

**ssql Documentation:**
- Getting Started: [doc/codelab-intro.md](codelab-intro.md)
- CLI Tutorial: [doc/cli-codelab.md](cli-codelab.md)
- API Reference: [doc/api-reference.md](api-reference.md)
- Troubleshooting: [doc/cli-troubleshooting.md](cli-troubleshooting.md)

**Design notes:** [how expressions become native Go](research/expr-transpiler-paper.md), with the measurements behind the numbers above.

---

**Ready to use expressions?** Start with simple examples and build up to complex pipelines. Use `ssql update -help` and `ssql where -help` for quick reference.

*Powered by [expr-lang](https://expr-lang.org/).*
