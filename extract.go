package ssql

import (
	"fmt"
	"iter"
	"os"
	"regexp"
)

// ExtractConfig describes `ssql extract`: apply Pattern (Go RE2) to
// Field; every NAMED capture group becomes a string field. A record
// whose field doesn't match is an error (loud) unless Skip drops it.
// The source field is removed unless Keep.
type ExtractConfig struct {
	Field   string
	Pattern string
	Skip    bool
	Keep    bool
}

// CompileExtract validates the config: the pattern must compile and
// have at least one named group.
func CompileExtract(cfg ExtractConfig) (*regexp.Regexp, []string, error) {
	re, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("extract: bad regex %q: %w", cfg.Pattern, err)
	}
	var names []string
	for _, n := range re.SubexpNames() {
		if n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("extract: regex %q has no named groups — use (?P<name>...) so captures become fields", cfg.Pattern)
	}
	return re, names, nil
}

// ExtractRecords turns text into fields with a regex. Captures are
// strings (cast afterwards: `cast -int status`); a missing source field
// counts as a non-match. Row-local and order-preserving.
func ExtractRecords(records iter.Seq[Record], cfg ExtractConfig) (iter.Seq[Record], error) {
	re, names, err := CompileExtract(cfg)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, n := range re.SubexpNames() {
		if n != "" {
			idx[n] = i
		}
	}
	return func(yield func(Record) bool) {
		var lineNo int64
		for r := range records {
			lineNo++
			text, ok := Get[string](r, cfg.Field)
			var m []string
			if ok {
				m = re.FindStringSubmatch(text)
			}
			if m == nil {
				if cfg.Skip {
					continue
				}
				fmt.Fprintf(os.Stderr, "extract: record %d: %q does not match %q (use -skip to drop non-matching records)\n", lineNo, text, cfg.Pattern)
				os.Exit(1)
			}
			mr := r.ToMutable()
			if !cfg.Keep {
				mr = mr.Delete(cfg.Field)
			}
			for _, n := range names {
				mr = mr.String(n, m[idx[n]])
			}
			if !yield(mr.Freeze()) {
				return
			}
		}
	}, nil
}

// ExtractFilter is the filter-shaped form for generated code; config
// errors are fatal (the assembler's contract for stmt fragments).
func ExtractFilter(cfg ExtractConfig) Filter[Record, Record] {
	return func(in iter.Seq[Record]) iter.Seq[Record] {
		out, err := ExtractRecords(in, cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return out
	}
}
