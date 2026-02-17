//go:build !(js && wasm)

package main

import (
	"math"
	"testing"
)

func TestParseExprSimpleNumber(t *testing.T) {
	node, err := parseExpr("42")
	if err != nil {
		t.Fatal(err)
	}
	val, err := node.eval(record{s: newSchema(nil), values: nil})
	if err != nil {
		t.Fatal(err)
	}
	if val != 42 {
		t.Errorf("expected 42, got %v", val)
	}
}

func TestParseExprDecimal(t *testing.T) {
	node, err := parseExpr("3.14")
	if err != nil {
		t.Fatal(err)
	}
	val, err := node.eval(record{s: newSchema(nil), values: nil})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(val-3.14) > 1e-9 {
		t.Errorf("expected 3.14, got %v", val)
	}
}

func TestParseExprAddition(t *testing.T) {
	node, err := parseExpr("2 + 3")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := node.eval(record{s: newSchema(nil), values: nil})
	if val != 5 {
		t.Errorf("expected 5, got %v", val)
	}
}

func TestParseExprPrecedence(t *testing.T) {
	node, err := parseExpr("2 + 3 * 4")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := node.eval(record{s: newSchema(nil), values: nil})
	if val != 14 {
		t.Errorf("expected 14 (2 + 3*4), got %v", val)
	}
}

func TestParseExprParentheses(t *testing.T) {
	node, err := parseExpr("(2 + 3) * 4")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := node.eval(record{s: newSchema(nil), values: nil})
	if val != 20 {
		t.Errorf("expected 20, got %v", val)
	}
}

func TestParseExprFieldReference(t *testing.T) {
	s := newSchema([]string{"salary"})
	r := record{s: s, values: []any{float64(50000)}}

	node, err := parseExpr("salary * 12")
	if err != nil {
		t.Fatal(err)
	}
	val, err := node.eval(r)
	if err != nil {
		t.Fatal(err)
	}
	if val != 600000 {
		t.Errorf("expected 600000, got %v", val)
	}
}

func TestParseExprMultipleFields(t *testing.T) {
	s := newSchema([]string{"revenue", "cost"})
	r := record{s: s, values: []any{float64(10000), float64(7000)}}

	node, err := parseExpr("revenue - cost")
	if err != nil {
		t.Fatal(err)
	}
	val, err := node.eval(r)
	if err != nil {
		t.Fatal(err)
	}
	if val != 3000 {
		t.Errorf("expected 3000, got %v", val)
	}
}

func TestParseExprDivision(t *testing.T) {
	s := newSchema([]string{"total", "count"})
	r := record{s: s, values: []any{float64(100), float64(4)}}

	node, err := parseExpr("total / count")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := node.eval(r)
	if val != 25 {
		t.Errorf("expected 25, got %v", val)
	}
}

func TestParseExprDivisionByZero(t *testing.T) {
	s := newSchema([]string{"x"})
	r := record{s: s, values: []any{float64(10)}}

	node, err := parseExpr("x / 0")
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.eval(r)
	if err == nil {
		t.Error("expected division by zero error")
	}
}

func TestParseExprUnaryMinus(t *testing.T) {
	node, err := parseExpr("-5")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := node.eval(record{s: newSchema(nil), values: nil})
	if val != -5 {
		t.Errorf("expected -5, got %v", val)
	}
}

func TestParseExprNilField(t *testing.T) {
	s := newSchema([]string{"x"})
	r := record{s: s, values: []any{nil}}

	node, err := parseExpr("x + 1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.eval(r)
	if err == nil {
		t.Error("expected error for nil field")
	}
}

func TestParseExprMissingField(t *testing.T) {
	s := newSchema([]string{"x"})
	r := record{s: s, values: []any{float64(10)}}

	node, err := parseExpr("y + 1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.eval(r)
	if err == nil {
		t.Error("expected error for missing field")
	}
}

func TestParseExprInvalid(t *testing.T) {
	_, err := parseExpr("")
	if err == nil {
		t.Error("expected error for empty expression")
	}

	_, err = parseExpr("2 +")
	if err == nil {
		t.Error("expected error for incomplete expression")
	}

	_, err = parseExpr("(2 + 3")
	if err == nil {
		t.Error("expected error for unclosed parenthesis")
	}
}

func TestParseExprComplexExpression(t *testing.T) {
	s := newSchema([]string{"a", "b", "c"})
	r := record{s: s, values: []any{float64(2), float64(3), float64(4)}}

	node, err := parseExpr("(a + b) * c - 1")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := node.eval(r)
	if val != 19 {
		t.Errorf("expected 19, got %v", val)
	}
}

func TestParseExprIntField(t *testing.T) {
	s := newSchema([]string{"count"})
	r := record{s: s, values: []any{int64(42)}}

	node, err := parseExpr("count * 2")
	if err != nil {
		t.Fatal(err)
	}
	val, _ := node.eval(r)
	if val != 84 {
		t.Errorf("expected 84, got %v", val)
	}
}
