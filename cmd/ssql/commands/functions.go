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

Math Functions (6):
  round(num), floor(num), ceil(num), abs(num), min(a, b), max(a, b)

Array Functions (26):
  len(arr), first(arr), last(arr), take(arr, n), reverse(arr),
  sort(arr), sortBy(arr, pred), uniq(arr), flatten(arr), concat(a, b),
  filter(arr, pred), map(arr, fn), reduce(arr, pred, init),
  find(arr, pred), findIndex(arr, pred), findLast(arr, pred),
  findLastIndex(arr, pred), groupBy(arr, pred),
  all(arr, pred), any(arr, pred), one(arr, pred), none(arr, pred),
  count(arr), sum(arr), mean(arr), median(arr)

Date Functions (5):
  now(), date(str), duration(str), timezone(str), bucket(ts, dur)

Type & Encoding (10):
  int(v), float(v), string(v), type(v),
  toJSON(v), fromJSON(str), toBase64(str), fromBase64(str),
  toPairs(map), fromPairs(arr)

Map Functions (3):
  keys(map), values(map), get(v, key)

Bitwise Functions (8):
  bitand(a, b), bitor(a, b), bitxor(a, b), bitnand(a, b),
  bitnot(a), bitshl(a, n), bitshr(a, n), bitushr(a, n)

Hash Functions (3):
  sha256(str), sha1(str), md5(str)

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
	fmt.Fprintln(w, "DATE FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  now()                   Current date/time")
	fmt.Fprintln(w, "    Example: now() → 2026-02-25T10:30:00+11:00")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  date(str)               Parse date string")
	fmt.Fprintln(w, "    Example: date(\"2026-01-15\")")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  duration(str)           Parse duration string")
	fmt.Fprintln(w, "    Example: duration(\"1h30m\") → 1h30m0s")
	fmt.Fprintln(w, "    Supports: ns, us, ms, s, m, h")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  timezone(str)           Get timezone location")
	fmt.Fprintln(w, "    Example: timezone(\"America/New_York\")")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  bucket(ts, dur)         Snap a timestamp down to an epoch-aligned bucket of width dur")
	fmt.Fprintln(w, "    Example: bucket(ts, \"1m\") → 1699999980   (int/float epochs: unit read from magnitude; RFC 3339 strings too)")
	fmt.Fprintln(w, "    Usage:   ssql update -set-expr minute 'bucket(ts, \"1m\")' | ssql group-by minute -avg temp t")
	fmt.Fprintln(w, "             (flag form: ssql update -set-bucket minute ts 1m)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common Usage:")
	fmt.Fprintln(w, "  ssql update -set-expr timestamp 'string(now())'")
	fmt.Fprintln(w, "  ssql where -if-expr 'date(created) > date(\"2026-01-01\")'")
}

func writeTypeFunctions(w io.Writer) {
	fmt.Fprintln(w, "TYPE & ENCODING FUNCTIONS:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Type Conversion:")
	fmt.Fprintln(w, "  int(value)              Convert to integer")
	fmt.Fprintln(w, "    Example: int(\"123\") → 123")
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
	fmt.Fprintln(w, "  ssql update -set-expr email_hash 'sha256(email)'")
	fmt.Fprintln(w, "  ssql update -set-expr id 'sha256(name + \":\" + email)'")
	fmt.Fprintln(w, "  ssql update -set-expr checksum 'md5(content)'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Note: For security-sensitive applications, prefer sha256 over md5/sha1.")
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

