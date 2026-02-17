// Data transformation operations for the WASM module.
// Self-contained — no ssql/v4 dependency.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// where filters rows matching a comparison condition.
func (ds dataset) where(field, op, value string) dataset {
	var rows []record
	for _, r := range ds.rows {
		fv := r.mustGet(field)
		if fv == nil {
			continue
		}
		if applyOperator(fv, op, value) {
			rows = append(rows, r)
		}
	}
	return dataset{s: ds.s, rows: rows}
}

// sortBy sorts rows by a field in ascending or descending order.
func (ds dataset) sortBy(field string, desc bool) dataset {
	rows := make([]record, len(ds.rows))
	copy(rows, ds.rows)
	sort.SliceStable(rows, func(i, j int) bool {
		av := rows[i].mustGet(field)
		bv := rows[j].mustGet(field)
		c := compareValues(av, bv)
		if desc {
			return c > 0
		}
		return c < 0
	})
	return dataset{s: ds.s, rows: rows}
}

// groupBy groups rows by a field and applies an aggregation function.
// Preserves first-seen group order.
func (ds dataset) groupBy(groupField, aggField, aggFunc string) dataset {
	type group struct {
		key  any
		rows []record
	}

	// Collect groups preserving insertion order
	groupMap := make(map[string]*group)
	var order []string
	for _, r := range ds.rows {
		key := r.mustGet(groupField)
		keyStr := fmt.Sprintf("%v", key)
		if g, ok := groupMap[keyStr]; ok {
			g.rows = append(g.rows, r)
		} else {
			groupMap[keyStr] = &group{key: key, rows: []record{r}}
			order = append(order, keyStr)
		}
	}

	// Build result schema: groupField + aggFunc
	resultFields := []string{groupField, aggFunc}
	resultSchema := newSchema(resultFields)

	var resultRows []record
	for _, keyStr := range order {
		g := groupMap[keyStr]
		aggVal := aggregate(g.rows, aggField, aggFunc)
		values := []any{g.key, aggVal}
		resultRows = append(resultRows, record{s: resultSchema, values: values})
	}

	return dataset{s: resultSchema, rows: resultRows}
}

// aggregate computes an aggregation over a set of rows.
func aggregate(rows []record, field, fn string) any {
	switch strings.ToLower(fn) {
	case "count":
		return int64(len(rows))
	case "sum":
		return sumField(rows, field)
	case "avg":
		s := sumField(rows, field)
		if len(rows) == 0 {
			return float64(0)
		}
		return s / float64(len(rows))
	case "min":
		return minField(rows, field)
	case "max":
		return maxField(rows, field)
	default:
		return nil
	}
}

func sumField(rows []record, field string) float64 {
	var sum float64
	for _, r := range rows {
		if f, ok := toFloat64(r.mustGet(field)); ok {
			sum += f
		}
	}
	return sum
}

func minField(rows []record, field string) any {
	var minVal float64
	found := false
	for _, r := range rows {
		if f, ok := toFloat64(r.mustGet(field)); ok {
			if !found || f < minVal {
				minVal = f
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	// Return int64 if it's a whole number
	if minVal == math.Trunc(minVal) && minVal >= math.MinInt64 && minVal <= math.MaxInt64 {
		return int64(minVal)
	}
	return minVal
}

func maxField(rows []record, field string) any {
	var maxVal float64
	found := false
	for _, r := range rows {
		if f, ok := toFloat64(r.mustGet(field)); ok {
			if !found || f > maxVal {
				maxVal = f
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	if maxVal == math.Trunc(maxVal) && maxVal >= math.MinInt64 && maxVal <= math.MaxInt64 {
		return int64(maxVal)
	}
	return maxVal
}

// groupByMulti groups rows by multiple fields and applies multiple aggregations.
// Preserves first-seen group order.
func (ds dataset) groupByMulti(groupFields []string, aggs []aggSpec) dataset {
	type group struct {
		keys []any
		rows []record
	}

	groupMap := make(map[string]*group)
	var order []string
	for _, r := range ds.rows {
		// Build composite key
		var keyParts []string
		var keys []any
		for _, gf := range groupFields {
			v := r.mustGet(gf)
			keys = append(keys, v)
			keyParts = append(keyParts, fmt.Sprintf("%v", v))
		}
		keyStr := strings.Join(keyParts, "\x00")
		if g, ok := groupMap[keyStr]; ok {
			g.rows = append(g.rows, r)
		} else {
			groupMap[keyStr] = &group{keys: keys, rows: []record{r}}
			order = append(order, keyStr)
		}
	}

	// Build result schema: groupFields... + agg aliases...
	resultFields := make([]string, 0, len(groupFields)+len(aggs))
	resultFields = append(resultFields, groupFields...)
	for _, a := range aggs {
		resultFields = append(resultFields, a.Alias)
	}
	resultSchema := newSchema(resultFields)

	var resultRows []record
	for _, keyStr := range order {
		g := groupMap[keyStr]
		values := make([]any, len(resultFields))
		// Copy group key values
		copy(values, g.keys)
		// Compute each aggregation
		for i, a := range aggs {
			values[len(groupFields)+i] = aggregate(g.rows, a.Field, a.Func)
		}
		resultRows = append(resultRows, record{s: resultSchema, values: values})
	}

	return dataset{s: resultSchema, rows: resultRows}
}

// distinct deduplicates rows by a field value.
func (ds dataset) distinct(field string) dataset {
	seen := make(map[string]bool)
	var rows []record
	for _, r := range ds.rows {
		key := fmt.Sprintf("%v", r.mustGet(field))
		if !seen[key] {
			seen[key] = true
			rows = append(rows, r)
		}
	}
	return dataset{s: ds.s, rows: rows}
}

// limit returns up to n rows, optionally skipping offset rows first.
func (ds dataset) limit(n, offset int) dataset {
	rows := ds.rows
	if offset > 0 {
		if offset >= len(rows) {
			return dataset{s: ds.s, rows: nil}
		}
		rows = rows[offset:]
	}
	if n > 0 && n < len(rows) {
		rows = rows[:n]
	}
	return dataset{s: ds.s, rows: rows}
}

// compute adds a derived field computed from an arithmetic expression.
func (ds dataset) compute(name, expression string) (dataset, error) {
	expr, err := parseExpr(expression)
	if err != nil {
		return dataset{}, fmt.Errorf("parse expression %q: %w", expression, err)
	}

	// Build expanded schema: original fields + new computed field
	newFields := make([]string, len(ds.s.fields)+1)
	copy(newFields, ds.s.fields)
	newFields[len(ds.s.fields)] = name
	newSchema := newSchema(newFields)

	rows := make([]record, len(ds.rows))
	for i, r := range ds.rows {
		val, err := expr.eval(r)
		if err != nil {
			// On eval error, set field to nil
			values := make([]any, len(newFields))
			copy(values, r.values)
			rows[i] = record{s: newSchema, values: values}
			continue
		}
		values := make([]any, len(newFields))
		copy(values, r.values)
		// Store as int64 if it's a whole number, else float64
		if val == math.Trunc(val) && val >= math.MinInt64 && val <= math.MaxInt64 && !math.IsNaN(val) && !math.IsInf(val, 0) {
			values[len(ds.s.fields)] = int64(val)
		} else {
			values[len(ds.s.fields)] = val
		}
		rows[i] = record{s: newSchema, values: values}
	}

	return dataset{s: newSchema, rows: rows}, nil
}

// pivot creates a cross-tabulation: unique values of colField become columns.
func (ds dataset) pivot(rowField, colField, valField, aggFunc string) dataset {
	// Collect unique column values in first-seen order
	colMap := make(map[string]bool)
	var colOrder []string
	for _, r := range ds.rows {
		cv := fmt.Sprintf("%v", r.mustGet(colField))
		if !colMap[cv] {
			colMap[cv] = true
			colOrder = append(colOrder, cv)
		}
	}

	// Group by (rowField, colField) pair
	type cellKey struct {
		row, col string
	}
	type cell struct {
		rows []record
	}
	cells := make(map[cellKey]*cell)
	rowMap := make(map[string]any) // preserve original row key values
	var rowOrder []string
	rowSeen := make(map[string]bool)
	for _, r := range ds.rows {
		rv := r.mustGet(rowField)
		rvStr := fmt.Sprintf("%v", rv)
		cv := fmt.Sprintf("%v", r.mustGet(colField))
		ck := cellKey{rvStr, cv}
		if c, ok := cells[ck]; ok {
			c.rows = append(c.rows, r)
		} else {
			cells[ck] = &cell{rows: []record{r}}
		}
		if !rowSeen[rvStr] {
			rowSeen[rvStr] = true
			rowMap[rvStr] = rv
			rowOrder = append(rowOrder, rvStr)
		}
	}

	// Build result schema: rowField + each colField value
	resultFields := make([]string, 0, 1+len(colOrder))
	resultFields = append(resultFields, rowField)
	resultFields = append(resultFields, colOrder...)
	resultSchema := newSchema(resultFields)

	var resultRows []record
	for _, rvStr := range rowOrder {
		values := make([]any, len(resultFields))
		values[0] = rowMap[rvStr]
		for i, cv := range colOrder {
			ck := cellKey{rvStr, cv}
			if c, ok := cells[ck]; ok {
				values[i+1] = aggregate(c.rows, valField, aggFunc)
			} else {
				// No data for this cell — default to 0 for numeric aggs, nil for count
				if aggFunc == "count" {
					values[i+1] = int64(0)
				} else {
					values[i+1] = float64(0)
				}
			}
		}
		resultRows = append(resultRows, record{s: resultSchema, values: values})
	}

	return dataset{s: resultSchema, rows: resultRows}
}

// pipeline executes a sequence of operations.
func (ds dataset) pipeline(ops []pipelineOp) (dataset, error) {
	result := ds
	for _, op := range ops {
		switch op.Op {
		case "where":
			result = result.where(op.Field, op.Operator, op.Value)
		case "sort":
			result = result.sortBy(op.Field, op.Desc)
		case "group_by":
			result = result.groupBy(op.GroupField, op.AggField, op.AggFunc)
		case "group_by_multi":
			result = result.groupByMulti(op.GroupFields, op.Aggs)
		case "distinct":
			result = result.distinct(op.Field)
		case "limit":
			result = result.limit(op.N, op.Offset)
		case "compute":
			var err error
			result, err = result.compute(op.Name, op.Expr)
			if err != nil {
				return dataset{}, fmt.Errorf("compute %q: %w", op.Name, err)
			}
		case "pivot":
			result = result.pivot(op.RowField, op.ColField, op.ValField, op.AggFunc)
		default:
			return dataset{}, fmt.Errorf("unknown operation: %s", op.Op)
		}
	}
	return result, nil
}
