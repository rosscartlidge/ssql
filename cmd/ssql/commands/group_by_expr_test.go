package commands

import (
	"strings"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

func TestEvalBatchExpr(t *testing.T) {
	// Create test records
	records := []ssql.Record{
		ssql.MakeMutableRecord().
			String("name", "Alice").
			String("dept", "Eng").
			Float("salary", 80000).
			Float("bonus", 0.1).
			Freeze(),
		ssql.MakeMutableRecord().
			String("name", "Bob").
			String("dept", "Eng").
			Float("salary", 90000).
			Float("bonus", 0.15).
			Freeze(),
		ssql.MakeMutableRecord().
			String("name", "Carol").
			String("dept", "Eng").
			Float("salary", 70000).
			Float("bonus", 0.2).
			Freeze(),
	}

	tests := []struct {
		name       string
		expression string
		expected   float64
		tolerance  float64
	}{
		{
			name:       "simple sum",
			expression: "sum(salary)",
			expected:   240000.0,
			tolerance:  0.01,
		},
		{
			name:       "sum with multiplication",
			expression: "sum(salary * bonus)",
			expected:   80000*0.1 + 90000*0.15 + 70000*0.2, // 8000 + 13500 + 14000 = 35500
			tolerance:  0.01,
		},
		{
			name:       "avg",
			expression: "avg(salary)",
			expected:   80000.0,
			tolerance:  0.01,
		},
		{
			name:       "count",
			expression: "count()",
			expected:   3.0,
			tolerance:  0.01,
		},
		{
			name:       "sum divided by count",
			expression: "sum(salary) / count()",
			expected:   80000.0,
			tolerance:  0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvalBatchExpr(tt.expression, records)
			if err != nil {
				t.Fatalf("EvalBatchExpr(%q) error: %v", tt.expression, err)
			}

			var resultFloat float64
			switch v := result.(type) {
			case float64:
				resultFloat = v
			case int:
				resultFloat = float64(v)
			case int64:
				resultFloat = float64(v)
			default:
				t.Fatalf("unexpected result type: %T", result)
			}

			if abs(resultFloat-tt.expected) > tt.tolerance {
				t.Errorf("EvalBatchExpr(%q) = %v, want %v", tt.expression, resultFloat, tt.expected)
			}
		})
	}
}

func TestEvalStreamExpr(t *testing.T) {
	// Create test records
	records := []ssql.Record{
		ssql.MakeMutableRecord().
			String("name", "Alice").
			Float("salary", 80000).
			Freeze(),
		ssql.MakeMutableRecord().
			String("name", "Bob").
			Float("salary", 90000).
			Freeze(),
		ssql.MakeMutableRecord().
			String("name", "Carol").
			Float("salary", 70000).
			Freeze(),
	}

	tests := []struct {
		name      string
		spec      StreamExprSpec
		expected  float64
		tolerance float64
	}{
		{
			name: "sum",
			spec: StreamExprSpec{
				InitExpr:  "{s: 0}",
				EveryExpr: "{s: s + salary}",
				FinalExpr: "s",
				Result:    "total",
			},
			expected:  240000.0,
			tolerance: 0.01,
		},
		{
			name: "average",
			spec: StreamExprSpec{
				InitExpr:  "{s: 0, n: 0}",
				EveryExpr: "{s: s + salary, n: n + 1}",
				FinalExpr: "s / n",
				Result:    "avg",
			},
			expected:  80000.0,
			tolerance: 0.01,
		},
		{
			name: "count",
			spec: StreamExprSpec{
				InitExpr:  "{n: 0}",
				EveryExpr: "{n: n + 1}",
				FinalExpr: "n",
				Result:    "count",
			},
			expected:  3.0,
			tolerance: 0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvalStreamExpr(tt.spec, records)
			if err != nil {
				t.Fatalf("EvalStreamExpr() error: %v", err)
			}

			var resultFloat float64
			switch v := result.(type) {
			case float64:
				resultFloat = v
			case int:
				resultFloat = float64(v)
			case int64:
				resultFloat = float64(v)
			default:
				t.Fatalf("unexpected result type: %T", result)
			}

			if abs(resultFloat-tt.expected) > tt.tolerance {
				t.Errorf("EvalStreamExpr() = %v, want %v", resultFloat, tt.expected)
			}
		})
	}
}

func TestAggPatcher(t *testing.T) {
	// This tests that the AggPatcher correctly transforms expressions
	records := []ssql.Record{
		ssql.MakeMutableRecord().
			Float("price", 100).
			Float("qty", 2).
			Freeze(),
		ssql.MakeMutableRecord().
			Float("price", 200).
			Float("qty", 3).
			Freeze(),
	}

	// Test complex expression with multiple field access
	result, err := EvalBatchExpr("sum(price * qty)", records)
	if err != nil {
		t.Fatalf("EvalBatchExpr error: %v", err)
	}

	expected := 100.0*2 + 200.0*3 // 800
	if resultFloat, ok := result.(float64); ok {
		if abs(resultFloat-expected) > 0.01 {
			t.Errorf("got %v, want %v", resultFloat, expected)
		}
	} else {
		t.Errorf("unexpected result type: %T", result)
	}
}

func TestAggPatcherWithStrings(t *testing.T) {
	// Test that non-field identifiers (like functions) are not transformed
	records := []ssql.Record{
		ssql.MakeMutableRecord().
			Float("value", 10).
			Freeze(),
		ssql.MakeMutableRecord().
			Float("value", 20).
			Freeze(),
	}

	// sum(value) should transform value → #.value
	result, err := EvalBatchExpr("sum(value)", records)
	if err != nil {
		t.Fatalf("EvalBatchExpr error: %v", err)
	}

	if resultFloat, ok := result.(float64); ok {
		if resultFloat != 30.0 {
			t.Errorf("got %v, want 30", resultFloat)
		}
	} else {
		t.Errorf("unexpected result type: %T", result)
	}
}

func TestCombinedExpressions(t *testing.T) {
	records := []ssql.Record{
		ssql.MakeMutableRecord().
			Float("a", 10).
			Float("b", 2).
			Freeze(),
		ssql.MakeMutableRecord().
			Float("a", 20).
			Float("b", 3).
			Freeze(),
	}

	tests := []struct {
		expr     string
		expected float64
	}{
		{"sum(a) + sum(b)", 35.0},     // 30 + 5
		{"sum(a) * 2", 60.0},          // 30 * 2
		{"avg(a) + avg(b)", 17.5},     // 15 + 2.5
		{"sum(a) / count()", 15.0},    // 30 / 2
		{"sum(a * b) / count()", 40.0}, // 80 / 2
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := EvalBatchExpr(tt.expr, records)
			if err != nil {
				t.Fatalf("EvalBatchExpr(%q) error: %v", tt.expr, err)
			}

			resultFloat, ok := result.(float64)
			if !ok {
				t.Fatalf("unexpected result type: %T", result)
			}

			if abs(resultFloat-tt.expected) > 0.01 {
				t.Errorf("EvalBatchExpr(%q) = %v, want %v", tt.expr, resultFloat, tt.expected)
			}
		})
	}
}

func TestEvalStreamExprErrors(t *testing.T) {
	// Test error cases
	t.Run("empty records", func(t *testing.T) {
		_, err := EvalStreamExpr(StreamExprSpec{
			InitExpr:  "{s: 0}",
			EveryExpr: "{s: s + salary}",
			FinalExpr: "s",
		}, []ssql.Record{})
		if err == nil {
			t.Error("expected error for empty records")
		}
		if !strings.Contains(err.Error(), "no records") {
			t.Errorf("expected 'no records' error, got: %v", err)
		}
	})

	t.Run("invalid init expression", func(t *testing.T) {
		records := []ssql.Record{
			ssql.MakeMutableRecord().Float("x", 1).Freeze(),
		}
		_, err := EvalStreamExpr(StreamExprSpec{
			InitExpr:  "{s: }",  // Invalid syntax
			EveryExpr: "{s: s + x}",
			FinalExpr: "s",
		}, records)
		if err == nil {
			t.Error("expected error for invalid init expression")
		}
	})
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
