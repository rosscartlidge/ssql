package ssql

import (
	"fmt"
	"iter"
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
	merged := make(map[string]any, len(left.fields)+len(right.fields))
	// Copy left fields directly
	for k, v := range left.fields {
		merged[k] = v
	}
	// Copy right fields (may override left on field name collision)
	for k, v := range right.fields {
		merged[k] = v
	}
	return Record{fields: merged}
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
		leftVal, leftExists := left.fields[field]
		rightVal, rightExists := right.fields[field]
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
		val, exists := r.fields[field]
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
	leftVal, leftExists := left.fields[p.leftField]
	rightVal, rightExists := right.fields[p.rightField]
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
	if val, exists := r.fields[p.leftField]; exists {
		return fmt.Sprintf("%v", val), true
	}
	// Fall back to right field (for build phase)
	if val, exists := r.fields[p.rightField]; exists {
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
					if val, exists := r.fields[clause.RightField]; exists {
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
						leftVal, exists := current.fields[clause.LeftField]
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
	merged := make(map[string]any, len(left.fields)+len(renames))
	for k, v := range left.fields {
		merged[k] = v
	}

	if len(renames) == 0 {
		// No renames specified - copy all right fields (standard merge)
		for k, v := range right.fields {
			merged[k] = v
		}
	} else {
		// Only copy specified fields with new names
		for rightField, newName := range renames {
			if val, exists := right.fields[rightField]; exists {
				merged[newName] = val
			}
		}
	}

	return Record{fields: merged}
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
	return func(input iter.Seq[Record]) iter.Seq[Record] {
		return func(yield func(Record) bool) {
			groups := make(map[string][]Record)
			groupFields := make(map[string]Record)
			var keys []string

			// Collect all records into groups
			for record := range input {
				var keyParts []string
				groupingFields := MakeMutableRecord()
				hasComplexField := false

				for _, field := range fields {
					if val, exists := record.fields[field]; exists {
						// Validate that the field value is simple (no iter.Seq or Record)
						if !isSimpleValue(val) {
							// Skip this entire record if any grouping field is complex
							hasComplexField = true
							break
						}
						keyParts = append(keyParts, fmt.Sprintf("%v", val))
						groupingFields.fields[field] = val
					} else {
						keyParts = append(keyParts, "<nil>")
						groupingFields.fields[field] = nil
					}
				}

				// Skip records with complex grouping field values
				if hasComplexField {
					continue
				}

				key := fmt.Sprintf("[%s]", strings.Join(keyParts, ","))
				if _, exists := groups[key]; !exists {
					keys = append(keys, key)
					groupFields[key] = groupingFields.Freeze()
				}
				groups[key] = append(groups[key], record)
			}

			// Yield records with grouping fields + sequence field
			for _, key := range keys {
				result := MakeMutableRecord()

				// Copy the grouping field values
				for k, v := range groupFields[key].All() {
					result.fields[k] = v
				}

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
				if seqValue, exists := record.fields[sequenceField]; exists {
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
			if val, ok := record.fields[field]; ok && val != nil {
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
