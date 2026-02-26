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

// groupBy groups rows by multiple fields and applies multiple aggregations.
// Preserves first-seen group order.
func (ds dataset) groupBy(groupFields []string, aggs []aggSpec) dataset {
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

// ============================================================================
// Window functions
// ============================================================================

// windowSpec describes a single window function computation.
type windowSpec struct {
	Type       string // "row_number","rank","dense_rank","ntile","percent_rank","lag","lead","first","last","sum","avg","count","min","max"
	Field      string // source field for lag/lead/sum/avg/min/max/first/last
	N          int    // offset for lag/lead, n for ntile
	ResultName string
}

// windowOrderField specifies a field and sort direction for window ORDER BY.
type windowOrderField struct {
	Field string
	Desc  bool
}

// windowFrame specifies frame boundaries.
// Preceding/Following of -1 means UNBOUNDED.
type windowFrame struct {
	Preceding int
	Following int
}

// windowConfig defines the partition, order, frame, and functions for one window clause.
type windowConfig struct {
	PartitionBy []string
	OrderBy     []windowOrderField
	Frame       windowFrame
	Specs       []windowSpec
}

// window applies window functions without collapsing rows.
func (ds dataset) window(configs []windowConfig) dataset {
	if len(ds.rows) == 0 {
		return ds
	}

	n := len(ds.rows)

	// results[originalIndex][resultFieldName] = computed value
	results := make([]map[string]any, n)
	for i := range results {
		results[i] = make(map[string]any)
	}

	// Track output order from first config that has OrderBy
	var outputOrder []int
	orderDecided := false

	for _, cfg := range configs {
		frame := cfg.Frame

		// Partition records
		partitions := make(map[string][]int)
		var partOrder []string
		for i, r := range ds.rows {
			key := windowGroupKey(r, cfg.PartitionBy)
			if _, exists := partitions[key]; !exists {
				partOrder = append(partOrder, key)
			}
			partitions[key] = append(partitions[key], i)
		}

		// Process each partition
		for _, pkey := range partOrder {
			indices := partitions[pkey]

			// Sort indices by OrderBy fields
			if len(cfg.OrderBy) > 0 {
				sort.SliceStable(indices, func(a, b int) bool {
					return compareRecordByOrderFields(ds.rows[indices[a]], ds.rows[indices[b]], cfg.OrderBy) < 0
				})
			}

			if !orderDecided {
				outputOrder = append(outputOrder, indices...)
			}

			partLen := len(indices)

			// Compute each spec for each row in the sorted partition
			for pos, origIdx := range indices {
				for _, spec := range cfg.Specs {
					val := computeWindowValue(spec, ds.rows, indices, pos, partLen, frame, cfg.OrderBy)
					results[origIdx][spec.ResultName] = val
				}
			}
		}

		if !orderDecided {
			orderDecided = true
		}
	}

	if outputOrder == nil {
		outputOrder = make([]int, n)
		for i := range outputOrder {
			outputOrder[i] = i
		}
	}

	// Collect all result field names in stable order
	var resultFieldNames []string
	resultFieldSet := make(map[string]bool)
	for _, cfg := range configs {
		for _, spec := range cfg.Specs {
			if !resultFieldSet[spec.ResultName] {
				resultFieldSet[spec.ResultName] = true
				resultFieldNames = append(resultFieldNames, spec.ResultName)
			}
		}
	}

	// Build new schema: original fields + result fields
	newFields := make([]string, len(ds.s.fields)+len(resultFieldNames))
	copy(newFields, ds.s.fields)
	for i, rf := range resultFieldNames {
		newFields[len(ds.s.fields)+i] = rf
	}
	newSchema := newSchema(newFields)

	rows := make([]record, len(outputOrder))
	for i, origIdx := range outputOrder {
		values := make([]any, len(newFields))
		copy(values, ds.rows[origIdx].values)
		for _, rf := range resultFieldNames {
			if idx, ok := newSchema.index[rf]; ok {
				values[idx] = results[origIdx][rf]
			}
		}
		rows[i] = record{s: newSchema, values: values}
	}

	return dataset{s: newSchema, rows: rows}
}

// windowGroupKey builds a partition key from a record.
func windowGroupKey(r record, partitionBy []string) string {
	if len(partitionBy) == 0 {
		return ""
	}
	var parts []string
	for _, f := range partitionBy {
		parts = append(parts, fmt.Sprintf("%v", r.mustGet(f)))
	}
	return strings.Join(parts, "\x00")
}

// compareRecordByOrderFields compares two records by window order fields.
func compareRecordByOrderFields(a, b record, orderBy []windowOrderField) int {
	for _, of := range orderBy {
		av := a.mustGet(of.Field)
		bv := b.mustGet(of.Field)
		cmp := compareValues(av, bv)
		if of.Desc {
			cmp = -cmp
		}
		if cmp != 0 {
			return cmp
		}
	}
	return 0
}

// computeWindowValue computes one window function for one row.
func computeWindowValue(spec windowSpec, rows []record, indices []int, pos, partLen int, frame windowFrame, orderBy []windowOrderField) any {
	switch spec.Type {
	case "row_number":
		return int64(pos + 1)

	case "rank":
		rank := pos + 1
		if pos > 0 && compareRecordByOrderFields(rows[indices[pos-1]], rows[indices[pos]], orderBy) == 0 {
			for i := pos - 1; i >= 0; i-- {
				if i == 0 || compareRecordByOrderFields(rows[indices[i-1]], rows[indices[pos]], orderBy) != 0 {
					rank = i + 1
					break
				}
			}
		}
		return int64(rank)

	case "dense_rank":
		denseRank := 1
		for i := 1; i <= pos; i++ {
			if compareRecordByOrderFields(rows[indices[i-1]], rows[indices[i]], orderBy) != 0 {
				denseRank++
			}
		}
		return int64(denseRank)

	case "ntile":
		n := spec.N
		if n <= 0 {
			return int64(1)
		}
		return int64((pos*n)/partLen) + 1

	case "percent_rank":
		if partLen <= 1 {
			return float64(0)
		}
		rank := pos + 1
		if pos > 0 && compareRecordByOrderFields(rows[indices[pos-1]], rows[indices[pos]], orderBy) == 0 {
			for i := pos - 1; i >= 0; i-- {
				if i == 0 || compareRecordByOrderFields(rows[indices[i-1]], rows[indices[pos]], orderBy) != 0 {
					rank = i + 1
					break
				}
			}
		}
		return float64(rank-1) / float64(partLen-1)

	case "lag":
		srcPos := pos - spec.N
		if srcPos < 0 || srcPos >= partLen {
			return nil
		}
		return rows[indices[srcPos]].mustGet(spec.Field)

	case "lead":
		srcPos := pos + spec.N
		if srcPos < 0 || srcPos >= partLen {
			return nil
		}
		return rows[indices[srcPos]].mustGet(spec.Field)

	case "first":
		start := windowFrameStart(pos, partLen, frame)
		return rows[indices[start]].mustGet(spec.Field)

	case "last":
		end := windowFrameEnd(pos, partLen, frame)
		return rows[indices[end]].mustGet(spec.Field)

	case "sum":
		start := windowFrameStart(pos, partLen, frame)
		end := windowFrameEnd(pos, partLen, frame)
		var sum float64
		for i := start; i <= end; i++ {
			if f, ok := toFloat64(rows[indices[i]].mustGet(spec.Field)); ok {
				sum += f
			}
		}
		return sum

	case "avg":
		start := windowFrameStart(pos, partLen, frame)
		end := windowFrameEnd(pos, partLen, frame)
		var sum float64
		var count int
		for i := start; i <= end; i++ {
			if f, ok := toFloat64(rows[indices[i]].mustGet(spec.Field)); ok {
				sum += f
				count++
			}
		}
		if count == 0 {
			return float64(0)
		}
		return sum / float64(count)

	case "count":
		start := windowFrameStart(pos, partLen, frame)
		end := windowFrameEnd(pos, partLen, frame)
		return int64(end - start + 1)

	case "min":
		start := windowFrameStart(pos, partLen, frame)
		end := windowFrameEnd(pos, partLen, frame)
		var minVal any
		for i := start; i <= end; i++ {
			v := rows[indices[i]].mustGet(spec.Field)
			if minVal == nil || compareValues(v, minVal) < 0 {
				minVal = v
			}
		}
		return minVal

	case "max":
		start := windowFrameStart(pos, partLen, frame)
		end := windowFrameEnd(pos, partLen, frame)
		var maxVal any
		for i := start; i <= end; i++ {
			v := rows[indices[i]].mustGet(spec.Field)
			if maxVal == nil || compareValues(v, maxVal) > 0 {
				maxVal = v
			}
		}
		return maxVal
	}

	return nil
}

// windowFrameStart returns the first index in the frame for position pos.
func windowFrameStart(pos, partLen int, frame windowFrame) int {
	if frame.Preceding < 0 {
		return 0 // UNBOUNDED PRECEDING
	}
	start := pos - frame.Preceding
	if start < 0 {
		return 0
	}
	return start
}

// windowFrameEnd returns the last index (inclusive) in the frame for position pos.
func windowFrameEnd(pos, partLen int, frame windowFrame) int {
	if frame.Following < 0 {
		return partLen - 1 // UNBOUNDED FOLLOWING
	}
	end := pos + frame.Following
	if end >= partLen {
		return partLen - 1
	}
	return end
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
			result = result.groupBy(op.GroupFields, op.Aggs)
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
		case "window":
			result = result.window(op.WindowConfigs)
		default:
			return dataset{}, fmt.Errorf("unknown operation: %s", op.Op)
		}
	}
	return result, nil
}
