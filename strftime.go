package ssql

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// strftime patterns (DFC147 §6). The pattern language users meet in
// DuckDB, Python and DataFusion is strftime's; Go's layout language is
// Go's alone. ssql reads strftime wherever a time pattern is given —
// strftime(ts, "%Y-%m-%d"), strptime(s, "%d/%m/%Y"), -format and
// -time-format flags, a sidecar's date format — and converts it ONCE to a
// Go layout for the hot path. The one rule for a flag that takes either
// language: a pattern containing % is strftime, anything else is a Go
// layout (IsStrftime).

// strftimeDirective is one %x directive: its Go layout token (when it has
// one), its Postgres to_char token, and whether only the slow formatter
// can produce it.
type strftimeDirective struct {
	goLayout string
	pg       string
	slow     bool // no Go layout token: %j %G %V %u (and %f outside "%S.%f")
}

var strftimeDirectives = map[string]strftimeDirective{
	"Y":  {"2006", "YYYY", false},
	"y":  {"06", "YY", false},
	"m":  {"01", "MM", false},
	"-m": {"1", "FMMM", false},
	"d":  {"02", "DD", false},
	"-d": {"2", "FMDD", false},
	"H":  {"15", "HH24", false},
	"I":  {"03", "HH12", false},
	"M":  {"04", "MI", false},
	"S":  {"05", "SS", false},
	"f":  {"000000", "US", false}, // valid only right after %S. — see StrftimeLayout
	"p":  {"PM", "AM", false},
	"b":  {"Jan", "Mon", false},
	"B":  {"January", "FMMonth", false},
	"a":  {"Mon", "Dy", false},
	"A":  {"Monday", "FMDay", false},
	"z":  {"-0700", "", false}, // to_char's OF is "+11", not "+1100": refused there
	"Z":  {"MST", "TZ", false},
	"%":  {"%", "%", false},
	"j":  {"", "DDD", true},
	"G":  {"", "IYYY", true},
	"V":  {"", "IW", true},
	"u":  {"", "ID", true},
}

// strftimeRefused names the directives ssql does not implement, with why,
// so the error can say so by name rather than "unknown".
var strftimeRefused = map[string]string{
	"U": "week of year counting from Sunday (use %V, the ISO week)", "W": "week of year counting from Monday (use %V)",
	"w": "weekday as a number (use %u, ISO, or the dow() function)", "e": "space-padded day (use %-d)",
	"c": "the locale's date and time", "x": "the locale's date", "X": "the locale's time",
	"n": "a newline", "t": "a tab", "s": "Unix seconds (use the epoch() function)",
}

// IsStrftime reports whether a pattern is strftime (contains a %
// directive) rather than a Go layout.
func IsStrftime(pattern string) bool { return strings.Contains(pattern, "%") }

// strftimeSupported lists the directives for messages.
func strftimeSupported() string {
	return "%Y %y %m %-m %d %-d %H %I %M %S %f %p %b %B %a %A %j %z %Z %G %V %u %%"
}

// strftimeTokens splits a pattern into directives (with their leading %)
// and literal runs.
type strftimeToken struct {
	directive string // "Y", "-d", … ; "" for a literal
	literal   string
}

func strftimeTokens(pattern string) ([]strftimeToken, error) {
	var out []strftimeToken
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, strftimeToken{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '%' {
			lit.WriteByte(c)
			continue
		}
		if i+1 >= len(pattern) {
			return nil, fmt.Errorf("pattern %q ends with a bare %%", pattern)
		}
		d := string(pattern[i+1])
		i++
		if d == "-" {
			if i+1 >= len(pattern) {
				return nil, fmt.Errorf("pattern %q ends with a bare %%-", pattern)
			}
			d = "-" + string(pattern[i+1])
			i++
		}
		if _, ok := strftimeDirectives[d]; !ok {
			if why, refused := strftimeRefused[strings.TrimPrefix(d, "-")]; refused {
				return nil, fmt.Errorf("pattern %q: %%%s is not supported — %s; supported: %s", pattern, d, why, strftimeSupported())
			}
			return nil, fmt.Errorf("pattern %q: unknown directive %%%s; supported: %s", pattern, d, strftimeSupported())
		}
		flush()
		out = append(out, strftimeToken{directive: d})
	}
	flush()
	return out, nil
}

// goLayoutLiteralSafe reports whether a literal run can sit in a Go layout
// unchanged: Go reads digits and the letters that start its tokens (Jan,
// Mon, MST, PM/pm, Z, _) as tokens, so a literal with any of them needs
// the slow path.
func goLayoutLiteralSafe(lit string) bool {
	for _, r := range lit {
		if r >= '0' && r <= '9' {
			return false
		}
		switch r {
		case 'J', 'M', 'P', 'p', 'Z', '_':
			return false
		}
	}
	return true
}

// StrftimeLayout converts a strftime pattern to a Go layout. slow=true
// means a directive (or a literal Go would misread) has no layout form,
// and layout is "" — FormatStrftime then builds the text directive by
// directive; parsing such a pattern is refused.
func StrftimeLayout(pattern string) (layout string, slow bool, err error) {
	toks, err := strftimeTokens(pattern)
	if err != nil {
		return "", false, err
	}
	var b strings.Builder
	for _, t := range toks {
		if t.directive == "" {
			if !goLayoutLiteralSafe(t.literal) {
				return "", true, nil
			}
			b.WriteString(t.literal)
			continue
		}
		d := strftimeDirectives[t.directive]
		if d.slow {
			return "", true, nil
		}
		if t.directive == "f" && !strings.HasSuffix(b.String(), "05.") {
			return "", true, nil // fractional seconds stand alone only on the slow path
		}
		b.WriteString(d.goLayout)
	}
	return b.String(), false, nil
}

// strftimePlan is a converted pattern, cached per pattern text.
type strftimePlan struct {
	layout string
	slow   bool
	toks   []strftimeToken
}

var strftimePlans sync.Map // pattern → *strftimePlan

func strftimePlanFor(pattern string) (*strftimePlan, error) {
	if p, ok := strftimePlans.Load(pattern); ok {
		return p.(*strftimePlan), nil
	}
	layout, slow, err := StrftimeLayout(pattern)
	if err != nil {
		return nil, err
	}
	toks, _ := strftimeTokens(pattern)
	p := &strftimePlan{layout: layout, slow: slow, toks: toks}
	strftimePlans.Store(pattern, p)
	return p, nil
}

// FormatStrftime renders t (in its own location — callers pass t.In(loc))
// with a strftime pattern.
func FormatStrftime(t time.Time, pattern string) (string, error) {
	p, err := strftimePlanFor(pattern)
	if err != nil {
		return "", err
	}
	if !p.slow {
		return t.Format(p.layout), nil
	}
	var b strings.Builder
	for _, tok := range p.toks {
		if tok.directive == "" {
			b.WriteString(tok.literal)
			continue
		}
		switch tok.directive {
		case "j":
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case "G":
			y, _ := t.ISOWeek()
			fmt.Fprintf(&b, "%d", y)
		case "V":
			_, w := t.ISOWeek()
			fmt.Fprintf(&b, "%02d", w)
		case "u":
			wd := int(t.Weekday())
			if wd == 0 {
				wd = 7
			}
			b.WriteString(strconv.Itoa(wd))
		case "f":
			fmt.Fprintf(&b, "%06d", t.Nanosecond()/1000)
		default:
			b.WriteString(t.Format(strftimeDirectives[tok.directive].goLayout))
		}
	}
	return b.String(), nil
}

// MustFormatStrftime is FormatStrftime for generated code (the pattern was
// validated at generation time), reading t in loc.
func MustFormatStrftime(t time.Time, pattern string, loc *time.Location) string {
	s, err := FormatStrftime(t.In(loc), pattern)
	if err != nil {
		panic(err)
	}
	return s
}

// TimeLayout is the Go layout for a pattern in either language: strftime
// when it contains %, else the pattern as a Go layout. Patterns with no
// Go form (%j, %G, %V, %u, a bare %f) are refused — they can be formatted
// but not parsed.
func TimeLayout(pattern string) (string, error) {
	if !IsStrftime(pattern) {
		return pattern, nil
	}
	layout, slow, err := StrftimeLayout(pattern)
	if err != nil {
		return "", err
	}
	if slow {
		return "", fmt.Errorf("pattern %q cannot be parsed (%%j, %%G, %%V, %%u and a %%f away from %%S. format only)", pattern)
	}
	return layout, nil
}

// ParseStrftime parses s with a pattern in either language. Text with no
// zone of its own is a wall clock in the PIPELINE zone (StreamLocation):
// a `05/01/2026 09:15` someone wrote by hand is local time, where an ISO
// `2026-01-05T09:15:00` from a database export is UTC (ParseTime). A
// pattern with %z or %Z keeps the text's offset.
func ParseStrftime(s, pattern string) (time.Time, error) {
	layout, err := TimeLayout(pattern)
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.ParseInLocation(layout, s, StreamLocation())
	if err != nil {
		return time.Time{}, fmt.Errorf("%q does not match %q: %v", s, pattern, err)
	}
	return t, nil
}

// MustParseTimeLayout is ParseStrftime for `cast -format` in every lane:
// a value that does not match stops the pipeline (as MustParseTime does).
func MustParseTimeLayout(val any, pattern, field string) time.Time {
	if t, ok := val.(time.Time); ok {
		return t
	}
	s, ok := val.(string)
	if !ok {
		s = fmt.Sprint(val)
	}
	t, err := ParseStrftime(s, pattern)
	if err != nil {
		panic(&CastError{Field: field, Value: val, Target: FieldTypeTime})
	}
	return t
}

// PgToChar renders a strftime pattern as a Postgres to_char pattern:
// literal runs are double-quoted (to_char reads bare letters as tokens).
// %z has no faithful to_char form (OF is "+11", not "+1100") and a
// literal containing a double quote cannot be quoted: both are errors.
func PgToChar(pattern string) (string, error) {
	toks, err := strftimeTokens(pattern)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, t := range toks {
		if t.directive == "" {
			if strings.Contains(t.literal, `"`) {
				return "", fmt.Errorf("pattern %q: a literal double quote has no to_char form", pattern)
			}
			b.WriteString(`"` + t.literal + `"`)
			continue
		}
		d := strftimeDirectives[t.directive]
		if d.pg == "" {
			return "", fmt.Errorf("pattern %q: %%%s has no Postgres to_char form", pattern, t.directive)
		}
		if t.directive == "%" {
			b.WriteString(`"%"`)
			continue
		}
		b.WriteString(d.pg)
	}
	return b.String(), nil
}

// UTS35ToStrftime converts a Unicode (CLDR / CSVW / XSD) date pattern —
// yyyy-MM-dd, dd/MM/yyyy HH:mm:ss — to strftime. Letters are pattern
// fields (counted runs), text in single quotes is literal, anything else
// is literal. Fields with no strftime form are errors by name.
func UTS35ToStrftime(pattern string) (string, error) {
	var b strings.Builder
	rs := []rune(pattern)
	for i := 0; i < len(rs); {
		r := rs[i]
		if r == '\'' {
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j >= len(rs) {
				return "", fmt.Errorf("pattern %q: unclosed quote", pattern)
			}
			if j == i+1 {
				b.WriteString("'") // '' is a literal quote
			} else {
				b.WriteString(strings.ReplaceAll(string(rs[i+1:j]), "%", "%%"))
			}
			i = j + 1
			continue
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			if r == '%' {
				b.WriteString("%%")
			} else {
				b.WriteRune(r)
			}
			i++
			continue
		}
		j := i
		for j < len(rs) && rs[j] == r {
			j++
		}
		n := j - i
		var d string
		switch r {
		case 'y', 'u':
			d = map[bool]string{true: "%y", false: "%Y"}[n == 2]
		case 'M', 'L':
			d = map[int]string{1: "%-m", 2: "%m", 3: "%b"}[n]
			if n >= 4 {
				d = "%B"
			}
		case 'd':
			d = map[bool]string{true: "%-d", false: "%d"}[n == 1]
		case 'D':
			d = "%j"
		case 'H':
			d = "%H"
		case 'h':
			d = "%I"
		case 'm':
			d = "%M"
		case 's':
			d = "%S"
		case 'S':
			d = "%f"
		case 'a':
			d = "%p"
		case 'E':
			d = map[bool]string{true: "%A", false: "%a"}[n >= 4]
		case 'Z', 'X', 'x':
			d = "%z"
		case 'z', 'v', 'V':
			d = "%Z"
		case 'T':
			b.WriteString("T") // a bare T separator in some XSD patterns
			i = j
			continue
		}
		if d == "" {
			return "", fmt.Errorf("pattern %q: the field %q has no strftime form", pattern, string(rs[i:j]))
		}
		b.WriteString(d)
		i = j
	}
	return b.String(), nil
}

// FormatTimePattern renders an instant for a sink: with a pattern (either
// language), on the pipeline zone's wall clock; with none, RFC 3339 Nano
// as every sink has always printed it. Sinks' -time-format (DFC147 §7.3).
func FormatTimePattern(t time.Time, pattern string) string {
	if pattern == "" {
		return t.Format(time.RFC3339Nano)
	}
	lt := t.In(StreamLocation())
	if IsStrftime(pattern) {
		s, err := FormatStrftime(lt, pattern)
		if err != nil {
			return lt.Format(time.RFC3339Nano) // the command validated the pattern; this is unreachable
		}
		return s
	}
	return lt.Format(pattern)
}

// ValidateTimePattern checks a -time-format value once: a strftime
// pattern must use supported directives; a Go layout is taken as given.
func ValidateTimePattern(pattern string) error {
	if !IsStrftime(pattern) {
		return nil
	}
	_, _, err := StrftimeLayout(pattern)
	return err
}
