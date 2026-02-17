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
		case "distinct":
			result = result.distinct(op.Field)
		case "limit":
			result = result.limit(op.N, op.Offset)
		default:
			return dataset{}, fmt.Errorf("unknown operation: %s", op.Op)
		}
	}
	return result, nil
}
