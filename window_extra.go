package ssql

// Window functions added by DFC130 unit 1: the rest of the SQL ranking
// and offset families.
//
//   - CUME_DIST: rows with an order value ≤ the current row's, over the
//     partition size (peers count together), in (0, 1].
//   - NTH_VALUE(field, n): the n-th row's value within the FRAME (1-based);
//     absent until the frame holds n rows.
//   - LAG/LEAD with a DEFAULT: the default stands in where LAG/LEAD would
//     have no row (SQL's third argument).
//   - COUNT(field): rows in the frame where the field is present — the
//     non-null count, where -count is COUNT(*).

type wCumeDist struct{}
type wNthValue struct {
	Field string
	N     int
}
type wLagDefault struct {
	Field   string
	Offset  int
	Default any
}
type wLeadDefault struct {
	Field   string
	Offset  int
	Default any
}
type wCountField struct{ Field string }

func (wCumeDist) windowFunc()    {}
func (wNthValue) windowFunc()    {}
func (wLagDefault) windowFunc()  {}
func (wLeadDefault) windowFunc() {}
func (wCountField) windowFunc()  {}

// WCumeDist is SQL's CUME_DIST(): the fraction of the partition whose order
// value is ≤ the current row's (ties count together).
func WCumeDist() WindowFunc { return wCumeDist{} }

// WNthValue is NTH_VALUE(field, n): the value of the n-th row (1-based) in
// the frame, absent while the frame has fewer than n rows.
func WNthValue(field string, n int) WindowFunc { return wNthValue{Field: field, N: n} }

// WLagDefault is LAG(field, offset, default): like WLag, with def where no
// earlier row exists.
func WLagDefault(field string, offset int, def any) WindowFunc {
	return wLagDefault{Field: field, Offset: offset, Default: def}
}

// WLeadDefault is LEAD(field, offset, default).
func WLeadDefault(field string, offset int, def any) WindowFunc {
	return wLeadDefault{Field: field, Offset: offset, Default: def}
}

// WCountField is COUNT(field): rows in the frame where field is present.
func WCountField(field string) WindowFunc { return wCountField{Field: field} }

// ---- streaming aggregators ----

// swCountField counts present values (unbounded frame).
type swCountField struct {
	field string
	count int64
}

func (s *swCountField) update(record Record, _ int, _ []OrderField, _ *Record) any {
	if v, ok := Get[any](record, s.field); ok && v != nil {
		s.count++
	}
	return s.count
}
func (s *swCountField) reset() { s.count = 0 }

// swSlidingCountField counts present values over a ROWS frame of `size`.
type swSlidingCountField struct {
	field  string
	ring   []bool
	size   int
	idx    int
	filled int
	count  int64
}

func (s *swSlidingCountField) update(record Record, _ int, _ []OrderField, _ *Record) any {
	v, ok := Get[any](record, s.field)
	present := ok && v != nil
	if s.filled == s.size {
		if s.ring[s.idx] {
			s.count--
		}
	} else {
		s.filled++
	}
	s.ring[s.idx] = present
	if present {
		s.count++
	}
	s.idx = (s.idx + 1) % s.size
	return s.count
}
func (s *swSlidingCountField) reset() {
	s.idx, s.filled, s.count = 0, 0, 0
	for i := range s.ring {
		s.ring[i] = false
	}
}

// swNthValue captures the n-th value seen in the partition (unbounded
// preceding frame): absent until then, then constant.
type swNthValue struct {
	field string
	n     int
	seen  int
	value any
}

func (s *swNthValue) update(record Record, _ int, _ []OrderField, _ *Record) any {
	s.seen++
	if s.seen == s.n {
		s.value, _ = Get[any](record, s.field)
	}
	if s.seen < s.n {
		return nil
	}
	return s.value
}
func (s *swNthValue) reset() { s.seen, s.value = 0, nil }

// swSlidingNthValue is NTH_VALUE over a ROWS frame of `size`: the n-th
// oldest value in the ring, absent while the frame holds fewer than n.
type swSlidingNthValue struct {
	field  string
	n      int
	ring   []any
	size   int
	idx    int
	filled int
}

func (s *swSlidingNthValue) update(record Record, _ int, _ []OrderField, _ *Record) any {
	v, _ := Get[any](record, s.field)
	s.ring[s.idx] = v
	s.idx = (s.idx + 1) % s.size
	if s.filled < s.size {
		s.filled++
	}
	if s.filled < s.n {
		return nil
	}
	// Oldest element is at idx when full, else at 0.
	oldest := 0
	if s.filled == s.size {
		oldest = s.idx
	}
	return s.ring[(oldest+s.n-1)%s.size]
}
func (s *swSlidingNthValue) reset() {
	s.idx, s.filled = 0, 0
	for i := range s.ring {
		s.ring[i] = nil
	}
}

// swLagDefault is swLag with a default where no history exists.
type swLagDefault struct {
	swLag
	def any
}

func (s *swLagDefault) update(record Record, pos int, order []OrderField, prev *Record) any {
	v := s.swLag.update(record, pos, order, prev)
	if v == nil {
		return s.def
	}
	return v
}
