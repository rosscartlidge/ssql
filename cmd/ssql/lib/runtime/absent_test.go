package runtime

import (
	"errors"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

// IsAbsentValueError knows expr-lang's wording for a nil operand; this
// pins it against the real VM, so an upgrade that changes the message
// fails here rather than turning every null comparison into a fatal
// error (or every real error into a quiet non-match).
func TestIsAbsentValueError(t *testing.T) {
	rec := func(fields map[string]any) ssql.Record {
		m := ssql.MakeMutableRecord()
		for k, v := range fields {
			if v == nil {
				m = m.Null(k)
			} else {
				m = m.String(k, v.(string))
			}
		}
		return m.Freeze()
	}
	eval := func(expression string, fields map[string]any) error {
		f, err := CompileExpr(expression)
		if err != nil {
			t.Fatalf("compile %q: %v", expression, err)
		}
		_, err = f(rec(fields))
		return err
	}

	// A null operand in a comparison or arithmetic: absent.
	for _, e := range []string{`x > 5`, `x + 1`, `date(x) > date("2023-01-01")`} {
		err := eval(e, map[string]any{"x": nil})
		if err == nil {
			t.Errorf("%s on a null: expected an error from the VM, got none (the helper has nothing to classify)", e)
			continue
		}
		if !IsAbsentValueError(err) {
			t.Errorf("%s on a null: %v should be an absent-value error", e, err)
		}
	}
	if !IsAbsentValueError(ErrAbsent) {
		t.Error("ErrAbsent itself")
	}

	// A value that is present but wrong is the run's error.
	for _, e := range []string{`date(x) > date("2023-01-01")`, `x + 1`} {
		err := eval(e, map[string]any{"x": "nope"})
		if err == nil {
			t.Errorf("%s on %q: expected an error", e, "nope")
			continue
		}
		if IsAbsentValueError(err) {
			t.Errorf("%s on %q: %v must NOT be an absent-value error", e, "nope", err)
		}
	}
	if IsAbsentValueError(errors.New("invalid operation: <nil> in a plain error")) {
		t.Error("a plain error is never classified by its text")
	}
}
