// Minimal arithmetic expression evaluator for the WASM module.
// Recursive-descent parser supporting field references, numeric literals,
// and +, -, *, / operators with standard precedence. Parentheses supported.
// TinyGo-compatible: no reflection, no regexp.
package main

import (
	"fmt"
	"strconv"
)

// exprNode is a node in the expression AST.
type exprNode interface {
	eval(r record) (float64, error)
}

// numberNode is a numeric literal.
type numberNode struct {
	val float64
}

func (n numberNode) eval(_ record) (float64, error) { return n.val, nil }

// fieldNode references a record field.
type fieldNode struct {
	name string
}

func (f fieldNode) eval(r record) (float64, error) {
	v := r.mustGet(f.name)
	if v == nil {
		return 0, fmt.Errorf("field %q is nil", f.name)
	}
	if fv, ok := toFloat64(v); ok {
		return fv, nil
	}
	return 0, fmt.Errorf("field %q is not numeric: %v", f.name, v)
}

// binaryNode applies an operator to two sub-expressions.
type binaryNode struct {
	op    byte // '+', '-', '*', '/'
	left  exprNode
	right exprNode
}

func (b binaryNode) eval(r record) (float64, error) {
	lv, err := b.left.eval(r)
	if err != nil {
		return 0, err
	}
	rv, err := b.right.eval(r)
	if err != nil {
		return 0, err
	}
	switch b.op {
	case '+':
		return lv + rv, nil
	case '-':
		return lv - rv, nil
	case '*':
		return lv * rv, nil
	case '/':
		if rv == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return lv / rv, nil
	}
	return 0, fmt.Errorf("unknown operator: %c", b.op)
}

// unaryNegNode negates a sub-expression.
type unaryNegNode struct {
	operand exprNode
}

func (u unaryNegNode) eval(r record) (float64, error) {
	v, err := u.operand.eval(r)
	if err != nil {
		return 0, err
	}
	return -v, nil
}

// exprParser holds the parsing state.
type exprParser struct {
	input []byte
	pos   int
	n     int
}

// parseExpr parses an expression string into an AST.
func parseExpr(s string) (exprNode, error) {
	p := &exprParser{input: []byte(s), pos: 0, n: len(s)}
	node, err := p.parseAddSub()
	if err != nil {
		return nil, err
	}
	p.skipSpaces()
	if p.pos < p.n {
		return nil, fmt.Errorf("unexpected character '%c' at position %d", p.input[p.pos], p.pos)
	}
	return node, nil
}

func (p *exprParser) skipSpaces() {
	for p.pos < p.n && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

// parseAddSub handles + and - (lowest precedence).
func (p *exprParser) parseAddSub() (exprNode, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpaces()
		if p.pos >= p.n {
			return left, nil
		}
		c := p.input[p.pos]
		if c != '+' && c != '-' {
			return left, nil
		}
		p.pos++
		right, err := p.parseMulDiv()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: c, left: left, right: right}
	}
}

// parseMulDiv handles * and / (higher precedence).
func (p *exprParser) parseMulDiv() (exprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpaces()
		if p.pos >= p.n {
			return left, nil
		}
		c := p.input[p.pos]
		if c != '*' && c != '/' {
			return left, nil
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: c, left: left, right: right}
	}
}

// parseUnary handles unary minus.
func (p *exprParser) parseUnary() (exprNode, error) {
	p.skipSpaces()
	if p.pos < p.n && p.input[p.pos] == '-' {
		p.pos++
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryNegNode{operand: operand}, nil
	}
	return p.parseAtom()
}

// parseAtom handles numbers, field names, and parenthesized expressions.
func (p *exprParser) parseAtom() (exprNode, error) {
	p.skipSpaces()
	if p.pos >= p.n {
		return nil, fmt.Errorf("unexpected end of expression")
	}

	c := p.input[p.pos]

	// Parenthesized expression
	if c == '(' {
		p.pos++
		node, err := p.parseAddSub()
		if err != nil {
			return nil, err
		}
		p.skipSpaces()
		if p.pos >= p.n || p.input[p.pos] != ')' {
			return nil, fmt.Errorf("expected ')' at position %d", p.pos)
		}
		p.pos++
		return node, nil
	}

	// Number
	if c >= '0' && c <= '9' || c == '.' {
		return p.parseNumber()
	}

	// Field name (identifier: letters, digits, underscores, hyphens)
	if isIdentStart(c) {
		return p.parseField()
	}

	return nil, fmt.Errorf("unexpected character '%c' at position %d", c, p.pos)
}

func (p *exprParser) parseNumber() (exprNode, error) {
	start := p.pos
	for p.pos < p.n && (p.input[p.pos] >= '0' && p.input[p.pos] <= '9' || p.input[p.pos] == '.') {
		p.pos++
	}
	// Handle exponent
	if p.pos < p.n && (p.input[p.pos] == 'e' || p.input[p.pos] == 'E') {
		p.pos++
		if p.pos < p.n && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
			p.pos++
		}
		for p.pos < p.n && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
			p.pos++
		}
	}
	f, err := strconv.ParseFloat(string(p.input[start:p.pos]), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid number %q: %w", string(p.input[start:p.pos]), err)
	}
	return numberNode{val: f}, nil
}

func (p *exprParser) parseField() (exprNode, error) {
	start := p.pos
	for p.pos < p.n && isIdentChar(p.input[p.pos]) {
		p.pos++
	}
	name := string(p.input[start:p.pos])
	return fieldNode{name: name}, nil
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}
