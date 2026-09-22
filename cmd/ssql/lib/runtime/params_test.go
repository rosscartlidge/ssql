package runtime

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

func TestCompileExprParams(t *testing.T) {
	rec := func(kv ...any) ssql.Record {
		m := ssql.MakeMutableRecord()
		for i := 0; i < len(kv); i += 2 {
			switch v := kv[i+1].(type) {
			case int64:
				m = m.Int(kv[i].(string), v)
			case float64:
				m = m.Float(kv[i].(string), v)
			case string:
				m = m.String(kv[i].(string), v)
			}
		}
		return m.Freeze()
	}

	t.Run("a parameter is a variable, its value is never syntax", func(t *testing.T) {
		attack := `x" || true || "`
		eval, err := CompileExprParams(`name == who`, StaticParams(map[string]any{"who": attack}))
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]bool{"bob": false, attack: true} {
			got, err := eval(rec("name", name))
			if err != nil || got != want {
				t.Errorf("name=%q: got %v, %v; want %v", name, got, err, want)
			}
		}
	})

	t.Run("typed value participates in arithmetic", func(t *testing.T) {
		eval, err := CompileExprParams(`price * rate`, StaticParams(map[string]any{"rate": 1.5}))
		if err != nil {
			t.Fatal(err)
		}
		got, err := eval(rec("price", int64(4)))
		if err != nil || got != 6.0 {
			t.Errorf("got %v, %v; want 6", got, err)
		}
	})

	t.Run("a parameter named like a field is an error", func(t *testing.T) {
		eval, err := CompileExprParams(`rate > 1`, StaticParams(map[string]any{"rate": 2.0}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = eval(rec("rate", 0.5))
		if err == nil || !strings.Contains(err.Error(), `parameter rate has the same name as a field`) {
			t.Errorf("want a collision error, got %v", err)
		}
	})

	t.Run("the thunk is read once, on first evaluation", func(t *testing.T) {
		calls := 0
		eval, _ := CompileExprParams(`n + k`, func() map[string]any { calls++; return map[string]any{"k": int64(10)} })
		if calls != 0 {
			t.Fatalf("thunk called at compile time")
		}
		for i := 0; i < 3; i++ {
			// expr-lang normalises integer arithmetic to int; compare by text.
			if got, _ := eval(rec("n", int64(i))); fmt.Sprint(got) != fmt.Sprint(i+10) {
				t.Errorf("row %d: got %v", i, got)
			}
		}
		if calls != 1 {
			t.Errorf("thunk called %d times, want 1", calls)
		}
	})

	t.Run("env form", func(t *testing.T) {
		filter := MustCompileExprFilterEnvParams(`n > lo`, StaticParams(map[string]any{"lo": int64(5)}))
		if !filter(map[string]any{"n": int64(6)}) || filter(map[string]any{"n": int64(5)}) {
			t.Error("env filter wrong")
		}
		eval := MustCompileExprEnvParams(`n > lo`, StaticParams(map[string]any{"lo": int64(5)}))
		if _, err := eval(map[string]any{"n": int64(6), "lo": int64(1)}); err == nil {
			t.Error("env form must report a field collision")
		}
	})
}

func TestExprIdentifiers(t *testing.T) {
	ids, err := ExprIdentifiers(`price * rate > lo && upper(name) == who && len(name) > 2 && rate > 0`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"price", "rate", "lo", "name", "who"}; strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", ids, want)
	}
	if _, err := ExprIdentifiers(`a +`); err == nil {
		t.Error("want a parse error")
	}
}

// -param-field NAME TYPE FIELD: bound per row, cast to TYPE, absent stays
// absent, a value not of TYPE is loud, collisions are errors.
func TestCompileExprParamsFields(t *testing.T) {
	rec := func(f map[string]any) ssql.Record { return ssql.NewRecord(f) }
	fields := FieldParams{"n": {Field: "1st", Type: ssql.FieldTypeInt}, "rate": {Field: "disc", Type: ssql.FieldTypeFloat}}
	eval, err := CompileExprParamsFields(`price * rate > n`, StaticParams(nil), fields)
	if err != nil {
		t.Fatal(err)
	}
	got, err := eval(rec(map[string]any{"price": int64(10), "disc": "1.5", "1st": int64(12)}))
	if err != nil || got != true {
		t.Errorf("cast view: %v %v", got, err)
	}
	// Absent field: the expression has no value (ErrAbsent), so a filter
	// is false and a set assigns nothing; never a comparison with nil.
	if _, err := eval(rec(map[string]any{"price": int64(10), "disc": 1.5})); !errors.Is(err, ErrAbsent) {
		t.Errorf("absent field parameter: want ErrAbsent, got %v", err)
	}
	// Not castable: loud.
	_, err = eval(rec(map[string]any{"price": int64(10), "disc": "cheap", "1st": int64(1)}))
	var ce *ssql.CastError
	if !errors.As(err, &ce) {
		t.Errorf("want CastError, got %v", err)
	}
	// Collision.
	eval, _ = CompileExprParamsFields(`n > 0`, nil, FieldParams{"n": {Field: "m", Type: ssql.FieldTypeInt}})
	_, err = eval(rec(map[string]any{"n": int64(1), "m": int64(2)}))
	var pc *ParamCollisionError
	if !errors.As(err, &pc) {
		t.Errorf("want collision, got %v", err)
	}
	// Env form.
	f := MustCompileExprFilterEnvParamsFields(`n > lo`, StaticParams(map[string]any{"lo": int64(5)}), FieldParams{"n": {Field: "c", Type: ssql.FieldTypeInt}})
	if !f(map[string]any{"c": "6"}) || f(map[string]any{"c": "5"}) || f(map[string]any{}) {
		t.Error("env form wrong")
	}
}
