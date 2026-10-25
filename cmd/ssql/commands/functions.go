package commands

import (
	"io"
	"fmt"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
)

// RegisterFunctions registers the functions subcommand
func RegisterFunctions(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("functions").
		Description("Show available expression functions and operators").
		Example("ssql functions", "List all function categories").
		Example("ssql functions -category string", "Show string functions in detail").
		Example("ssql functions -examples", "Show common expression patterns").
		Example("ssql functions bucket", "Show one function's entry — what Alt-h shows when the cursor is on that name in an expression").

		Flag("NAME").
			String().
			Global().
			Default("").
			Completer(cf.NoCompleter{Hint: "<function>"}).
			Help("A function name: show just its entry (signature, description, example)").
			Done().

		Flag("-category", "-c").
			String().
			Completer(&cf.StaticCompleter{Options: []string{"string", "math", "array", "type", "date", "map", "bitwise", "hash", "operators", "helpers"}}).
			Global().
			Default("").
			Help("Show detailed help for a category").
			Done().

		Flag("-examples", "-e").
			Bool().
			Global().
			Help("Show common expression patterns and examples").
			Done().

		Handler(func(ctx *cf.Context) error {
			var category string
			var showExamples bool

			if cat, ok := ctx.GlobalFlags["-category"]; ok {
				category = cat.(string)
			}

			if ex, ok := ctx.GlobalFlags["-examples"]; ok {
				showExamples = ex.(bool)
			}

			if showExamples {
				return printExamples(ctx)
			}

			if name, _ := ctx.GlobalFlags["NAME"].(string); name != "" {
				entry, ok := FunctionEntry(name)
				if !ok {
					return fmt.Errorf("functions: no function %q — `ssql functions` lists them all, `ssql functions -category CAT` one category", name)
				}
				fmt.Fprint(ctx.Stdout(), entry)
				return nil
			}

			if category == "" {
				return printAllCategories(ctx)
			}

			return printCategory(ctx, category)
		}).
		Done()
	return cmd
}

// FunctionsReference is the concise expression-function reference printed by
// `ssql functions` (no args) and appended to the Alt-h help popup when the
// cursor is on an expression argument (see the -help-at intercept in main.go),
// so the function list is at hand exactly when you're authoring an expression.
// Single source of truth — keep in sync with doc/EXPRESSIONS.md.
const FunctionsReference = `EXPRESSION FUNCTIONS AVAILABLE:

String Functions (15):
  upper(str), lower(str), trim(str), trimPrefix(str, prefix),
  trimSuffix(str, suffix), split(str, sep), splitAfter(str, sep),
  join(arr, sep), replace(str, old, new), replaceRegex(str, pat, repl),
  repeat(str, n), indexOf(str, sub), lastIndexOf(str, sub),
  hasPrefix(str, prefix), hasSuffix(str, suffix)

Math Functions (41):
  round(num), round(num, places), floor(num), ceil(num), trunc(num), abs(num), sign(num), min(a, b), max(a, b)
  sqrt(x), cbrt(x), exp(x), ln(x), log10(x), log2(x), log(b, x), pow(x, y), pi()
  sin(x), cos(x), tan(x), asin(x), acos(x), atan(x), atan2(y, x)
  sinh(x), cosh(x), tanh(x), asinh(x), acosh(x), atanh(x), degrees(x), radians(x)
  isnan(x), isinf(x), isfinite(x), gcd(a, b), lcm(a, b), factorial(n), gamma(x), lgamma(x)

Array Functions (26):
  len(arr), first(arr), last(arr), take(arr, n), reverse(arr),
  sort(arr), sortBy(arr, pred), uniq(arr), flatten(arr), concat(a, b),
  filter(arr, pred), map(arr, fn), reduce(arr, pred, init),
  find(arr, pred), findIndex(arr, pred), findLast(arr, pred),
  findLastIndex(arr, pred), groupBy(arr, pred),
  all(arr, pred), any(arr, pred), one(arr, pred), none(arr, pred),
  count(arr), sum(arr), mean(arr), median(arr)

Date Functions (22):
  now(), date(v), year(ts), month(ts), day(ts), hour(ts), minute(ts), second(ts),
  dow(ts), doy(ts), week(ts), quarter(ts), epoch(ts),
  date_trunc(ts, unit), date_add(ts, n, unit), date_diff(unit, a, b), tz(ts, zone),
  strftime(ts, pattern), strptime(text, pattern), bucket(ts, dur), duration(str), timezone(str)

Type & Encoding (11):
  int(v), int128(v), float(v), string(v), type(v),
  toJSON(v), fromJSON(str), toBase64(str), fromBase64(str),
  toPairs(map), fromPairs(arr)

Map Functions (3):
  keys(map), values(map), get(v, key)

Bitwise Functions (8):
  bitand(a, b), bitor(a, b), bitxor(a, b), bitnand(a, b),
  bitnot(a), bitshl(a, n), bitshr(a, n), bitushr(a, n)

Hash Functions (4):
  hash(n), hash(n, seed), sha256(str), sha1(str), md5(str)

Helpers (2):
  has(field), getOr(field, default)

Operators:
  +  -  *  /  %  **  ==  !=  <  >  <=  >=  and  or  not  ?:  ??  in  |

Use: ssql functions -category <name>   # Show detailed help for category
     ssql functions -examples          # Show common expression patterns

Full reference: doc/EXPRESSIONS.md
`

func printAllCategories(ctx *cf.Context) error {
	fmt.Fprint(ctx.Stdout(), FunctionsReference)
	return nil
}

func printCategory(ctx *cf.Context, category string) error {
	switch strings.ToLower(category) {
	case "string":
		writeStringFunctions(ctx.Stdout())
		return nil
	case "math":
		writeMathFunctions(ctx.Stdout())
		return nil
	case "array":
		writeArrayFunctions(ctx.Stdout())
		return nil
	case "type":
		writeTypeFunctions(ctx.Stdout())
		return nil
	case "date":
		writeDateFunctions(ctx.Stdout())
		return nil
	case "map":
		writeMapFunctions(ctx.Stdout())
		return nil
	case "bitwise":
		writeBitwiseFunctions(ctx.Stdout())
		return nil
	case "hash":
		writeHashFunctions(ctx.Stdout())
		return nil
	case "operators":
		writeOperators(ctx.Stdout())
		return nil
	case "helpers":
		writeHelpers(ctx.Stdout())
		return nil
	default:
		fmt.Fprintf(ctx.Stdout(), "Unknown category: %s\n", category)
		fmt.Fprintln(ctx.Stdout())
		fmt.Fprintln(ctx.Stdout(), "Available categories: string, math, array, date, type, map, bitwise, hash, operators, helpers")
		return nil
	}
}

func writeStringFunctions(w io.Writer) {
	fmt.Fprintln(w, "STRING FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  upper(str)              Convert to uppercase")
	fmt.Fprintln(w, "    Example: upper(\"hello\") → \"HELLO\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  lower(str)              Convert to lowercase")
	fmt.Fprintln(w, "    Example: lower(\"WORLD\") → \"world\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  trim(str)               Remove leading/trailing whitespace")
	fmt.Fprintln(w, "    Example: trim(\"  text  \") → \"text\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  trimPrefix(str, prefix) Remove prefix from string")
	fmt.Fprintln(w, "    Example: trimPrefix(\"hello world\", \"hello \") → \"world\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  trimSuffix(str, suffix) Remove suffix from string")
	fmt.Fprintln(w, "    Example: trimSuffix(\"hello.csv\", \".csv\") → \"hello\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  split(str, sep)         Split string into array")
	fmt.Fprintln(w, "    Example: split(\"a,b,c\", \",\") → [\"a\", \"b\", \"c\"]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  splitAfter(str, sep)    Split string, keeping separator attached")
	fmt.Fprintln(w, "    Example: splitAfter(\"a,b,c\", \",\") → [\"a,\", \"b,\", \"c\"]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  join(arr, sep)          Join array into string")
	fmt.Fprintln(w, "    Example: join([\"a\", \"b\"], \",\") → \"a,b\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  replace(str, old, new)  Replace all occurrences of substring")
	fmt.Fprintln(w, "    Example: replace(\"hello world\", \"world\", \"there\") → \"hello there\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  replaceRegex(str, pattern, replacement)")
	fmt.Fprintln(w, "                          Regex replace with capture group support")
	fmt.Fprintln(w, "    Example: replaceRegex(\"abc 123\", \"[^0-9]\", \"\") → \"123\"")
	fmt.Fprintln(w, "    Example: replaceRegex(phone, \"(\\\\d{3})(\\\\d{4})\", \"$1-$2\")")
	fmt.Fprintln(w, "    Note: Use \\\\ for regex backslashes (e.g., \\\\d, \\\\w, \\\\s)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  repeat(str, n)          Repeat string n times")
	fmt.Fprintln(w, "    Example: repeat(\"ab\", 3) → \"ababab\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  indexOf(str, sub)       First index of substring (-1 if not found)")
	fmt.Fprintln(w, "    Example: indexOf(\"hello\", \"ll\") → 2")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  lastIndexOf(str, sub)   Last index of substring (-1 if not found)")
	fmt.Fprintln(w, "    Example: lastIndexOf(\"hello\", \"l\") → 3")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  hasPrefix(str, prefix)  Check if starts with prefix")
	fmt.Fprintln(w, "    Example: hasPrefix(\"hello\", \"he\") → true")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  hasSuffix(str, suffix)  Check if ends with suffix")
	fmt.Fprintln(w, "    Example: hasSuffix(\"world\", \"ld\") → true")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  str contains substr     Check if contains substring (OPERATOR only)")
	fmt.Fprintln(w, "    Example: \"hello\" contains \"ll\" → true")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Note: contains, startsWith, endsWith are operators, not functions:")
	fmt.Fprintln(w, "    name startsWith \"A\"    email contains \"@\"    file endsWith \".csv\"")
	fmt.Fprintln(w, "    (contains(email, \"@\") is a parse error — use `email contains \"@\"`)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Fprintln(w, "  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Fprintln(w, "  ssql update -set-expr clean 'replaceRegex(name, \"[^a-zA-Z]\", \"_\")'")
	fmt.Fprintln(w, "  ssql update -set-expr base 'trimSuffix(filename, \".csv\")'")
}

func writeMathFunctions(w io.Writer) {
	fmt.Fprintln(w, "MATH FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  round(num)     Round to nearest integer")
	fmt.Fprintln(w, "    Example: round(3.7) → 4")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  floor(num)     Round down")
	fmt.Fprintln(w, "    Example: floor(3.7) → 3")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  ceil(num)      Round up")
	fmt.Fprintln(w, "    Example: ceil(3.2) → 4")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  abs(num)       Absolute value")
	fmt.Fprintln(w, "    Example: abs(-5) → 5")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  min(a, b)      Minimum of two values")
	fmt.Fprintln(w, "    Example: min(10, 20) → 10")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  max(a, b)      Maximum of two values")
	fmt.Fprintln(w, "    Example: max(10, 20) → 20")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  round(num, places) Round half away from zero at places decimals")
	fmt.Fprintln(w, "    Example: round(3.14159, 2) → 3.14")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  trunc(num)     Drop the fraction, toward zero")
	fmt.Fprintln(w, "    Example: trunc(-3.7) → -3")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  sign(num)      -1, 0 or 1")
	fmt.Fprintln(w, "    Example: sign(-2.5) → -1")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  sqrt(x)        Square root")
	fmt.Fprintln(w, "    Example: sqrt(16) → 4")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  cbrt(x)        Cube root")
	fmt.Fprintln(w, "    Example: cbrt(27) → 3")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  exp(x)         e to the power x")
	fmt.Fprintln(w, "    Example: exp(1) → 2.718281828459045")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  ln(x)          Natural logarithm")
	fmt.Fprintln(w, "    Example: ln(exp(2)) → 2")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  log10(x)       Logarithm base 10")
	fmt.Fprintln(w, "    Example: log10(1000) → 3")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  log2(x)        Logarithm base 2")
	fmt.Fprintln(w, "    Example: log2(8) → 3")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  log(b, x)      Logarithm of x in base b (no one-argument log: say ln or log10)")
	fmt.Fprintln(w, "    Example: log(3, 81) → 4")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  pow(x, y)      x to the power y (also x ** y)")
	fmt.Fprintln(w, "    Example: pow(2, 10) → 1024")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  pi()           π")
	fmt.Fprintln(w, "    Example: pi() → 3.141592653589793")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  sin(x)         Sine, x in radians")
	fmt.Fprintln(w, "    Example: sin(pi() / 2) → 1")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  cos(x)         Cosine, x in radians")
	fmt.Fprintln(w, "    Example: cos(0) → 1")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  tan(x)         Tangent, x in radians")
	fmt.Fprintln(w, "    Example: tan(0) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  asin(x)        Arc sine, in radians")
	fmt.Fprintln(w, "    Example: asin(1) → 1.5707963267948966")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  acos(x)        Arc cosine, in radians")
	fmt.Fprintln(w, "    Example: acos(1) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  atan(x)        Arc tangent, in radians")
	fmt.Fprintln(w, "    Example: atan(0) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  atan2(y, x)    Angle of the point (x, y), in radians")
	fmt.Fprintln(w, "    Example: atan2(1, 1) → 0.7853981633974483")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  sinh(x)        Hyperbolic sine")
	fmt.Fprintln(w, "    Example: sinh(0) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  cosh(x)        Hyperbolic cosine")
	fmt.Fprintln(w, "    Example: cosh(0) → 1")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  tanh(x)        Hyperbolic tangent")
	fmt.Fprintln(w, "    Example: tanh(0) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  asinh(x)       Inverse hyperbolic sine")
	fmt.Fprintln(w, "    Example: asinh(0) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  acosh(x)       Inverse hyperbolic cosine")
	fmt.Fprintln(w, "    Example: acosh(1) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  atanh(x)       Inverse hyperbolic tangent")
	fmt.Fprintln(w, "    Example: atanh(0) → 0")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  degrees(x)     Radians to degrees")
	fmt.Fprintln(w, "    Example: degrees(pi()) → 180")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  radians(x)     Degrees to radians")
	fmt.Fprintln(w, "    Example: radians(180) → 3.141592653589793")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  isnan(x)       Whether x is NaN (seen only inside the expression: a result that is not a number is absent once written)")
	fmt.Fprintln(w, "    Example: isnan(0.0 / 0.0) → true")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  isinf(x)       Whether x is infinite")
	fmt.Fprintln(w, "    Example: isinf(1 / 0.0) → true")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  isfinite(x)    Whether x is a finite number")
	fmt.Fprintln(w, "    Example: isfinite(ln(0)) → false")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  gcd(a, b)      Greatest common divisor of two integers")
	fmt.Fprintln(w, "    Example: gcd(12, 18) → 6")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  lcm(a, b)      Least common multiple of two integers")
	fmt.Fprintln(w, "    Example: lcm(4, 6) → 12")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  factorial(n)   n! (exact to 20!)")
	fmt.Fprintln(w, "    Example: factorial(5) → 120")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  gamma(x)       The gamma function")
	fmt.Fprintln(w, "    Example: gamma(5) → 24")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  lgamma(x)      The natural log of the absolute value of gamma(x)")
	fmt.Fprintln(w, "    Example: lgamma(5) → 3.1780538303479458")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  A result that is not a number — sqrt of a negative, ln(0), overflow — is absent in every lane (DFC153).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql update -set-expr final_price 'round(price * 0.85)'")
	fmt.Fprintln(w, "  ssql update -set-expr balance 'max(0, amount - fees)'")
	fmt.Fprintln(w, "  ssql where -if-expr 'abs(actual - expected) < 0.01'")
}

func writeArrayFunctions(w io.Writer) {
	fmt.Fprintln(w, "ARRAY FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Access:")
	fmt.Fprintln(w, "  len(arr)                  Length of array/string")
	fmt.Fprintln(w, "  first(arr)                First element")
	fmt.Fprintln(w, "  last(arr)                 Last element")
	fmt.Fprintln(w, "  get(v, key)               Safe access (returns nil if missing)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Transform:")
	fmt.Fprintln(w, "  take(arr, n)              First n elements")
	fmt.Fprintln(w, "  reverse(arr)              Reverse array")
	fmt.Fprintln(w, "  sort(arr)                 Sort ascending")
	fmt.Fprintln(w, "  sortBy(arr, pred)         Sort by predicate result")
	fmt.Fprintln(w, "    Example: sortBy([{\"n\":3},{\"n\":1}], {.n}) → [{\"n\":1},{\"n\":3}]")
	fmt.Fprintln(w, "  uniq(arr)                 Remove duplicates")
	fmt.Fprintln(w, "  flatten(arr)              Flatten nested arrays")
	fmt.Fprintln(w, "  concat(arr1, arr2)        Concatenate arrays")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Filter & Search:")
	fmt.Fprintln(w, "  filter(arr, pred)         Filter elements matching predicate")
	fmt.Fprintln(w, "  find(arr, pred)           First matching element")
	fmt.Fprintln(w, "  findIndex(arr, pred)      Index of first match")
	fmt.Fprintln(w, "  findLast(arr, pred)       Last matching element")
	fmt.Fprintln(w, "  findLastIndex(arr, pred)  Index of last match")
	fmt.Fprintln(w, "  groupBy(arr, pred)        Group elements by predicate result")
	fmt.Fprintln(w, "    Example: groupBy([1,2,3,4], {# % 2 == 0 ? \"even\" : \"odd\"})")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Test:")
	fmt.Fprintln(w, "  all(arr, pred)            All elements match")
	fmt.Fprintln(w, "  any(arr, pred)            At least one matches")
	fmt.Fprintln(w, "  one(arr, pred)            Exactly one matches")
	fmt.Fprintln(w, "  none(arr, pred)           No elements match")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Aggregate:")
	fmt.Fprintln(w, "  count(arr)                Count of elements")
	fmt.Fprintln(w, "  sum(arr)                  Sum of elements")
	fmt.Fprintln(w, "  mean(arr)                 Average of elements")
	fmt.Fprintln(w, "  median(arr)               Median of elements")
	fmt.Fprintln(w, "  reduce(arr, pred, init)   Reduce to single value")
	fmt.Fprintln(w, "    Example: reduce([1,2,3,4], {# + #acc}, 0) → 10")
	fmt.Fprintln(w, "  map(arr, fn)              Transform each element")
	fmt.Fprintln(w, "    Example: map([1,2,3], {# * 2}) → [2,4,6]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Note: In predicates, # = current element, #acc = accumulator")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql where -if-expr 'all(scores, {# >= 60})'")
	fmt.Fprintln(w, "  ssql update -set-expr avg_score 'mean(scores)'")
	fmt.Fprintln(w, "  ssql update -set-expr top3 'take(sort(scores), 3)'")
}

func writeDateFunctions(w io.Writer) {
	p := func(line string) { io.WriteString(w, line+"\n") } // lines hold strftime % directives: never a Printf format
	p("DATE FUNCTIONS (a time is an instant; its calendar is read in the pipeline's zone — ssql conventions -category data):")
	p("")
	p("  now()                   The current instant")
	p("    Example: hour(now()) → the local hour")
	p("")
	p("  date(v)                 Parse a time: RFC 3339, 2026-01-15 10:30:00, a bare date (UTC), Unix seconds")
	p("    Also: date(text, pattern[, zone]) — strftime when the pattern has %, else a Go layout")
	p("    Example: date(\"2026-01-15\"), date(\"15/01/2026\", \"%d/%m/%Y\")")
	p("")
	p("  year(ts)                Year, in the pipeline zone")
	p("  month(ts)               Month 1-12")
	p("  day(ts)                 Day of month 1-31")
	p("  hour(ts)                Hour 0-23")
	p("    Example: hour(tz(ts, \"UTC\")) reads another zone")
	p("  minute(ts)              Minute 0-59")
	p("  second(ts)              Second 0-59")
	p("  dow(ts)                 Day of week, Sunday = 0")
	p("  doy(ts)                 Day of year 1-366")
	p("  week(ts)                ISO week 1-53")
	p("  quarter(ts)             Quarter 1-4")
	p("  epoch(ts)               Unix seconds (zone-free)")
	p("")
	p("  date_trunc(ts, unit)    Start of the year|quarter|month|week|day|hour|minute|second (weeks start Monday)")
	p("    Example: date_trunc(ts, \"month\")   (flag form: update -set-bucket month ts month)")
	p("  date_add(ts, n, unit)   ts plus n units; months/years clamp to the month's end (Jan 31 + 1 month = Feb 28)")
	p("    Example: date_add(ts, -7, \"day\")")
	p("  date_diff(unit, a, b)   Unit boundaries crossed from a to b (Jan 31 → Feb 1 is 1 month); negative when b is first")
	p("    Example: date_diff(\"day\", created, now())")
	p("  tz(ts, zone)            The same instant read in another zone by the functions around it")
	p("    Example: hour(tz(ts, \"America/New_York\"))")
	p("")
	p("  strftime(ts, pattern)   Format on the pipeline zone's wall clock (%Y-%m-%d %H:%M, %b %-d; DuckDB's and Python's directives)")
	p("    Example: strftime(ts, \"%d/%m/%Y\")")
	p("  strptime(text, pattern) Parse text with a strftime pattern (a wall clock in the pipeline zone)")
	p("    Example: strptime(when, \"%d/%m/%Y %H:%M\")")
	p("")
	p("  bucket(ts, dur)         Snap down to an epoch-aligned bucket of a fixed width (zone-free)")
	p("    Example: bucket(ts, \"1m\")   (flag form: update -set-bucket minute ts 1m)")
	p("  duration(str)           Parse a Go duration (ns, us, ms, s, m, h)")
	p("    Example: ts + duration(\"1h30m\")")
	p("  timezone(str)           A Go time.Location for ts.In(): prefer tz(ts, zone)")
	p("")
	p("The names are DuckDB's; generate sql emits the engine's own functions for all of them.")
	p("The zone: from … -tz Australia/Sydney, else TZ, else /etc/localtime; carried in the _schema header.")
	p("Patterns on flags: from csv -format COL PATTERN, cast -format COL PATTERN, to csv|tsv|table|markdown -time-format PATTERN.")
	p("")
	p("Common Usage:")
	p("  ssql update -set-expr month 'date_trunc(ts, \"month\")' | ssql group-by month -sum amount total")
	p("  ssql where -if-expr 'hour(ts) >= 9 && dow(ts) in [1,2,3,4,5]'")
	p("  ssql update -set-expr age_days 'date_diff(\"day\", created, now())'")
}

func writeTypeFunctions(w io.Writer) {
	fmt.Fprintln(w, "TYPE & ENCODING FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Type Conversion:")
	fmt.Fprintln(w, "  int(value)              Convert to integer")
	fmt.Fprintln(w, "    Example: int(\"123\") → 123")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  int128(value)           Widen to a 128-bit integer (the int128 column type, DuckDB's HUGEINT): an integer, a whole float, or a digit string — the way to write a literal past int64")
	fmt.Fprintln(w, "    Example: int128(\"123456789012345678901234567890\") * 2 → 246913578024691357802469135780")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  float(value)            Convert to float")
	fmt.Fprintln(w, "    Example: float(\"3.14\") → 3.14")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  string(value)           Convert to string")
	fmt.Fprintln(w, "    Example: string(123) → \"123\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  type(value)             Get type name as string")
	fmt.Fprintln(w, "    Example: type(42) → \"int\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "JSON:")
	fmt.Fprintln(w, "  toJSON(value)           Serialize to JSON string")
	fmt.Fprintln(w, "    Example: toJSON({\"a\": 1}) → \"{\\\"a\\\":1}\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  fromJSON(str)           Parse JSON string to value")
	fmt.Fprintln(w, "    Example: fromJSON(\"{\\\"a\\\":1}\") → {\"a\": 1}")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Base64:")
	fmt.Fprintln(w, "  toBase64(str)           Encode to Base64")
	fmt.Fprintln(w, "    Example: toBase64(\"hello\") → \"aGVsbG8=\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  fromBase64(str)         Decode from Base64")
	fmt.Fprintln(w, "    Example: fromBase64(\"aGVsbG8=\") → \"hello\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Map Conversion:")
	fmt.Fprintln(w, "  toPairs(map)            Map to [[key, value], ...] pairs")
	fmt.Fprintln(w, "    Example: toPairs({\"a\":1}) → [[\"a\", 1]]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  fromPairs(arr)          [[key, value], ...] pairs to map")
	fmt.Fprintln(w, "    Example: fromPairs([[\"a\",1]]) → {\"a\": 1}")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql update -set-expr age_num 'int(age_str)'")
	fmt.Fprintln(w, "  ssql update -set-expr label 'string(round(value * 100)) + \"%\"'")
	fmt.Fprintln(w, "  ssql update -set-expr encoded 'toBase64(email)'")
}

func writeMapFunctions(w io.Writer) {
	fmt.Fprintln(w, "MAP FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  keys(map)               Get all keys from map")
	fmt.Fprintln(w, "    Example: keys({\"a\":1, \"b\":2}) → [\"a\", \"b\"]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  values(map)             Get all values from map")
	fmt.Fprintln(w, "    Example: values({\"a\":1, \"b\":2}) → [1, 2]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  get(v, key)             Safe access (returns nil if missing)")
	fmt.Fprintln(w, "    Example: get({\"a\":1}, \"a\") → 1")
	fmt.Fprintln(w, "    Example: get({\"a\":1}, \"z\") → nil")
	fmt.Fprintln(w, "    Also works on arrays: get([10,20,30], 1) → 20")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql update -set-expr field_count 'len(keys(metadata))'")
}

func writeBitwiseFunctions(w io.Writer) {
	fmt.Fprintln(w, "BITWISE FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitand(a, b)     Bitwise AND")
	fmt.Fprintln(w, "    Example: bitand(0xFF, 0x0F) → 15")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitor(a, b)      Bitwise OR")
	fmt.Fprintln(w, "    Example: bitor(0xF0, 0x0F) → 255")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitxor(a, b)     Bitwise XOR")
	fmt.Fprintln(w, "    Example: bitxor(0xFF, 0x0F) → 240")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitnand(a, b)    Bitwise AND NOT")
	fmt.Fprintln(w, "    Example: bitnand(0xFF, 0x0F) → 240")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitnot(a)        Bitwise NOT")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitshl(a, n)     Left shift")
	fmt.Fprintln(w, "    Example: bitshl(1, 4) → 16")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitshr(a, n)     Right shift")
	fmt.Fprintln(w, "    Example: bitshr(16, 4) → 1")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bitushr(a, n)    Unsigned right shift")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql update -set-expr masked 'bitand(flags, 0x0F)'")
	fmt.Fprintln(w, "  ssql where -if-expr 'bitand(permissions, 4) != 0'")
}

func writeHashFunctions(w io.Writer) {
	fmt.Fprintln(w, "HASH FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  hash(n)        Reproducible 32-bit mix of an integer: the same value in every lane and on every machine")
	fmt.Fprintln(w, "    Example: hash(1) → 3531918615")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  hash(n, seed)  The same with a seed, an independent stream per seed")
	fmt.Fprintln(w, "    Example: hash(1, 7) → 558192479")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  sha256(str)    SHA-256 hash (64 hex characters)")
	fmt.Fprintln(w, "    Example: sha256(\"hello\") → \"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  sha1(str)      SHA-1 hash (40 hex characters)")
	fmt.Fprintln(w, "    Example: sha1(\"hello\") → \"aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  md5(str)       MD5 hash (32 hex characters)")
	fmt.Fprintln(w, "    Example: md5(\"hello\") → \"5d41402abc4b2a76b9719d911017c592\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql from range 1000 | ssql update -set-expr key 'hash(n) % 100' -set-expr amount 'hash(n, 1) % 500'")
	fmt.Fprintln(w, "  ssql update -set-expr email_hash 'sha256(email)'")
	fmt.Fprintln(w, "  ssql update -set-expr id 'sha256(name + \":\" + email)'")
	fmt.Fprintln(w, "  ssql update -set-expr checksum 'md5(content)'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Note: hash() is for spread, not security — a fixture, a demo or a benchmark needs")
	fmt.Fprintln(w, "      no file when `from range` and hash() make its keys. Prefer sha256 over md5/sha1.")
}

func writeOperators(w io.Writer) {
	fmt.Fprintln(w, "OPERATORS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Arithmetic:")
	fmt.Fprintln(w, "  +    Addition:         price + tax")
	fmt.Fprintln(w, "  -    Subtraction:      revenue - cost")
	fmt.Fprintln(w, "  *    Multiplication:   price * qty")
	fmt.Fprintln(w, "  /    Division:         total / count")
	fmt.Fprintln(w, "  %    Modulo:           value % 10")
	fmt.Fprintln(w, "  **   Power:            base ** exponent")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Comparison:")
	fmt.Fprintln(w, "  ==   Equal:            status == \"active\"")
	fmt.Fprintln(w, "  !=   Not equal:        dept != \"Sales\"")
	fmt.Fprintln(w, "  <    Less than:        age < 18")
	fmt.Fprintln(w, "  >    Greater than:     salary > 50000")
	fmt.Fprintln(w, "  <=   Less/equal:       score <= 100")
	fmt.Fprintln(w, "  >=   Greater/equal:    age >= 21")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Logical:")
	fmt.Fprintln(w, "  and  Logical AND:      age >= 18 and status == \"active\"")
	fmt.Fprintln(w, "  or   Logical OR:       dept == \"Sales\" or dept == \"Marketing\"")
	fmt.Fprintln(w, "  not  Logical NOT:      not (email contains \"@test.com\")")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "String/Array:")
	fmt.Fprintln(w, "  contains     Contains:      email contains \"@\"")
	fmt.Fprintln(w, "  startsWith   Starts with:   name startsWith \"A\"")
	fmt.Fprintln(w, "  endsWith     Ends with:     file endsWith \".csv\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Special:")
	fmt.Fprintln(w, "  ? :  Ternary:          age >= 18 ? \"adult\" : \"minor\"")
	fmt.Fprintln(w, "  ??   Nil coalescing:   value ?? \"default\"")
	fmt.Fprintln(w, "  in   Membership:       status in [\"active\", \"pending\"]")
	fmt.Fprintln(w, "  |    Pipe:             name | trim | upper")
	fmt.Fprintln(w, "  ..   Range:            1..5 → [1, 2, 3, 4, 5]")
}

func writeHelpers(w io.Writer) {
	fmt.Fprintln(w, "HELPER FUNCTIONS (ssql-specific):")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  has(field)              Check if field exists")
	fmt.Fprintln(w, "    Example: has(\"email\") → true/false")
	fmt.Fprintln(w, "    Usage:   ssql where -if-expr 'has(\"email\") and email contains \"@\"'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  getOr(field, default)   Get field value or default")
	fmt.Fprintln(w, "    Example: getOr(\"age\", 0) → field value or 0")
	fmt.Fprintln(w, "    Usage:   ssql update -set-expr total 'getOr(\"price\", 0) * getOr(\"qty\", 1)'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Why use these?")
	fmt.Fprintln(w, "  - Prevents errors when fields are missing or sparse")
	fmt.Fprintln(w, "  - Enables expressions to work gracefully with incomplete data")
	fmt.Fprintln(w, "  - Provides sensible defaults for missing values")
}

func printExamples(ctx *cf.Context) error {
	fmt.Fprintln(ctx.Stdout(), "COMMON EXPRESSION PATTERNS:")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Data Validation:")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'has(\"email\") and email contains \"@\"'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'age >= 0 and age <= 120'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Data Cleaning:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr email 'lower(trim(email))'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr status 'getOr(\"status\", \"pending\")'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr clean 'replaceRegex(name, \"[^a-zA-Z ]\", \"\")'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr base 'trimSuffix(filename, \".csv\")'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Calculations:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr total 'price * qty'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr discount 'total > 1000 ? total * 0.1 : 0'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr final 'round((price * qty) * (1 - discount / 100))'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Array Operations:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr avg_score 'mean(scores)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr top3 'take(sort(scores), 3)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'none(scores, {# < 60})'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Complex Filters:")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr 'age >= 18 and age <= 65 and status == \"active\"'")
	fmt.Fprintln(ctx.Stdout(), "  ssql where -if-expr '(age >= 18 and verified) or role == \"admin\"'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "String Manipulation:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr full_name 'first + \" \" + last'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr domain 'split(email, \"@\")[1]'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr slug 'lower(join(split(trim(title), \" \"), \"-\"))'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Encoding:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr encoded 'toBase64(email)'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr payload 'toJSON({\"name\": name, \"age\": age})'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Categorization:")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr category 'age < 18 ? \"minor\" : \"adult\"'")
	fmt.Fprintln(ctx.Stdout(), "  ssql update -set-expr tier 'revenue > 10000 ? \"gold\" : (revenue > 5000 ? \"silver\" : \"bronze\")'")
	fmt.Fprintln(ctx.Stdout())
	fmt.Fprintln(ctx.Stdout(), "Full reference: doc/EXPRESSIONS.md")
	return nil
}

// functionCatalog renders every category's detailed reference — the same
// text `ssql functions -category CAT` prints — as one string.
func functionCatalog() string {
	var b strings.Builder
	for _, write := range []func(io.Writer){
		writeStringFunctions, writeMathFunctions, writeArrayFunctions, writeDateFunctions,
		writeTypeFunctions, writeMapFunctions, writeBitwiseFunctions, writeHashFunctions,
		writeOperators, writeHelpers,
	} {
		write(&b)
		b.WriteString("\n")
	}
	return b.String()
}

// FunctionEntry returns one function's block from the detailed reference —
// the `  name(args)   description` line and the indented Example/Usage
// lines under it — so Alt-h can show the precise help for the function
// under the cursor and `ssql functions NAME` can print it. Entries are
// the lines that start a two-space-indented `name(`; the block ends at
// the next blank line.
func FunctionEntry(name string) (string, bool) {
	lines := strings.Split(functionCatalog(), "\n")
	prefix := "  " + name + "("
	for i, l := range lines {
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "" && strings.HasPrefix(lines[j], "    ") {
			j++
		}
		return strings.Join(lines[i:j], "\n") + "\n", true
	}
	return "", false
}

