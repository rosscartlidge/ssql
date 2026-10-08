package runtime

import (
	"fmt"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

func TestReplaceRegex(t *testing.T) {
	tests := []struct {
		name        string
		expression  string
		record      ssql.Record
		expected    string
	}{
		{
			name:       "simple replacement",
			expression: `replaceRegex(name, "[^a-zA-Z]", "_")`,
			record:     ssql.MakeMutableRecord().String("name", "John Smith-Jones").Freeze(),
			expected:   "John_Smith_Jones",
		},
		{
			name:       "strip non-digits",
			expression: `replaceRegex(code, "[^0-9]", "")`,
			record:     ssql.MakeMutableRecord().String("code", "ABC 123").Freeze(),
			expected:   "123",
		},
		{
			name:       "capture group expansion",
			expression: `replaceRegex(phone, "(\\d{3})(\\d{3})(\\d{4})", "($1) $2-$3")`,
			record:     ssql.MakeMutableRecord().String("phone", "1234567890").Freeze(),
			expected:   "(123) 456-7890",
		},
		{
			name:       "named capture group",
			expression: `replaceRegex(s, "(?P<first>\\w+) (?P<last>\\w+)", "${last}, ${first}")`,
			record:     ssql.MakeMutableRecord().String("s", "John Smith").Freeze(),
			expected:   "Smith, John",
		},
		{
			name:       "no match returns original",
			expression: `replaceRegex(s, "xyz", "abc")`,
			record:     ssql.MakeMutableRecord().String("s", "hello world").Freeze(),
			expected:   "hello world",
		},
		{
			name:       "invalid regex returns original",
			expression: `replaceRegex(s, "[invalid", "x")`,
			record:     ssql.MakeMutableRecord().String("s", "hello").Freeze(),
			expected:   "hello",
		},
		{
			name:       "empty string",
			expression: `replaceRegex(s, ".", "x")`,
			record:     ssql.MakeMutableRecord().String("s", "").Freeze(),
			expected:   "",
		},
		{
			name:       "replace all occurrences",
			expression: `replaceRegex(s, "\\s+", " ")`,
			record:     ssql.MakeMutableRecord().String("s", "hello   world   foo").Freeze(),
			expected:   "hello world foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eval, err := CompileExpr(tt.expression)
			if err != nil {
				t.Fatalf("CompileExpr(%q): %v", tt.expression, err)
			}
			result, err := eval(tt.record)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			got, ok := result.(string)
			if !ok {
				t.Fatalf("expected string, got %T: %v", result, result)
			}
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestCustomFunctions(t *testing.T) {
	record := ssql.MakeMutableRecord().
		String("name", "Alice").
		String("email", "alice@example.com").
		Freeze()

	t.Run("has existing field", func(t *testing.T) {
		eval, err := CompileExpr(`has("name")`)
		if err != nil {
			t.Fatal(err)
		}
		result, _ := eval(record)
		if result != true {
			t.Errorf("got %v, want true", result)
		}
	})

	t.Run("has missing field", func(t *testing.T) {
		eval, err := CompileExpr(`has("missing")`)
		if err != nil {
			t.Fatal(err)
		}
		result, _ := eval(record)
		if result != false {
			t.Errorf("got %v, want false", result)
		}
	})

	t.Run("getOr existing", func(t *testing.T) {
		eval, err := CompileExpr(`getOr("name", "default")`)
		if err != nil {
			t.Fatal(err)
		}
		result, _ := eval(record)
		if result != "Alice" {
			t.Errorf("got %v, want Alice", result)
		}
	})

	t.Run("getOr missing", func(t *testing.T) {
		eval, err := CompileExpr(`getOr("missing", "default")`)
		if err != nil {
			t.Fatal(err)
		}
		result, _ := eval(record)
		if result != "default" {
			t.Errorf("got %v, want default", result)
		}
	})

	t.Run("sha256", func(t *testing.T) {
		eval, err := CompileExpr(`sha256("hello")`)
		if err != nil {
			t.Fatal(err)
		}
		result, _ := eval(record)
		got := result.(string)
		if len(got) != 64 {
			t.Errorf("sha256 hash length = %d, want 64", len(got))
		}
	})
}

// DFC144 Level 1: an expression sees a nested value as the list or map it
// is. Before 2026-10-08 len(tags) on ["go","rust"] was 13 (the text's
// length) and tags[0] was 91 (the byte '[').
func TestCompileExprOpensNestedValues(t *testing.T) {
	rec := ssql.MakeMutableRecord().
		String("name", "Alice").
		JSONString("tags", ssql.JSONString(`["go","rust"]`)).
		JSONString("addr", ssql.JSONString(`{"city":"NYC","zip":"10001"}`)).
		JSONString("scores", ssql.JSONString(`[10,20,30]`)).
		Freeze()
	cases := map[string]any{
		`len(tags)`:               2,
		`tags[0]`:                 "go",
		`addr.city`:               "NYC",
		`"go" in tags`:            true,
		`sort(scores)[-1]`:        int64(30),
		`len(getOr("tags", []))`:  2,
		`len(name)`:               5,
	}
	for expr, want := range cases {
		eval, err := CompileExpr(expr)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		got, err := eval(rec)
		if err != nil {
			t.Fatalf("eval %q: %v", expr, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v (%T), want %v", expr, got, got, want)
		}
	}
}

// After `flatten items`, the columns are named items.qty, items.price;
// an expression reaches them as items.qty (DFC144 Level 2).
func TestCompileExprReachesDottedColumns(t *testing.T) {
	rec := ssql.MakeMutableRecord().
		Int("items.qty", 3).
		Float("items.price", 1.5).
		String("items.sku", "PEN").
		String("other", "x").
		Freeze()
	eval, err := CompileExpr(`items.qty * items.price`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := eval(rec)
	if err != nil || fmt.Sprint(got) != "4.5" {
		t.Fatalf("items.qty * items.price = %v, %v", got, err)
	}
	// a real field named like the head wins
	rec2 := ssql.MakeMutableRecord().String("a", "real").String("a.b", "col").Freeze()
	eval2, _ := CompileExpr(`a`)
	if got, _ := eval2(rec2); got != "real" {
		t.Errorf("a real field must win over synthesis, got %v", got)
	}
}
