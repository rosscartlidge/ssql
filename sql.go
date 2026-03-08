package ssql

import (
	"fmt"
	"iter"
	"maps"
	"math"
	"sort"
	"strings"
)

// OrderedValue represents types that are both orderable and valid Record field values
// Used for Min/Max aggregations which require comparison operators
// Note: Must exactly match types from Value constraint that are also comparable with < >
type OrderedValue interface {
	~int64 | ~float64 | string
}

// ============================================================================
// SQL-STYLE OPERATIONS - JOIN, GROUPBY, AGGREGATION
// ============================================================================

// ============================================================================
// JOIN OPERATIONS
// ============================================================================

// JoinPredicate defines the condition for joining two records.
// Implementations can optionally implement KeyExtractor to enable hash join optimization.
type JoinPredicate interface {
	// Match returns true if the left and right records should be joined
	Match(left, right Record) bool
}

// KeyExtractor is an optional interface that JoinPredicate implementations can provide
// to enable O(n+m) hash join optimization instead of O(n×m) nested loop.
type KeyExtractor interface {
	// ExtractKey returns the join key for a record.
	// Returns (key, true) if successful, ("", false) if key fields are missing.
	ExtractKey(r Record) (string, bool)
}

// fieldsJoinPredicate implements both JoinPredicate and KeyExtractor
// for equality-based joins on specific fields.
type fieldsJoinPredicate struct {
	fields []string
}

// customJoinPredicate wraps a custom function for non-optimized joins
type customJoinPredicate struct {
	fn func(left, right Record) bool
}

// innerJoinNested performs O(n×m) nested loop join
func innerJoinNested(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	yield func(Record) bool,
) {
	// Materialize right side for multiple iterations
	var rightRecords []Record
	for r := range rightSeq {
		rightRecords = append(rightRecords, r)
	}

	// Nested loop join
	for left := range leftSeq {
		for _, right := range rightRecords {
			if predicate.Match(left, right) {
				if !yield(mergeRecords(left, right)) {
					return
				}
			}
		}
	}
}

// mergeRecords creates a new Record by merging left and right fields directly.
// This is optimized for high-throughput join operations by avoiding the
// MutableRecord -> Freeze() copy overhead.
func mergeRecords(left, right Record) Record {
	// Pre-allocate with combined capacity
	// Copy left fields directly
	merged := maps.Collect(left.All())
	// Copy right fields (may override left on field name collision)
	maps.Insert(merged, right.All())
	return NewRecord(merged)
}

// innerJoinHash performs O(n+m) hash-based inner join
func innerJoinHash(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	extractor KeyExtractor,
	yield func(Record) bool,
) {
	// BUILD PHASE: Hash right side
	hashTable := make(map[string][]Record)
	for right := range rightSeq {
		key, ok := extractor.ExtractKey(right)
		if !ok {
			continue
		}
		hashTable[key] = append(hashTable[key], right)
	}

	// PROBE PHASE: Stream left and lookup
	for left := range leftSeq {
		key, ok := extractor.ExtractKey(left)
		if !ok {
			continue
		}

		if matches, found := hashTable[key]; found {
			for _, right := range matches {
				// Verify with Match() for correctness (handles hash collisions)
				if predicate.Match(left, right) {
					if !yield(mergeRecords(left, right)) {
						return
					}
				}
			}
		}
	}
}

// InnerJoin performs an inner join between two record streams (SQL INNER JOIN).
// Only returns records where the join predicate matches.
// The right stream is fully materialized in memory.
//
// Performance: Uses O(n+m) hash join for OnFields() predicates, O(n×m) nested loop
// for OnCondition() predicates. Hash join is 3-16x faster for large datasets.
//
// Example:
//
//	// Join customers with their orders
//	customers, _ := ssql.ReadCSV("customers.csv")
//	orders, _ := ssql.ReadCSV("orders.csv")
//
//	customerOrders := ssql.InnerJoin(
//	    orders,
//	    ssql.OnFields("customer_id"),
//	)(customers)
//
//	// Custom join condition
//	highValueOrders := ssql.InnerJoin(
//	    orders,
//	    ssql.OnCondition(func(customer, order ssql.Record) bool {
//	        customerID := ssql.GetOr(customer, "id", "")
//	        orderCustomerID := ssql.GetOr(order, "customer_id", "")
//	        orderAmount := ssql.GetOr(order, "amount", float64(0))
//	        return customerID == orderCustomerID && orderAmount > 1000.0
//	    }),
//	)(customers)
func InnerJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
	return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Check if predicate supports hash join optimization
			if extractor, ok := predicate.(KeyExtractor); ok {
				// Use O(n+m) hash join
				innerJoinHash(leftSeq, rightSeq, predicate, extractor, yield)
				return
			}

			// Fallback to O(n×m) nested loop join
			innerJoinNested(leftSeq, rightSeq, predicate, yield)
		}
	}
}

// leftJoinNested performs O(n×m) nested loop left join
func leftJoinNested(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	yield func(Record) bool,
) {
	// Materialize right side for multiple iterations
	var rightRecords []Record
	for r := range rightSeq {
		rightRecords = append(rightRecords, r)
	}

	for left := range leftSeq {
		matched := false
		for _, right := range rightRecords {
			if predicate.Match(left, right) {
				if !yield(mergeRecords(left, right)) {
					return
				}
				matched = true
			}
		}
		// If no match, yield left record only
		if !matched {
			if !yield(left) {
				return
			}
		}
	}
}

// leftJoinHash performs O(n+m) hash-based left join
func leftJoinHash(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	extractor KeyExtractor,
	yield func(Record) bool,
) {
	// BUILD PHASE: Hash right side
	hashTable := make(map[string][]Record)
	for right := range rightSeq {
		key, ok := extractor.ExtractKey(right)
		if !ok {
			continue
		}
		hashTable[key] = append(hashTable[key], right)
	}

	// PROBE PHASE: Stream left and lookup
	for left := range leftSeq {
		key, ok := extractor.ExtractKey(left)
		matched := false

		if ok {
			if matches, found := hashTable[key]; found {
				for _, right := range matches {
					// Verify with Match() for correctness
					if predicate.Match(left, right) {
						if !yield(mergeRecords(left, right)) {
							return
						}
						matched = true
					}
				}
			}
		}

		// If no match, yield left record only
		if !matched {
			if !yield(left) {
				return
			}
		}
	}
}

// LeftJoin performs a left join between two record streams
func LeftJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
	return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Check if predicate supports hash join optimization
			if extractor, ok := predicate.(KeyExtractor); ok {
				// Use O(n+m) hash join
				leftJoinHash(leftSeq, rightSeq, predicate, extractor, yield)
				return
			}

			// Fallback to O(n×m) nested loop join
			leftJoinNested(leftSeq, rightSeq, predicate, yield)
		}
	}
}

// rightJoinNested performs O(n×m) nested loop right join
func rightJoinNested(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	yield func(Record) bool,
) {
	// Materialize both sides
	var leftRecords []Record
	for l := range leftSeq {
		leftRecords = append(leftRecords, l)
	}
	var rightRecords []Record
	for r := range rightSeq {
		rightRecords = append(rightRecords, r)
	}

	// Track which right records were matched
	matched := make([]bool, len(rightRecords))

	// First pass: yield matched records
	for _, left := range leftRecords {
		for i, right := range rightRecords {
			if predicate.Match(left, right) {
				if !yield(mergeRecords(left, right)) {
					return
				}
				matched[i] = true
			}
		}
	}

	// Second pass: yield unmatched right records
	for i, right := range rightRecords {
		if !matched[i] {
			if !yield(right) {
				return
			}
		}
	}
}

// rightJoinHash performs O(n+m) hash-based right join
func rightJoinHash(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	extractor KeyExtractor,
	yield func(Record) bool,
) {
	// BUILD PHASE: Hash left side and materialize right
	leftHashTable := make(map[string][]Record)
	for left := range leftSeq {
		key, ok := extractor.ExtractKey(left)
		if !ok {
			continue
		}
		leftHashTable[key] = append(leftHashTable[key], left)
	}

	// Materialize right side to track matches
	var rightRecords []Record
	for r := range rightSeq {
		rightRecords = append(rightRecords, r)
	}
	matched := make([]bool, len(rightRecords))

	// PROBE PHASE: For each right record, lookup matching left records
	for i, right := range rightRecords {
		key, ok := extractor.ExtractKey(right)
		if ok {
			if leftMatches, found := leftHashTable[key]; found {
				for _, left := range leftMatches {
					// Verify with Match() for correctness
					if predicate.Match(left, right) {
						if !yield(mergeRecords(left, right)) {
							return
						}
						matched[i] = true
					}
				}
			}
		}
	}

	// Second pass: yield unmatched right records
	for i, right := range rightRecords {
		if !matched[i] {
			if !yield(right) {
				return
			}
		}
	}
}

// RightJoin performs a right join between two record streams
func RightJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
	return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Check if predicate supports hash join optimization
			if extractor, ok := predicate.(KeyExtractor); ok {
				// Use O(n+m) hash join
				rightJoinHash(leftSeq, rightSeq, predicate, extractor, yield)
				return
			}

			// Fallback to O(n×m) nested loop join
			rightJoinNested(leftSeq, rightSeq, predicate, yield)
		}
	}
}

// fullJoinNested performs O(n×m) nested loop full outer join
func fullJoinNested(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	yield func(Record) bool,
) {
	// Materialize both sides
	var leftRecords []Record
	for l := range leftSeq {
		leftRecords = append(leftRecords, l)
	}
	var rightRecords []Record
	for r := range rightSeq {
		rightRecords = append(rightRecords, r)
	}

	// Track which records were matched
	leftMatched := make([]bool, len(leftRecords))
	rightMatched := make([]bool, len(rightRecords))

	// First pass: yield matched records
	for i, left := range leftRecords {
		for j, right := range rightRecords {
			if predicate.Match(left, right) {
				if !yield(mergeRecords(left, right)) {
					return
				}
				leftMatched[i] = true
				rightMatched[j] = true
			}
		}
	}

	// Second pass: yield unmatched left records
	for i, left := range leftRecords {
		if !leftMatched[i] {
			if !yield(left) {
				return
			}
		}
	}

	// Third pass: yield unmatched right records
	for j, right := range rightRecords {
		if !rightMatched[j] {
			if !yield(right) {
				return
			}
		}
	}
}

// fullJoinHash performs O(n+m) hash-based full outer join
func fullJoinHash(
	leftSeq iter.Seq[Record],
	rightSeq iter.Seq[Record],
	predicate JoinPredicate,
	extractor KeyExtractor,
	yield func(Record) bool,
) {
	// BUILD PHASE: Hash right side and materialize left
	rightHashTable := make(map[string][]int) // Map key to indices in rightRecords
	var rightRecords []Record
	for right := range rightSeq {
		key, ok := extractor.ExtractKey(right)
		if ok {
			idx := len(rightRecords)
			rightHashTable[key] = append(rightHashTable[key], idx)
		}
		rightRecords = append(rightRecords, right)
	}

	// Materialize left side
	var leftRecords []Record
	for l := range leftSeq {
		leftRecords = append(leftRecords, l)
	}

	// Track matches
	leftMatched := make([]bool, len(leftRecords))
	rightMatched := make([]bool, len(rightRecords))

	// PROBE PHASE: For each left record, lookup matching right records
	for i, left := range leftRecords {
		key, ok := extractor.ExtractKey(left)
		if ok {
			if rightIndices, found := rightHashTable[key]; found {
				for _, j := range rightIndices {
					right := rightRecords[j]
					// Verify with Match() for correctness
					if predicate.Match(left, right) {
						if !yield(mergeRecords(left, right)) {
							return
						}
						leftMatched[i] = true
						rightMatched[j] = true
					}
				}
			}
		}
	}

	// Second pass: yield unmatched left records
	for i, left := range leftRecords {
		if !leftMatched[i] {
			if !yield(left) {
				return
			}
		}
	}

	// Third pass: yield unmatched right records
	for j, right := range rightRecords {
		if !rightMatched[j] {
			if !yield(right) {
				return
			}
		}
	}
}

// FullJoin performs a full outer join between two record streams
func FullJoin(rightSeq iter.Seq[Record], predicate JoinPredicate) Filter[Record, Record] {
	return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Check if predicate supports hash join optimization
			if extractor, ok := predicate.(KeyExtractor); ok {
				// Use O(n+m) hash join
				fullJoinHash(leftSeq, rightSeq, predicate, extractor, yield)
				return
			}

			// Fallback to O(n×m) nested loop join
			fullJoinNested(leftSeq, rightSeq, predicate, yield)
		}
	}
}

// ============================================================================
// JOIN HELPER FUNCTIONS
// ============================================================================

// OnFields creates a join predicate that matches records on specified fields.
// This is the most common way to join records (equivalent to SQL ON field1 = field2).
//
// Example:
//
//	// Join on single field
//	joined := ssql.InnerJoin(
//	    orders,
//	    ssql.OnFields("customer_id"),
//	)(customers)
//
//	// Join on multiple fields
//	joined := ssql.InnerJoin(
//	    orderDetails,
//	    ssql.OnFields("order_id", "product_id"),
//	)(orders)
func OnFields(fields ...string) JoinPredicate {
	return &fieldsJoinPredicate{fields: fields}
}

// Match implements JoinPredicate for fieldsJoinPredicate
func (p *fieldsJoinPredicate) Match(left, right Record) bool {
	for _, field := range p.fields {
		leftVal, leftExists := Get[any](left, field)
		rightVal, rightExists := Get[any](right, field)
		if !leftExists || !rightExists || leftVal != rightVal {
			return false
		}
	}
	return true
}

// ExtractKey implements KeyExtractor for hash join optimization
func (p *fieldsJoinPredicate) ExtractKey(r Record) (string, bool) {
	var parts []string
	for _, field := range p.fields {
		val, exists := Get[any](r, field)
		if !exists {
			return "", false
		}
		// Convert value to string for hash key
		// Use fmt.Sprintf to handle different types consistently
		parts = append(parts, fmt.Sprintf("%v", val))
	}
	// Join with separator that's unlikely to appear in data
	return strings.Join(parts, "\x00"), true
}

// Match implements JoinPredicate for customJoinPredicate
func (p *customJoinPredicate) Match(left, right Record) bool {
	return p.fn(left, right)
}

// OnCondition creates a join predicate from a custom condition function.
// Custom predicates use O(n×m) nested loop join.
// For better performance with equality joins, use OnFields instead.
func OnCondition(condition func(left, right Record) bool) JoinPredicate {
	return &customJoinPredicate{fn: condition}
}

// OnFieldPair creates a join predicate that matches records where leftField = rightField.
// This is used when the field names differ between the left and right sides.
// Supports hash join optimization (O(n+m) complexity).
//
// Example:
//
//	// Join where left.a_kind = right.kind
//	joined := ssql.InnerJoin(
//	    kindRecords,
//	    ssql.OnFieldPair("a_kind", "kind"),
//	)(leftRecords)
func OnFieldPair(leftField, rightField string) JoinPredicate {
	return &fieldPairJoinPredicate{leftField: leftField, rightField: rightField}
}

// fieldPairJoinPredicate implements JoinPredicate and KeyExtractor for
// equality joins where field names differ between left and right.
type fieldPairJoinPredicate struct {
	leftField  string
	rightField string
}

// Match implements JoinPredicate for fieldPairJoinPredicate
func (p *fieldPairJoinPredicate) Match(left, right Record) bool {
	leftVal, leftExists := Get[any](left, p.leftField)
	rightVal, rightExists := Get[any](right, p.rightField)
	if !leftExists || !rightExists {
		return false
	}
	return fmt.Sprintf("%v", leftVal) == fmt.Sprintf("%v", rightVal)
}

// ExtractKey implements KeyExtractor for hash join optimization.
// Note: This works by using a "mode" - we extract from leftField for left records
// and rightField for right records. Since the hash join builds on right and probes
// with left, we need a way to know which field to use.
// IMPORTANT: This only works correctly because innerJoinHash/leftJoinHash etc.
// build hash table from RIGHT records and probe with LEFT records.
func (p *fieldPairJoinPredicate) ExtractKey(r Record) (string, bool) {
	// Try left field first (for probe phase)
	if val, exists := Get[any](r, p.leftField); exists {
		return fmt.Sprintf("%v", val), true
	}
	// Fall back to right field (for build phase)
	if val, exists := Get[any](r, p.rightField); exists {
		return fmt.Sprintf("%v", val), true
	}
	return "", false
}

// ============================================================================
// LOOKUP JOIN - Multi-clause join for enrichment operations
// ============================================================================

// LookupClause defines a single lookup operation within a LookupJoin.
// Each clause specifies which fields to match on and how to rename fields from the right side.
type LookupClause struct {
	LeftField    string            // Field name in the left record to match on
	RightField   string            // Field name in the right record to match on
	FieldRenames map[string]string // Map of right_field -> new_name for fields to bring in
}

// Lookup creates a LookupClause for joining on specified fields with optional renames.
// The renames parameter is a list of old, new field name pairs.
//
// Example:
//
//	// Join on a_kind=kind, rename kind_name to a_kind_name
//	ssql.Lookup("a_kind", "kind", "kind_name", "a_kind_name")
//
//	// Join on id=id, bring all fields (no renames)
//	ssql.Lookup("id", "id")
func Lookup(leftField, rightField string, renames ...string) LookupClause {
	clause := LookupClause{
		LeftField:    leftField,
		RightField:   rightField,
		FieldRenames: make(map[string]string),
	}
	// Parse rename pairs
	for i := 0; i+1 < len(renames); i += 2 {
		clause.FieldRenames[renames[i]] = renames[i+1]
	}
	return clause
}

// LookupJoin performs multiple lookup operations on the same right-side data.
// This is more efficient than multiple separate joins when enriching records
// from a single lookup table.
//
// For each left record, LookupJoin processes each clause in order:
//   - Finds matching right record(s) by comparing leftField to rightField
//   - Copies fields from right record, applying renames from FieldRenames
//   - If multiple right records match, produces multiple output records (multiply behavior)
//
// If no FieldRenames are specified for a clause, ALL fields from the right record
// are merged (existing behavior).
//
// Example:
//
//	// Lookup a_kind and z_kind from kind.csv in one pass
//	clauses := []ssql.LookupClause{
//	    {LeftField: "a_kind", RightField: "kind", FieldRenames: map[string]string{"kind_name": "a_kind_name"}},
//	    {LeftField: "z_kind", RightField: "kind", FieldRenames: map[string]string{"kind_name": "z_kind_name"}},
//	}
//	enriched := ssql.LookupJoin(kindRecords, clauses)(leftRecords)
func LookupJoin(rightSeq iter.Seq[Record], clauses []LookupClause) Filter[Record, Record] {
	return func(leftSeq iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Materialize right side once
			var rightRecords []Record
			for r := range rightSeq {
				rightRecords = append(rightRecords, r)
			}

			// Build hash indices for each clause (by rightField)
			indices := make([]map[string][]Record, len(clauses))
			for i, clause := range clauses {
				indices[i] = make(map[string][]Record)
				for _, r := range rightRecords {
					if val, exists := Get[any](r, clause.RightField); exists {
						key := fmt.Sprintf("%v", val)
						indices[i][key] = append(indices[i][key], r)
					}
				}
			}

			// Process each left record
			for left := range leftSeq {
				// Start with the original left record
				currentRecords := []Record{left}

				// Apply each clause
				for i, clause := range clauses {
					var nextRecords []Record

					for _, current := range currentRecords {
						// Get the lookup key from the left field
						leftVal, exists := Get[any](current, clause.LeftField)
						if !exists {
							// No match possible, keep current record unchanged
							nextRecords = append(nextRecords, current)
							continue
						}

						key := fmt.Sprintf("%v", leftVal)
						matches, found := indices[i][key]

						if !found || len(matches) == 0 {
							// No match, keep current record unchanged
							nextRecords = append(nextRecords, current)
							continue
						}

						// For each match, create a new record with merged fields
						for _, right := range matches {
							merged := mergeWithRenames(current, right, clause.FieldRenames)
							nextRecords = append(nextRecords, merged)
						}
					}

					currentRecords = nextRecords
				}

				// Yield all resulting records
				for _, r := range currentRecords {
					if !yield(r) {
						return
					}
				}
			}
		}
	}
}

// mergeWithRenames creates a new record by copying left and adding selected/renamed fields from right.
// If renames is empty, all right fields are copied (standard merge behavior).
// If renames has entries, only those fields are copied with the specified new names.
func mergeWithRenames(left, right Record, renames map[string]string) Record {
	// Start with a copy of left
	merged := maps.Collect(left.All())

	if len(renames) == 0 {
		// No renames specified - copy all right fields (standard merge)
		maps.Insert(merged, right.All())
	} else {
		// Only copy specified fields with new names
		for rightField, newName := range renames {
			if val, exists := Get[any](right, rightField); exists {
				merged[newName] = val
			}
		}
	}

	return NewRecord(merged)
}

// ============================================================================
// GROUPBY OPERATIONS
// ============================================================================

// GroupBy groups records by a key extraction function (SQL GROUP BY with custom key).
// Returns records with the key field and a sequence field containing group members.
// Use with Aggregate to compute aggregations over each group.
//
// Example:
//
//	// Group by age bracket
//	data, _ := ssql.ReadCSV("people.csv")
//	grouped := ssql.GroupBy[string](
//	    "group_members",
//	    "age_bracket",
//	    func(r ssql.Record) string {
//	        age := ssql.GetOr(r, "age", int64(0))
//	        if age < 30 {
//	            return "young"
//	        } else if age < 60 {
//	            return "middle"
//	        }
//	        return "senior"
//	    },
//	)(data)
//
//	// Apply aggregations
//	summary := ssql.Aggregate("group_members", map[string]ssql.AggregateFunc{
//	    "count":      ssql.Count(),
//	    "avg_salary": ssql.Avg("salary"),
//	})(grouped)
func GroupBy[K comparable](sequenceField string, keyField string, keyFn func(Record) K) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			groups := make(map[K][]Record)
			var keys []K

			// Collect all records into groups
			for record := range input {
				key := keyFn(record)
				if _, exists := groups[key]; !exists {
					keys = append(keys, key)
				}
				groups[key] = append(groups[key], record)
			}

			// Yield records with key field + sequence field
			for _, key := range keys {
				result := MakeMutableRecord()

				// Set the key field
				result.fields[keyField] = key

				// Add the sequence of group members as an iter.Seq[Record]
				groupRecords := groups[key]
				result.fields[sequenceField] = func() iter.Seq[Record] {
					return func(yield func(Record) bool) {
						for _, record := range groupRecords {
							if !yield(record) {
								return
							}
						}
					}
				}()

				if !yield(result.Freeze()) {
					return
				}
			}
		}
	}
}

// GroupByFields groups records by specified field values (SQL GROUP BY field1, field2...).
// Returns Records with grouping fields + a sequence field containing group members.
// Use with Aggregate to compute aggregations over each group.
//
// This is the most common grouping operation in ssql.
//
// Example:
//
//	// Group sales by region
//	sales, _ := ssql.ReadCSV("sales.csv")
//	grouped := ssql.GroupByFields("sales", "region")(sales)
//
//	// Compute aggregations
//	summary := ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
//	    "total_revenue": ssql.Sum("amount"),
//	    "count":         ssql.Count(),
//	    "avg_amount":    ssql.Avg("amount"),
//	})(grouped)
//
//	// Group by multiple fields
//	grouped := ssql.GroupByFields("orders", "region", "product_category")(sales)
func GroupByFields(sequenceField string, fields ...string) Filter[Record, Record] {
	// Dispatch to optimized implementation based on number of fields
	switch len(fields) {
	case 1:
		return groupByOneField(sequenceField, fields[0])
	case 2:
		return groupByTwoFields(sequenceField, fields[0], fields[1])
	default:
		return groupByMultipleFields(sequenceField, fields)
	}
}

// groupByOneField is optimized for single-field grouping using map[any][]Record
// No string conversion needed - uses values directly as keys
func groupByOneField(sequenceField, field string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			groups := make(map[any][]Record)
			var keys []any // Maintain insertion order

			for record := range input {
				val, exists := Get[any](record, field)
				if !exists {
					val = nil
				} else if !isSimpleValue(val) {
					continue // Skip complex values
				}

				if _, exists := groups[val]; !exists {
					keys = append(keys, val)
				}
				groups[val] = append(groups[val], record)
			}

			// Yield grouped records
			for _, key := range keys {
				groupRecords := groups[key]
				result := MakeMutableRecord()
				result.fields[field] = key
				result.fields[sequenceField] = recordsToSeq(groupRecords)

				if !yield(result.Freeze()) {
					return
				}
			}
		}
	}
}

// groupKey2 is a comparable key for two-field grouping
type groupKey2 [2]any

// groupByTwoFields is optimized for two-field grouping using array keys
func groupByTwoFields(sequenceField, field1, field2 string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			groups := make(map[groupKey2][]Record)
			var keys []groupKey2

			for record := range input {
				var key groupKey2
				skip := false

				if val, exists := Get[any](record, field1); exists {
					if !isSimpleValue(val) {
						skip = true
					} else {
						key[0] = val
					}
				}
				if !skip {
					if val, exists := Get[any](record, field2); exists {
						if !isSimpleValue(val) {
							skip = true
						} else {
							key[1] = val
						}
					}
				}

				if skip {
					continue
				}

				if _, exists := groups[key]; !exists {
					keys = append(keys, key)
				}
				groups[key] = append(groups[key], record)
			}

			for _, key := range keys {
				groupRecords := groups[key]
				result := MakeMutableRecord()
				result.fields[field1] = key[0]
				result.fields[field2] = key[1]
				result.fields[sequenceField] = recordsToSeq(groupRecords)

				if !yield(result.Freeze()) {
					return
				}
			}
		}
	}
}

// groupKeyN is a comparable key for N-field grouping (up to 8 fields)
type groupKeyN [8]any

// groupByMultipleFields handles 3+ fields using fixed-size array keys
func groupByMultipleFields(sequenceField string, fields []string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			groups := make(map[groupKeyN][]Record)
			var keys []groupKeyN

			for record := range input {
				var key groupKeyN
				skip := false

				for i, field := range fields {
					if i >= 8 {
						break // Safety limit
					}
					if val, exists := Get[any](record, field); exists {
						if !isSimpleValue(val) {
							skip = true
							break
						}
						key[i] = val
					}
				}

				if skip {
					continue
				}

				if _, exists := groups[key]; !exists {
					keys = append(keys, key)
				}
				groups[key] = append(groups[key], record)
			}

			for _, key := range keys {
				groupRecords := groups[key]
				result := MakeMutableRecord()
				for i, field := range fields {
					if i >= 8 {
						break
					}
					result.fields[field] = key[i]
				}
				result.fields[sequenceField] = recordsToSeq(groupRecords)

				if !yield(result.Freeze()) {
					return
				}
			}
		}
	}
}

// recordsToSeq converts a slice of records to an iter.Seq[Record]
func recordsToSeq(records []Record) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for _, record := range records {
			if !yield(record) {
				return
			}
		}
	}
}

// StreamGroupByFields groups presorted records by specified field values in a streaming fashion.
// Input MUST be sorted by the grouping fields. Buffers only one group at a time (O(group size) memory).
// Output format is identical to GroupByFields — Records with grouping fields + sequence field.
//
// Example:
//
//	// Data already sorted by dept
//	sorted := ssql.SortBy(func(r ssql.Record) string { return ssql.GetOr(r, "dept", "") })(records)
//	grouped := ssql.StreamGroupByFields("members", "dept")(sorted)
//	summary := ssql.Aggregate("members", map[string]ssql.AggregateFunc{
//	    "count": ssql.Count(),
//	})(grouped)
func StreamGroupByFields(sequenceField string, fields ...string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var currentKey string
			var currentKeyValues []any
			var buffer []Record

			for record := range input {
				// Extract key values from this record
				keyValues := make([]any, len(fields))
				for i, field := range fields {
					val, exists := Get[any](record, field)
					if !exists {
						val = nil
					}
					keyValues[i] = val
				}
				key := fmt.Sprintf("%v", keyValues)

				if len(buffer) == 0 {
					// First record
					currentKey = key
					currentKeyValues = keyValues
					buffer = append(buffer, record)
					continue
				}

				if key == currentKey {
					// Same group — buffer
					buffer = append(buffer, record)
				} else {
					// Group changed — emit buffered group
					result := MakeMutableRecord()
					for i, field := range fields {
						result.fields[field] = currentKeyValues[i]
					}
					result.fields[sequenceField] = recordsToSeq(buffer)
					if !yield(result.Freeze()) {
						return
					}

					// Start new group
					currentKey = key
					currentKeyValues = keyValues
					buffer = []Record{record}
				}
			}

			// Emit final group
			if len(buffer) > 0 {
				result := MakeMutableRecord()
				for i, field := range fields {
					result.fields[field] = currentKeyValues[i]
				}
				result.fields[sequenceField] = recordsToSeq(buffer)
				yield(result.Freeze())
			}
		}
	}
}

// ============================================================================
// AGGREGATION OPERATIONS
// ============================================================================

// AggregateResult is a sealed interface that can only be created by AggResult[V Value]
// This ensures at compile time that aggregation results satisfy the Value constraint
type AggregateResult interface {
	getValue() any
	GetValue() any // Public getter for external use
	sealed()       // Prevents external implementations
}

// AggResult wraps an aggregation result with compile-time type safety
// The generic constraint V Value ensures only valid types can be wrapped
type AggResult[V Value] struct {
	val V
}

func (a AggResult[V]) getValue() any { return a.val }
func (a AggResult[V]) GetValue() any { return a.val }
func (a AggResult[V]) sealed()       {}

// AggregateFunc defines an aggregation function over a group of records.
// Takes a slice of records and returns an AggregateResult.
// The result is guaranteed at compile time to contain a type satisfying Value.
type AggregateFunc func([]Record) AggregateResult

// Aggregate applies aggregation functions to records containing sequence fields.
// Use after GroupBy or GroupByFields to compute summary statistics.
//
// Example:
//
//	// Complete GROUP BY + Aggregate pipeline
//	sales, _ := ssql.ReadCSV("sales.csv")
//
//	// Group and aggregate in one pipeline
//	summary := ssql.Aggregate("sales", map[string]ssql.AggregateFunc{
//	    "total_revenue": ssql.Sum("amount"),
//	    "count":         ssql.Count(),
//	    "avg_amount":    ssql.Avg("amount"),
//	    "min_amount":    ssql.Min[float64]("amount"),
//	    "max_amount":    ssql.Max[float64]("amount"),
//	})(ssql.GroupByFields("sales", "region")(sales))
//
//	// Get top 5 regions by revenue
//	top5 := ssql.Limit[ssql.Record](5)(
//	    ssql.SortBy(func(r ssql.Record) float64 {
//	        return -ssql.GetOr(r, "total_revenue", float64(0))
//	    })(summary))
func Aggregate(sequenceField string, aggregations map[string]AggregateFunc) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			for record := range input {
				result := MakeMutableRecord()

				// Copy all fields except the sequence field
				for field, value := range record.All() {
					if field != sequenceField {
						result.fields[field] = value
					}
				}

				// Extract the sequence from the specified field
				if seqValue, exists := Get[any](record, sequenceField); exists {
					if seq, ok := seqValue.(iter.Seq[Record]); ok {
						// Materialize the sequence for aggregation functions
						var records []Record
						for r := range seq {
							records = append(records, r)
						}

						// Apply all aggregation functions (type-safe at compile time)
						for name, aggFn := range aggregations {
							aggResult := aggFn(records)
							result.fields[name] = aggResult.getValue()
						}
					}
				}

				if !yield(result.Freeze()) {
					return
				}
			}
		}
	}
}

// Pivot creates a cross-tabulation (pivot table): unique values of colField become columns.
// Each cell contains the result of applying aggFunc to valField values grouped by (rowField, colField).
// Supported aggFunc values: "count", "sum", "avg", "min", "max".
//
// Example:
//
//	// Input: dept, quarter, revenue
//	// Output: dept, Q1, Q2, Q3, Q4 (with aggregated revenue values)
//	pivoted := ssql.Pivot("dept", "quarter", "revenue", "sum")(records)
func Pivot(rowField, colField, valField, aggFunc string) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// Materialize all records (pivot needs two passes)
			var all []Record
			for r := range input {
				all = append(all, r)
			}
			if len(all) == 0 {
				return
			}

			// Collect unique column values in first-seen order
			colSeen := make(map[string]bool)
			var colOrder []string
			for _, r := range all {
				cv := fmt.Sprintf("%v", GetOr(r, colField, ""))
				if !colSeen[cv] {
					colSeen[cv] = true
					colOrder = append(colOrder, cv)
				}
			}

			// Group by (rowField, colField) pair
			type cellKey struct{ row, col string }
			cells := make(map[cellKey][]float64)
			rowOriginal := make(map[string]any) // preserve original row key values
			var rowOrder []string
			rowSeen := make(map[string]bool)

			for _, r := range all {
				rv := GetOr[any](r, rowField, "")
				rvStr := fmt.Sprintf("%v", rv)
				cv := fmt.Sprintf("%v", GetOr(r, colField, ""))

				ck := cellKey{rvStr, cv}
				if aggFunc == "count" {
					cells[ck] = append(cells[ck], 1)
				} else {
					if v, ok := Get[float64](r, valField); ok {
						cells[ck] = append(cells[ck], v)
					}
				}

				if !rowSeen[rvStr] {
					rowSeen[rvStr] = true
					rowOriginal[rvStr] = rv
					rowOrder = append(rowOrder, rvStr)
				}
			}

			// Emit one record per row value
			for _, rvStr := range rowOrder {
				mut := MakeMutableRecord()
				// Set row field preserving original type
				rowVal := rowOriginal[rvStr]
				switch v := rowVal.(type) {
				case string:
					mut = mut.String(rowField, v)
				case int64:
					mut = mut.Int(rowField, v)
				case float64:
					mut = mut.Float(rowField, v)
				default:
					mut = mut.String(rowField, fmt.Sprintf("%v", v))
				}

				for _, cv := range colOrder {
					ck := cellKey{rvStr, cv}
					vals := cells[ck]
					switch aggFunc {
					case "count":
						mut = mut.Int(cv, int64(len(vals)))
					case "sum":
						var sum float64
						for _, v := range vals {
							sum += v
						}
						mut = mut.Float(cv, sum)
					case "avg":
						var sum float64
						for _, v := range vals {
							sum += v
						}
						if len(vals) > 0 {
							mut = mut.Float(cv, sum/float64(len(vals)))
						} else {
							mut = mut.Float(cv, 0)
						}
					case "min":
						if len(vals) > 0 {
							min := vals[0]
							for _, v := range vals[1:] {
								if v < min {
									min = v
								}
							}
							mut = mut.Float(cv, min)
						} else {
							mut = mut.Float(cv, 0)
						}
					case "max":
						if len(vals) > 0 {
							max := vals[0]
							for _, v := range vals[1:] {
								if v > max {
									max = v
								}
							}
							mut = mut.Float(cv, max)
						} else {
							mut = mut.Float(cv, 0)
						}
					}
				}

				if !yield(mut.Freeze()) {
					return
				}
			}
		}
	}
}

// ============================================================================
// ROLLUP / CUBE OPERATIONS
// ============================================================================

// RollupMode specifies which grouping sets to compute.
type RollupMode int

const (
	// RollupHierarchical produces grouping sets by peeling fields from the right:
	// [], [a], [a,b], [a,b,c] — like SQL GROUP BY ROLLUP(a,b,c).
	RollupHierarchical RollupMode = iota

	// RollupCube produces all 2^n subsets of fields:
	// [], [a], [b], [a,b] — like SQL GROUP BY CUBE(a,b).
	RollupCube
)

// RollupConfig specifies the fields, aggregations, and mode for a rollup operation.
type RollupConfig struct {
	Fields       []string                 // Group-by fields in order
	Aggregations map[string]AggregateFunc // Named aggregation functions
	Mode         RollupMode               // Rollup or Cube
}

// Rollup performs hierarchical or cube aggregation, enriching each detail-level row
// with parent-level aggregation results using a field naming convention.
//
// For each detail-level group (all fields), the output record contains:
//   - The group key fields (e.g., a_kind, z_kind)
//   - Detail-level aggregations with full prefix: a_kind_z_kind_count
//   - Parent-level aggregations with shorter prefixes: a_kind_count, count
//
// Field naming rule for grouping set [f1, f2, ...] and aggregation result name R:
//   - ()           → R           (e.g., "count")
//   - (a)          → a_R         (e.g., "a_kind_count")
//   - (a, b)       → a_b_R       (e.g., "a_kind_z_kind_count")
//
// Example:
//
//	config := ssql.RollupConfig{
//	    Fields:       []string{"dept", "region"},
//	    Aggregations: map[string]ssql.AggregateFunc{"count": ssql.Count()},
//	    Mode:         ssql.RollupHierarchical,
//	}
//	enriched := ssql.Rollup(config)(records)
//	// Each row: dept, region, dept_region_count, dept_count, count
func Rollup(config RollupConfig) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// 1. Materialize all input records
			var allRecords []Record
			for r := range input {
				allRecords = append(allRecords, r)
			}
			if len(allRecords) == 0 {
				return
			}

			// 2. Compute grouping sets
			sets := computeGroupingSets(config.Fields, config.Mode)

			// 3. For each grouping set, group records and compute aggregations
			// Store results: groupingSetIndex → groupKey → aggName → value
			type aggResults map[string]any
			type groupResults map[string]aggResults
			setResults := make([]groupResults, len(sets))

			for i, setFields := range sets {
				setResults[i] = make(groupResults)

				// Group records by these fields
				groups := make(map[string][]Record)
				var groupOrder []string

				for _, r := range allRecords {
					key := buildGroupKey(r, setFields)
					if _, exists := groups[key]; !exists {
						groupOrder = append(groupOrder, key)
					}
					groups[key] = append(groups[key], r)
				}

				// Compute aggregations for each group
				for _, key := range groupOrder {
					members := groups[key]
					results := make(aggResults)
					for name, aggFn := range config.Aggregations {
						results[name] = aggFn(members).getValue()
					}
					setResults[i][key] = results
				}
			}

			// 4. Emit output: iterate over the most detailed level (last set = all fields)
			detailSetIdx := len(sets) - 1
			detailFields := sets[detailSetIdx]

			// Group records at detail level to get key values
			detailGroups := make(map[string]Record) // key → first record (for field values)
			var detailOrder []string

			for _, r := range allRecords {
				key := buildGroupKey(r, detailFields)
				if _, exists := detailGroups[key]; !exists {
					detailGroups[key] = r
					detailOrder = append(detailOrder, key)
				}
			}

			for _, detailKey := range detailOrder {
				representative := detailGroups[detailKey]
				mut := MakeMutableRecord()

				// Set group key fields from the representative record
				for _, field := range config.Fields {
					val, exists := Get[any](representative, field)
					if exists {
						mut.fields[field] = val
					}
				}

				// For each grouping set, look up the aggregation results and add with prefix
				for i, setFields := range sets {
					// Build the lookup key for this grouping set from the detail record
					lookupKey := buildGroupKey(representative, setFields)
					prefix := groupingSetPrefix(setFields)

					if results, ok := setResults[i][lookupKey]; ok {
						for aggName, value := range results {
							fieldName := prefix + aggName
							mut.fields[fieldName] = value
						}
					}
				}

				if !yield(mut.Freeze()) {
					return
				}
			}
		}
	}
}

// computeGroupingSets returns the list of field subsets for the given mode.
//
// For RollupHierarchical with fields [a, b, c]:
//
//	[], [a], [a,b], [a,b,c]
//
// For RollupCube with fields [a, b]:
//
//	[], [a], [b], [a,b]
func computeGroupingSets(fields []string, mode RollupMode) [][]string {
	switch mode {
	case RollupCube:
		n := len(fields)
		sets := make([][]string, 0, 1<<n)
		for mask := 0; mask < (1 << n); mask++ {
			var subset []string
			for i := range n {
				if mask&(1<<i) != 0 {
					subset = append(subset, fields[i])
				}
			}
			sets = append(sets, subset)
		}
		return sets
	default: // RollupHierarchical
		sets := make([][]string, len(fields)+1)
		sets[0] = []string{} // grand total
		for i := 1; i <= len(fields); i++ {
			sets[i] = make([]string, i)
			copy(sets[i], fields[:i])
		}
		return sets
	}
}

// groupingSetPrefix joins field names with "_" and appends "_".
// Returns empty string for the grand total (empty field list).
func groupingSetPrefix(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, "_") + "_"
}

// buildGroupKey concatenates field values as a lookup key for a grouping set.
func buildGroupKey(record Record, fields []string) string {
	if len(fields) == 0 {
		return "" // grand total — single group
	}
	var parts []string
	for _, field := range fields {
		val, _ := Get[any](record, field)
		parts = append(parts, fmt.Sprintf("%v", val))
	}
	return strings.Join(parts, "\x00")
}

// ============================================================================
// COMMON AGGREGATION FUNCTIONS
// ============================================================================

// Count returns the number of records in a group (SQL COUNT(*)).
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "total": ssql.Count(),
//	}
func Count() AggregateFunc {
	return func(records []Record) AggregateResult {
		return AggResult[int64]{val: int64(len(records))}
	}
}

// Sum sums numeric values from a field across all records (SQL SUM(field)).
// Automatically converts values to float64.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "total_revenue": ssql.Sum("amount"),
//	}
func Sum(field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		var sum float64
		for _, record := range records {
			// Use type-safe Get with automatic conversion to float64
			if value, ok := Get[float64](record, field); ok {
				sum += value
			}
		}
		return AggResult[float64]{val: sum}
	}
}

// Avg calculates the average of numeric values from a field (SQL AVG(field)).
// Automatically converts values to float64. Returns 0.0 for empty groups.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "avg_salary": ssql.Avg("salary"),
//	}
func Avg(field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		var sum float64
		var count int64
		for _, record := range records {
			// Use type-safe Get with automatic conversion to float64
			if value, ok := Get[float64](record, field); ok {
				sum += value
				count++
			}
		}
		if count == 0 {
			return AggResult[float64]{val: 0.0}
		}
		return AggResult[float64]{val: sum / float64(count)}
	}
}

// Min finds the minimum value from a field across all records (SQL MIN(field)).
// Requires specifying the type parameter for type safety.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "min_age":    ssql.Min[int64]("age"),
//	    "min_salary": ssql.Min[float64]("salary"),
//	}
func Min[T OrderedValue](field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		if len(records) == 0 {
			var zero T
			return AggResult[T]{val: zero}
		}

		var min T
		found := false
		for _, record := range records {
			// Use type-safe Get with automatic conversion
			if value, ok := Get[T](record, field); ok {
				if !found || value < min {
					min = value
					found = true
				}
			}
		}
		return AggResult[T]{val: min}
	}
}

// Max finds the maximum value from a field across all records (SQL MAX(field)).
// Requires specifying the type parameter for type safety.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "max_age":    ssql.Max[int64]("age"),
//	    "max_salary": ssql.Max[float64]("salary"),
//	}
func Max[T OrderedValue](field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		if len(records) == 0 {
			var zero T
			return AggResult[T]{val: zero}
		}

		var max T
		found := false
		for _, record := range records {
			// Use type-safe Get with automatic conversion
			if value, ok := Get[T](record, field); ok {
				if !found || value > max {
					max = value
					found = true
				}
			}
		}
		return AggResult[T]{val: max}
	}
}

// First returns the first non-nil value from a field
// Requires specifying the type parameter for compile-time type safety
func First[T Value](field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		for _, record := range records {
			if val, ok := Get[T](record, field); ok {
				return AggResult[T]{val: val}
			}
		}
		var zero T
		return AggResult[T]{val: zero}
	}
}

// Last returns the last non-nil value from a field
// Requires specifying the type parameter for compile-time type safety
func Last[T Value](field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		var lastVal T
		found := false
		for _, record := range records {
			if val, ok := Get[T](record, field); ok {
				lastVal = val
				found = true
			}
		}
		if !found {
			var zero T
			return AggResult[T]{val: zero}
		}
		return AggResult[T]{val: lastVal}
	}
}

// Collect gathers all values from a field into a slice (SQL GROUP_CONCAT/ARRAY_AGG).
// Returns a []any slice containing all non-nil values from the specified field.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "all_names": ssql.Collect("name"),
//	    "products":  ssql.Collect("product"),
//	}
func Collect(field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		var result []any
		for _, record := range records {
			if val, ok := Get[any](record, field); ok && val != nil {
				result = append(result, val)
			}
		}
		if result == nil {
			result = []any{}
		}
		return AggResult[[]any]{val: result}
	}
}

// CollectSeq gathers all values from a field into a typed iterator (SQL GROUP_CONCAT/ARRAY_AGG).
// Returns an iter.Seq[any] containing all values from the specified field that match the type T.
// Requires specifying the type parameter for compile-time type safety during collection.
// Values that don't match type T are skipped.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "all_names":    ssql.CollectSeq[string]("name"),
//	    "all_amounts":  ssql.CollectSeq[float64]("amount"),
//	    "all_products": ssql.CollectSeq[ssql.Record]("product"),
//	}
//
// To iterate with type safety, use type assertion:
//
//	for val := range result {
//	    name := val.(string)  // Safe if you used CollectSeq[string]
//	}
func CollectSeq[T Value](field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		// Collect values into a slice first (records slice is consumed during aggregation)
		var values []T
		for _, record := range records {
			if val, ok := Get[T](record, field); ok {
				values = append(values, val)
			}
		}
		// Return an iterator over the collected values as iter.Seq[any]
		seq := func(yield func(any) bool) {
			for _, v := range values {
				if !yield(v) {
					return
				}
			}
		}
		return AggResult[iter.Seq[any]]{val: seq}
	}
}

// ============================================================================
// WINDOW / ANALYTIC FUNCTIONS
// ============================================================================

// OrderField specifies a field and sort direction for window ORDER BY.
type OrderField struct {
	Field string
	Desc  bool
}

// SortRecords sorts records by multiple fields with mixed ascending/descending.
// Uses CompareRecordFields for proper cross-type comparison (strings, ints, floats).
func SortRecords(orderBy []OrderField) Filter[Record, Record] {
	return SortFunc(func(a, b Record) int {
		return CompareRecordFields(a, b, orderBy)
	})
}

// WindowFrame specifies the frame boundaries for aggregate window functions.
// Preceding/Following of -1 means UNBOUNDED.
type WindowFrame struct {
	Preceding int // rows before current (-1 = UNBOUNDED PRECEDING)
	Following int // rows after current (-1 = UNBOUNDED FOLLOWING)
}

// WindowSpec defines a single window function computation and its result field name.
type WindowSpec struct {
	Function   WindowFunc
	ResultName string
}

// WindowConfig defines the partitioning, ordering, frame, and window functions for one clause.
type WindowConfig struct {
	PartitionBy []string
	OrderBy     []OrderField
	Frame       WindowFrame
	Specs       []WindowSpec
}

// WindowFunc is a sealed interface for window function types.
type WindowFunc interface {
	windowFunc()
}

// Ranking functions
type wRowNumber struct{}
type wRank struct{}
type wDenseRank struct{}
type wNtile struct{ N int }
type wPercentRank struct{}

// Offset functions
type wLag struct {
	Field  string
	Offset int
}
type wLead struct {
	Field  string
	Offset int
}
type wFirst struct{ Field string }
type wLast struct{ Field string }

// Aggregate window functions
type wSum struct{ Field string }
type wAvg struct{ Field string }
type wCount struct{}
type wMin struct{ Field string }
type wMax struct{ Field string }

func (wRowNumber) windowFunc()   {}
func (wRank) windowFunc()        {}
func (wDenseRank) windowFunc()   {}
func (wNtile) windowFunc()       {}
func (wPercentRank) windowFunc() {}
func (wLag) windowFunc()         {}
func (wLead) windowFunc()        {}
func (wFirst) windowFunc()       {}
func (wLast) windowFunc()        {}
func (wSum) windowFunc()         {}
func (wAvg) windowFunc()         {}
func (wCount) windowFunc()       {}
func (wMin) windowFunc()         {}
func (wMax) windowFunc()         {}

// Constructors

// WRowNumber creates a ROW_NUMBER() window function.
func WRowNumber() WindowFunc { return wRowNumber{} }

// WRank creates a RANK() window function.
func WRank() WindowFunc { return wRank{} }

// WDenseRank creates a DENSE_RANK() window function.
func WDenseRank() WindowFunc { return wDenseRank{} }

// WNtile creates a NTILE(n) window function.
func WNtile(n int) WindowFunc { return wNtile{N: n} }

// WPercentRank creates a PERCENT_RANK() window function.
func WPercentRank() WindowFunc { return wPercentRank{} }

// WLag creates a LAG(field, offset) window function.
func WLag(field string, offset int) WindowFunc { return wLag{Field: field, Offset: offset} }

// WLead creates a LEAD(field, offset) window function.
func WLead(field string, offset int) WindowFunc { return wLead{Field: field, Offset: offset} }

// WFirst creates a FIRST_VALUE(field) window function.
func WFirst(field string) WindowFunc { return wFirst{Field: field} }

// WLast creates a LAST_VALUE(field) window function.
func WLast(field string) WindowFunc { return wLast{Field: field} }

// WSum creates a windowed SUM(field) function.
func WSum(field string) WindowFunc { return wSum{Field: field} }

// WAvg creates a windowed AVG(field) function.
func WAvg(field string) WindowFunc { return wAvg{Field: field} }

// WCount creates a windowed COUNT(*) function.
func WCount() WindowFunc { return wCount{} }

// WMin creates a windowed MIN(field) function.
func WMin(field string) WindowFunc { return wMin{Field: field} }

// WMax creates a windowed MAX(field) function.
func WMax(field string) WindowFunc { return wMax{Field: field} }

// CompareAny compares two values of any type for sorting.
// Returns -1, 0, or 1. nil sorts before non-nil. Numeric types are compared cross-type.
// Falls back to string comparison.
func CompareAny(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// Try numeric comparison
	af, aOk := toFloat(a)
	bf, bOk := toFloat(b)
	if aOk && bOk {
		if af < bf {
			return -1
		}
		if af > bf {
			return 1
		}
		return 0
	}

	// Fall back to string comparison
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	if as < bs {
		return -1
	}
	if as > bs {
		return 1
	}
	return 0
}

// toFloat converts any numeric value to float64.
func toFloat(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int64:
		return float64(val), true
	case int:
		return float64(val), true
	default:
		return 0, false
	}
}

// CompareRecordFields compares two records by multiple order fields.
func CompareRecordFields(a, b Record, orderBy []OrderField) int {
	for _, of := range orderBy {
		av, _ := Get[any](a, of.Field)
		bv, _ := Get[any](b, of.Field)
		cmp := CompareAny(av, bv)
		if of.Desc {
			cmp = -cmp
		}
		if cmp != 0 {
			return cmp
		}
	}
	return 0
}

// Window applies window functions to a record stream without collapsing rows.
// Each WindowConfig defines a partition/order/frame context and a set of window functions.
// The result fields are added to every output record.
func Window(configs []WindowConfig) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			// 1. Materialize all input records with original indices
			var all []Record
			for r := range input {
				all = append(all, r)
			}
			if len(all) == 0 {
				return
			}

			n := len(all)

			// results[originalIndex][resultFieldName] = computed value
			results := make([]map[string]any, n)
			for i := range results {
				results[i] = make(map[string]any)
			}

			// Track output order from first config that has OrderBy
			var outputOrder []int
			orderDecided := false

			// 2. Process each config (clause)
			for _, cfg := range configs {
				frame := cfg.Frame

				// Partition records
				// partitions maps group key -> list of original indices
				partitions := make(map[string][]int)
				var partOrder []string // preserve first-seen order of partition keys

				for i, r := range all {
					key := buildGroupKey(r, cfg.PartitionBy)
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
							return CompareRecordFields(all[indices[a]], all[indices[b]], cfg.OrderBy) < 0
						})
					}

					// Record output order from first config
					if !orderDecided {
						outputOrder = append(outputOrder, indices...)
					}

					partLen := len(indices)

					// For each row in the sorted partition, compute all specs
					for pos, origIdx := range indices {
						for _, spec := range cfg.Specs {
							val := computeWindowFunc(spec.Function, all, indices, pos, partLen, frame, cfg.OrderBy)
							results[origIdx][spec.ResultName] = val
						}
					}
				}

				if !orderDecided {
					orderDecided = true
				}
			}

			// If no config had OrderBy, use original input order
			if outputOrder == nil {
				outputOrder = make([]int, n)
				for i := range outputOrder {
					outputOrder[i] = i
				}
			}

			// 3. Emit records in output order with computed fields
			for _, origIdx := range outputOrder {
				r := all[origIdx]
				mut := r.ToMutable()
				for field, val := range results[origIdx] {
					if val == nil {
						mut.fields[field] = nil
					} else {
						mut = applyWindowValue(mut, field, val)
					}
				}
				if !yield(mut.Freeze()) {
					return
				}
			}
		}
	}
}

// computeWindowFunc computes a single window function value for one row.
// indices is the sorted partition (original record indices).
// pos is the position within the sorted partition.
func computeWindowFunc(fn WindowFunc, all []Record, indices []int, pos, partLen int, frame WindowFrame, orderBy []OrderField) any {
	switch f := fn.(type) {
	case wRowNumber:
		return int64(pos + 1)

	case wRank:
		// Same as pos+1, but ties get the same rank
		rank := 1
		for i := range pos {
			if CompareRecordFields(all[indices[i]], all[indices[pos]], orderBy) != 0 {
				rank = i + 1
			}
		}
		if pos > 0 && CompareRecordFields(all[indices[pos-1]], all[indices[pos]], orderBy) == 0 {
			// Same as previous — find the rank of the first in this tie group
			for i := pos - 1; i >= 0; i-- {
				if i == 0 || CompareRecordFields(all[indices[i-1]], all[indices[pos]], orderBy) != 0 {
					rank = i + 1
					break
				}
			}
		} else {
			rank = pos + 1
		}
		return int64(rank)

	case wDenseRank:
		denseRank := 1
		for i := 1; i <= pos; i++ {
			if CompareRecordFields(all[indices[i-1]], all[indices[i]], orderBy) != 0 {
				denseRank++
			}
		}
		return int64(denseRank)

	case wNtile:
		if f.N <= 0 {
			return int64(1)
		}
		// NTILE distributes rows into f.N roughly-equal buckets
		return int64((pos*f.N)/partLen) + 1

	case wPercentRank:
		if partLen <= 1 {
			return float64(0)
		}
		// PERCENT_RANK = (rank - 1) / (partition_size - 1)
		// Compute rank first (same logic as wRank)
		rank := pos + 1
		if pos > 0 && CompareRecordFields(all[indices[pos-1]], all[indices[pos]], orderBy) == 0 {
			for i := pos - 1; i >= 0; i-- {
				if i == 0 || CompareRecordFields(all[indices[i-1]], all[indices[pos]], orderBy) != 0 {
					rank = i + 1
					break
				}
			}
		}
		return float64(rank-1) / float64(partLen-1)

	case wLag:
		// LAG ignores frame — always indexes into full partition
		srcPos := pos - f.Offset
		if srcPos < 0 || srcPos >= partLen {
			return nil
		}
		v, _ := Get[any](all[indices[srcPos]], f.Field)
		return v

	case wLead:
		srcPos := pos + f.Offset
		if srcPos < 0 || srcPos >= partLen {
			return nil
		}
		v, _ := Get[any](all[indices[srcPos]], f.Field)
		return v

	case wFirst:
		start := frameStart(pos, partLen, frame)
		v, _ := Get[any](all[indices[start]], f.Field)
		return v

	case wLast:
		end := frameEnd(pos, partLen, frame)
		v, _ := Get[any](all[indices[end]], f.Field)
		return v

	case wSum:
		start := frameStart(pos, partLen, frame)
		end := frameEnd(pos, partLen, frame)
		var sum float64
		for i := start; i <= end; i++ {
			if v, ok := Get[float64](all[indices[i]], f.Field); ok {
				sum += v
			}
		}
		return sum

	case wAvg:
		start := frameStart(pos, partLen, frame)
		end := frameEnd(pos, partLen, frame)
		var sum float64
		var count int
		for i := start; i <= end; i++ {
			if v, ok := Get[float64](all[indices[i]], f.Field); ok {
				sum += v
				count++
			}
		}
		if count == 0 {
			return float64(0)
		}
		return sum / float64(count)

	case wCount:
		start := frameStart(pos, partLen, frame)
		end := frameEnd(pos, partLen, frame)
		return int64(end - start + 1)

	case wMin:
		start := frameStart(pos, partLen, frame)
		end := frameEnd(pos, partLen, frame)
		var minVal any
		for i := start; i <= end; i++ {
			v, _ := Get[any](all[indices[i]], f.Field)
			if minVal == nil || CompareAny(v, minVal) < 0 {
				minVal = v
			}
		}
		return minVal

	case wMax:
		start := frameStart(pos, partLen, frame)
		end := frameEnd(pos, partLen, frame)
		var maxVal any
		for i := start; i <= end; i++ {
			v, _ := Get[any](all[indices[i]], f.Field)
			if maxVal == nil || CompareAny(v, maxVal) > 0 {
				maxVal = v
			}
		}
		return maxVal
	}

	return nil
}

// frameStart returns the first index in the frame for position pos.
func frameStart(pos, _ int, frame WindowFrame) int {
	if frame.Preceding < 0 {
		return 0 // UNBOUNDED PRECEDING
	}
	start := pos - frame.Preceding
	if start < 0 {
		return 0
	}
	return start
}

// frameEnd returns the last index (inclusive) in the frame for position pos.
func frameEnd(pos, partLen int, frame WindowFrame) int {
	if frame.Following < 0 {
		return partLen - 1 // UNBOUNDED FOLLOWING
	}
	end := pos + frame.Following
	if end >= partLen {
		return partLen - 1
	}
	return end
}

// applyWindowValue sets a computed window value on a mutable record with appropriate type.
func applyWindowValue(mut MutableRecord, field string, val any) MutableRecord {
	switch v := val.(type) {
	case int64:
		return mut.Int(field, v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return mut.Float(field, v)
		}
		return mut.Float(field, v)
	case string:
		return mut.String(field, v)
	case bool:
		return mut.Bool(field, v)
	default:
		mut.fields[field] = val
		return mut
	}
}

// ============================================================================
// STREAMING WINDOW FUNCTIONS
// ============================================================================

// streamWindowAgg is the interface for incremental window function state.
type streamWindowAgg interface {
	// update processes one record and returns the current window value.
	// pos is 0-based position within the partition.
	// orderBy fields and prevRecord are used for RANK/DENSE_RANK.
	update(record Record, pos int, orderBy []OrderField, prevRecord *Record) any
	// reset clears state for a new partition.
	reset()
}

// streamWindowLeadSpec holds a LEAD spec that requires delayed emission.
type streamWindowLeadSpec struct {
	field      string
	offset     int
	resultName string
}

// --- Concrete streaming window aggregates ---

type swRowNumber struct{ pos int64 }

func (s *swRowNumber) update(_ Record, _ int, _ []OrderField, _ *Record) any {
	s.pos++
	return s.pos
}
func (s *swRowNumber) reset() { s.pos = 0 }

type swRank struct {
	pos  int64 // 1-based position
	rank int64
}

func (s *swRank) update(record Record, _ int, orderBy []OrderField, prevRecord *Record) any {
	s.pos++
	if prevRecord == nil || CompareRecordFields(*prevRecord, record, orderBy) != 0 {
		s.rank = s.pos
	}
	return s.rank
}
func (s *swRank) reset() { s.pos = 0; s.rank = 0 }

type swDenseRank struct {
	rank int64
}

func (s *swDenseRank) update(record Record, _ int, orderBy []OrderField, prevRecord *Record) any {
	if prevRecord == nil || CompareRecordFields(*prevRecord, record, orderBy) != 0 {
		s.rank++
	}
	return s.rank
}
func (s *swDenseRank) reset() { s.rank = 0 }

type swSum struct {
	field string
	sum   float64
}

func (s *swSum) update(record Record, _ int, _ []OrderField, _ *Record) any {
	if v, ok := Get[float64](record, s.field); ok {
		s.sum += v
	}
	return s.sum
}
func (s *swSum) reset() { s.sum = 0 }

type swAvg struct {
	field string
	sum   float64
	count int64
}

func (s *swAvg) update(record Record, _ int, _ []OrderField, _ *Record) any {
	if v, ok := Get[float64](record, s.field); ok {
		s.sum += v
		s.count++
	}
	if s.count == 0 {
		return float64(0)
	}
	return s.sum / float64(s.count)
}
func (s *swAvg) reset() { s.sum = 0; s.count = 0 }

type swCount struct{ count int64 }

func (s *swCount) update(_ Record, _ int, _ []OrderField, _ *Record) any {
	s.count++
	return s.count
}
func (s *swCount) reset() { s.count = 0 }

type swFirst struct {
	field    string
	value    any
	captured bool
}

func (s *swFirst) update(record Record, _ int, _ []OrderField, _ *Record) any {
	if !s.captured {
		s.value, _ = Get[any](record, s.field)
		s.captured = true
	}
	return s.value
}
func (s *swFirst) reset() { s.value = nil; s.captured = false }

type swLast struct{ field string }

func (s *swLast) update(record Record, _ int, _ []OrderField, _ *Record) any {
	v, _ := Get[any](record, s.field)
	return v
}
func (s *swLast) reset() {}

// --- Sliding window aggregates (bounded frames, ROWS N,0) ---

// swSlidingSum maintains a ring buffer for sliding SUM.
type swSlidingSum struct {
	field  string
	ring   []float64
	size   int // frame size = preceding + 1
	idx    int // write position in ring
	sum    float64
	filled int // how many slots filled so far
}

func (s *swSlidingSum) update(record Record, _ int, _ []OrderField, _ *Record) any {
	val := float64(0)
	if v, ok := Get[float64](record, s.field); ok {
		val = v
	}
	if s.filled >= s.size {
		s.sum -= s.ring[s.idx]
	}
	s.ring[s.idx] = val
	s.sum += val
	s.idx = (s.idx + 1) % s.size
	if s.filled < s.size {
		s.filled++
	}
	return s.sum
}

func (s *swSlidingSum) reset() {
	for i := range s.ring {
		s.ring[i] = 0
	}
	s.idx = 0
	s.sum = 0
	s.filled = 0
}

// swSlidingAvg maintains a ring buffer for sliding AVG.
type swSlidingAvg struct {
	field  string
	ring   []float64
	has    []bool // tracks which slots have valid values
	size   int
	idx    int
	sum    float64
	count  int
	filled int
}

func (s *swSlidingAvg) update(record Record, _ int, _ []OrderField, _ *Record) any {
	val, ok := Get[float64](record, s.field)
	if s.filled >= s.size {
		// Evict oldest
		if s.has[s.idx] {
			s.sum -= s.ring[s.idx]
			s.count--
		}
	}
	s.ring[s.idx] = val
	s.has[s.idx] = ok
	if ok {
		s.sum += val
		s.count++
	}
	s.idx = (s.idx + 1) % s.size
	if s.filled < s.size {
		s.filled++
	}
	if s.count == 0 {
		return float64(0)
	}
	return s.sum / float64(s.count)
}

func (s *swSlidingAvg) reset() {
	for i := range s.ring {
		s.ring[i] = 0
		s.has[i] = false
	}
	s.idx = 0
	s.sum = 0
	s.count = 0
	s.filled = 0
}

// swSlidingCount returns min(pos+1, frameSize).
type swSlidingCount struct {
	frameSize int
	count     int64
}

func (s *swSlidingCount) update(_ Record, _ int, _ []OrderField, _ *Record) any {
	if s.count < int64(s.frameSize) {
		s.count++
	}
	return s.count
}

func (s *swSlidingCount) reset() { s.count = 0 }

// swSlidingFirst maintains a ring buffer for sliding FIRST.
type swSlidingFirst struct {
	field  string
	ring   []any
	size   int
	idx    int
	filled int
}

func (s *swSlidingFirst) update(record Record, _ int, _ []OrderField, _ *Record) any {
	val, _ := Get[any](record, s.field)
	s.ring[s.idx] = val
	s.idx = (s.idx + 1) % s.size
	if s.filled < s.size {
		s.filled++
	}
	// Oldest element is at idx when full, or at 0 when not yet full
	if s.filled < s.size {
		return s.ring[0]
	}
	return s.ring[s.idx] // idx just advanced past newest, so it points to oldest
}

func (s *swSlidingFirst) reset() {
	for i := range s.ring {
		s.ring[i] = nil
	}
	s.idx = 0
	s.filled = 0
}

// swRunningMin tracks the running minimum (default frame, values never leave).
type swRunningMin struct {
	field  string
	minVal any
}

func (s *swRunningMin) update(record Record, _ int, _ []OrderField, _ *Record) any {
	v, _ := Get[any](record, s.field)
	if s.minVal == nil || CompareAny(v, s.minVal) < 0 {
		s.minVal = v
	}
	return s.minVal
}

func (s *swRunningMin) reset() { s.minVal = nil }

// swRunningMax tracks the running maximum (default frame, values never leave).
type swRunningMax struct {
	field  string
	maxVal any
}

func (s *swRunningMax) update(record Record, _ int, _ []OrderField, _ *Record) any {
	v, _ := Get[any](record, s.field)
	if s.maxVal == nil || CompareAny(v, s.maxVal) > 0 {
		s.maxVal = v
	}
	return s.maxVal
}

func (s *swRunningMax) reset() { s.maxVal = nil }

// dequeEntry stores a value and its position for monotonic deque operations.
type dequeEntry struct {
	pos int
	val any
}

// swSlidingMin uses a monotonic deque (ascending) for O(1) amortized MIN.
type swSlidingMin struct {
	field string
	size  int
	deque []dequeEntry
}

func (s *swSlidingMin) update(record Record, pos int, _ []OrderField, _ *Record) any {
	v, _ := Get[any](record, s.field)
	// Pop back entries >= new value (maintain ascending order)
	for len(s.deque) > 0 && CompareAny(s.deque[len(s.deque)-1].val, v) >= 0 {
		s.deque = s.deque[:len(s.deque)-1]
	}
	s.deque = append(s.deque, dequeEntry{pos: pos, val: v})
	// Pop front entries outside frame
	for len(s.deque) > 0 && s.deque[0].pos <= pos-s.size {
		s.deque = s.deque[1:]
	}
	return s.deque[0].val
}

func (s *swSlidingMin) reset() { s.deque = s.deque[:0] }

// swSlidingMax uses a monotonic deque (descending) for O(1) amortized MAX.
type swSlidingMax struct {
	field string
	size  int
	deque []dequeEntry
}

func (s *swSlidingMax) update(record Record, pos int, _ []OrderField, _ *Record) any {
	v, _ := Get[any](record, s.field)
	// Pop back entries <= new value (maintain descending order)
	for len(s.deque) > 0 && CompareAny(s.deque[len(s.deque)-1].val, v) <= 0 {
		s.deque = s.deque[:len(s.deque)-1]
	}
	s.deque = append(s.deque, dequeEntry{pos: pos, val: v})
	// Pop front entries outside frame
	for len(s.deque) > 0 && s.deque[0].pos <= pos-s.size {
		s.deque = s.deque[1:]
	}
	return s.deque[0].val
}

func (s *swSlidingMax) reset() { s.deque = s.deque[:0] }

// swLag uses a ring buffer for LAG(field, offset).
type swLag struct {
	field  string
	offset int
	ring   []any
	idx    int
	filled int
}

func (s *swLag) update(record Record, _ int, _ []OrderField, _ *Record) any {
	val, _ := Get[any](record, s.field)
	size := s.offset + 1
	// Store current value
	s.ring[s.idx] = val
	s.idx = (s.idx + 1) % size
	if s.filled < size {
		s.filled++
	}
	// Return value from offset positions ago
	if s.filled <= s.offset {
		return nil // not enough history yet
	}
	// The value we want is at (idx) mod size — it's the oldest in the ring
	return s.ring[s.idx%size]
}

func (s *swLag) reset() {
	for i := range s.ring {
		s.ring[i] = nil
	}
	s.idx = 0
	s.filled = 0
}

// isBoundedFrame returns true if the frame has a fixed Preceding and Following=0.
func isBoundedFrame(frame WindowFrame) bool {
	return frame.Preceding >= 0 && frame.Following == 0
}

// newStreamWindowAgg creates a streaming aggregate for a supported window function.
// The frame parameter determines whether to use running (default frame) or sliding (bounded) variants.
// Returns (nil, nil) for LEAD specs — they are handled separately via delayed emission.
func newStreamWindowAgg(fn WindowFunc, frame WindowFrame) (streamWindowAgg, error) {
	bounded := isBoundedFrame(frame)
	frameSize := frame.Preceding + 1 // only valid when bounded

	switch f := fn.(type) {
	case wRowNumber:
		return &swRowNumber{}, nil
	case wRank:
		return &swRank{}, nil
	case wDenseRank:
		return &swDenseRank{}, nil
	case wSum:
		if bounded {
			return &swSlidingSum{field: f.Field, ring: make([]float64, frameSize), size: frameSize}, nil
		}
		return &swSum{field: f.Field}, nil
	case wAvg:
		if bounded {
			return &swSlidingAvg{field: f.Field, ring: make([]float64, frameSize), has: make([]bool, frameSize), size: frameSize}, nil
		}
		return &swAvg{field: f.Field}, nil
	case wCount:
		if bounded {
			return &swSlidingCount{frameSize: frameSize}, nil
		}
		return &swCount{}, nil
	case wFirst:
		if bounded {
			return &swSlidingFirst{field: f.Field, ring: make([]any, frameSize), size: frameSize}, nil
		}
		return &swFirst{field: f.Field}, nil
	case wLast:
		return &swLast{field: f.Field}, nil
	case wMin:
		if bounded {
			return &swSlidingMin{field: f.Field, size: frameSize}, nil
		}
		return &swRunningMin{field: f.Field}, nil
	case wMax:
		if bounded {
			return &swSlidingMax{field: f.Field, size: frameSize}, nil
		}
		return &swRunningMax{field: f.Field}, nil
	case wLag:
		ring := make([]any, f.Offset+1)
		return &swLag{field: f.Field, offset: f.Offset, ring: ring}, nil
	case wLead:
		return nil, nil // handled separately via delayed emission
	case wNtile:
		return nil, fmt.Errorf("NTILE cannot be streamed (requires partition size)")
	case wPercentRank:
		return nil, fmt.Errorf("PERCENT_RANK cannot be streamed (requires partition size)")
	default:
		return nil, fmt.Errorf("unknown window function type: %T", fn)
	}
}

// canStreamWindow checks if a single WindowConfig can be computed in streaming mode.
func canStreamWindow(cfg WindowConfig) error {
	if cfg.Frame.Following < 0 {
		return fmt.Errorf("streaming window does not support UNBOUNDED FOLLOWING")
	}
	if cfg.Frame.Following > 0 {
		return fmt.Errorf("streaming window does not yet support Following > 0; use Window() instead")
	}
	for _, spec := range cfg.Specs {
		if _, err := newStreamWindowAgg(spec.Function, cfg.Frame); err != nil {
			return err
		}
	}
	return nil
}

// samePartitionOrder checks if two configs have the same partition and order fields.
func samePartitionOrder(a, b WindowConfig) bool {
	if len(a.PartitionBy) != len(b.PartitionBy) {
		return false
	}
	for i := range a.PartitionBy {
		if a.PartitionBy[i] != b.PartitionBy[i] {
			return false
		}
	}
	if len(a.OrderBy) != len(b.OrderBy) {
		return false
	}
	for i := range a.OrderBy {
		if a.OrderBy[i] != b.OrderBy[i] {
			return false
		}
	}
	return true
}

// validateStreamWindowConfigs validates that all configs can stream and share the same partition/order.
func validateStreamWindowConfigs(configs []WindowConfig) error {
	for i, cfg := range configs {
		if err := canStreamWindow(cfg); err != nil {
			return fmt.Errorf("clause %d: %w", i+1, err)
		}
		if i > 0 && !samePartitionOrder(configs[0], cfg) {
			return fmt.Errorf("streaming window requires all clauses to share the same partition and order fields")
		}
	}
	return nil
}

// StreamWindow applies window functions in streaming mode with O(1) memory per aggregate.
// Input MUST be presorted by partition fields then order fields.
// Supports default frame (UNBOUNDED PRECEDING TO CURRENT ROW) and bounded frames (ROWS N,0).
// LAG emits immediately via ring buffer. LEAD uses delayed emission with a lookahead buffer.
// For Following > 0 or unsorted input, use Window() instead.
func StreamWindow(configs []WindowConfig) (Filter[Record, Record], error) {
	if err := validateStreamWindowConfigs(configs); err != nil {
		return nil, err
	}

	// Pre-build all streaming aggregates, separating LEAD specs
	var regularAggs []swSpecAgg
	var leadSpecs []streamWindowLeadSpec
	maxLeadOffset := 0

	for _, cfg := range configs {
		for _, spec := range cfg.Specs {
			agg, _ := newStreamWindowAgg(spec.Function, cfg.Frame) // already validated
			if agg == nil {
				// LEAD — handled via delayed emission
				lead := spec.Function.(wLead)
				leadSpecs = append(leadSpecs, streamWindowLeadSpec{
					field:      lead.Field,
					offset:     lead.Offset,
					resultName: spec.ResultName,
				})
				if lead.Offset > maxLeadOffset {
					maxLeadOffset = lead.Offset
				}
			} else {
				regularAggs = append(regularAggs, swSpecAgg{resultName: spec.ResultName, agg: agg})
			}
		}
	}

	// Use first config's partition/order (all are the same after validation)
	partitionBy := configs[0].PartitionBy
	orderBy := configs[0].OrderBy

	if len(leadSpecs) == 0 {
		// No LEAD — immediate emission path (same as Phase 1 but with new agg types)
		return streamWindowImmediate(regularAggs, partitionBy, orderBy), nil
	}

	// LEAD present — delayed emission path
	return streamWindowDelayed(regularAggs, leadSpecs, maxLeadOffset, partitionBy, orderBy), nil
}

// streamWindowImmediate emits records immediately (no LEAD specs).
func streamWindowImmediate(allAggs []swSpecAgg, partitionBy []string, orderBy []OrderField) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var prevKey string
			var prevRecord *Record
			firstRecord := true
			pos := 0

			for record := range input {
				key := buildGroupKey(record, partitionBy)

				if firstRecord {
					firstRecord = false
					prevKey = key
				} else if key != prevKey {
					for _, sa := range allAggs {
						sa.agg.reset()
					}
					prevRecord = nil
					prevKey = key
					pos = 0
				}

				mut := record.ToMutable()
				for _, sa := range allAggs {
					val := sa.agg.update(record, pos, orderBy, prevRecord)
					if val == nil {
						mut.fields[sa.resultName] = nil
					} else {
						mut = applyWindowValue(mut, sa.resultName, val)
					}
				}

				r := mut.Freeze()
				prevRecord = &r
				pos++

				if !yield(r) {
					return
				}
			}
		}
	}
}

// swSpecAgg pairs a result name with its streaming aggregate.
type swSpecAgg struct {
	resultName string
	agg        streamWindowAgg
}

// pendingRecord holds a record with its pre-computed regular agg values, awaiting LEAD values.
type pendingRecord struct {
	record    Record
	aggValues map[string]any
}

// streamWindowDelayed handles LEAD specs by buffering records for lookahead.
func streamWindowDelayed(regularAggs []swSpecAgg, leadSpecs []streamWindowLeadSpec, maxLeadOffset int, partitionBy []string, orderBy []OrderField) Filter[Record, Record] {
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			var prevKey string
			var prevRecord *Record
			firstRecord := true
			pos := 0
			var buf []pendingRecord

			emitRecord := func(pr pendingRecord, bufIdx int, bufLen int) bool {
				mut := pr.record.ToMutable()
				// Apply regular agg values
				for name, val := range pr.aggValues {
					if val == nil {
						mut.fields[name] = nil
					} else {
						mut = applyWindowValue(mut, name, val)
					}
				}
				// Apply LEAD values
				for _, ls := range leadSpecs {
					lookIdx := bufIdx + ls.offset
					if lookIdx < bufLen {
						v, _ := Get[any](buf[lookIdx].record, ls.field)
						if v == nil {
							mut.fields[ls.resultName] = nil
						} else {
							mut = applyWindowValue(mut, ls.resultName, v)
						}
					} else {
						mut.fields[ls.resultName] = nil
					}
				}
				return yield(mut.Freeze())
			}

			flushBuf := func() bool {
				for i := range buf {
					if !emitRecord(buf[i], i, len(buf)) {
						buf = buf[:0]
						return false
					}
				}
				buf = buf[:0]
				return true
			}

			for record := range input {
				key := buildGroupKey(record, partitionBy)

				if firstRecord {
					firstRecord = false
					prevKey = key
				} else if key != prevKey {
					// Partition changed — flush buffer, reset
					if !flushBuf() {
						return
					}
					for _, sa := range regularAggs {
						sa.agg.reset()
					}
					prevRecord = nil
					prevKey = key
					pos = 0
				}

				// Compute regular aggs for this record
				vals := make(map[string]any, len(regularAggs))
				for _, sa := range regularAggs {
					vals[sa.resultName] = sa.agg.update(record, pos, orderBy, prevRecord)
				}
				buf = append(buf, pendingRecord{record: record, aggValues: vals})

				// Emit oldest when buffer exceeds delay
				if len(buf) > maxLeadOffset {
					if !emitRecord(buf[0], 0, len(buf)) {
						return
					}
					buf = buf[1:]
				}

				prevRecord2 := record
				prevRecord = &prevRecord2
				pos++
			}

			// Flush remaining records (LEAD values will be nil for tail)
			flushBuf()
		}
	}
}

// MustStreamWindow is like StreamWindow but panics on error.
// Useful in generated code where configs are known-valid at generation time.
func MustStreamWindow(configs []WindowConfig) Filter[Record, Record] {
	f, err := StreamWindow(configs)
	if err != nil {
		panic(fmt.Sprintf("MustStreamWindow: %v", err))
	}
	return f
}
