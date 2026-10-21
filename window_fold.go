package ssql

import (
	"fmt"
	"maps"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// exprFold is a compiled init / every / final fold — the engine behind
// `group-by -stream-expr` (StreamExprAgg) and `window -stream-expr`
// (WStreamExpr). The semantics are a map-state fold:
//
//	state = eval(INIT)                                 // must be an object
//	for each record: state = eval(EVERY, state ∪ record) // the record SHADOWS state
//	result = eval(FINAL, state)                         // state fields only
//
// The three programs compile once and are shared by every group, frame
// and partition the fold runs over: EVERY needs a record's field names to
// compile (an unknown field is a loud compile error, the way every
// expression flag behaves), so it compiles on the first step; FINAL on
// the first result. Until v4.121.2 StreamExprAgg recompiled all three for
// every group.
type exprFold struct {
	initExpr, everyExpr, finalExpr string

	mu    sync.Mutex
	init  *vm.Program
	every *vm.Program
	final *vm.Program
}

func newExprFold(initExpr, everyExpr, finalExpr string) *exprFold {
	return &exprFold{initExpr: initExpr, everyExpr: everyExpr, finalExpr: finalExpr}
}

// begin runs INIT and returns a fresh state.
func (f *exprFold) begin() (map[string]any, error) {
	f.mu.Lock()
	if f.init == nil {
		prog, err := expr.Compile(f.initExpr, ExprStandardOptions()...)
		if err != nil {
			f.mu.Unlock()
			return nil, fmt.Errorf("compiling init expression: %w", err)
		}
		f.init = prog
	}
	prog := f.init
	f.mu.Unlock()

	state, err := expr.Run(prog, nil)
	if err != nil {
		return nil, fmt.Errorf("evaluating init expression: %w", err)
	}
	stateMap, ok := state.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("init expression must return object, got %T", state)
	}
	return stateMap, nil
}

// step folds one record into state and returns the new state. The record's
// fields shadow state keys of the same name, as they always have.
func (f *exprFold) step(state map[string]any, record Record) (map[string]any, error) {
	env := make(map[string]any, len(state)+record.Len())
	maps.Copy(env, state)
	maps.Insert(env, record.All())

	f.mu.Lock()
	if f.every == nil {
		prog, err := expr.Compile(f.everyExpr, append([]expr.Option{expr.Env(env)}, ExprStandardOptions()...)...)
		if err != nil {
			f.mu.Unlock()
			return nil, fmt.Errorf("compiling every expression: %w", err)
		}
		f.every = prog
	}
	prog := f.every
	f.mu.Unlock()

	newState, err := expr.Run(prog, env)
	if err != nil {
		return nil, fmt.Errorf("evaluating every expression: %w", err)
	}
	stateMap, ok := newState.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("every expression must return object, got %T", newState)
	}
	return stateMap, nil
}

// result evaluates FINAL over the state (state fields only, like the VM
// always did: a record field in FINAL is an unknown name).
func (f *exprFold) result(state map[string]any) (any, error) {
	f.mu.Lock()
	if f.final == nil {
		prog, err := expr.Compile(f.finalExpr, append([]expr.Option{expr.Env(state)}, ExprStandardOptions()...)...)
		if err != nil {
			f.mu.Unlock()
			return nil, fmt.Errorf("compiling final expression: %w", err)
		}
		f.final = prog
	}
	prog := f.final
	f.mu.Unlock()

	result, err := expr.Run(prog, state)
	if err != nil {
		return nil, fmt.Errorf("evaluating final expression: %w", err)
	}
	return result, nil
}

// run folds a whole group — StreamExprAgg's body.
func (f *exprFold) run(records []Record) (any, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("no records to process")
	}
	state, err := f.begin()
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if state, err = f.step(state, r); err != nil {
			return nil, err
		}
	}
	return f.result(state)
}

// ============================================================================
// window -stream-expr: a fold over the frame
// ============================================================================

// wStreamExpr is `window -stream-expr INIT EVERY FINAL`: the fold runs
// over the frame's rows in frame order and FINAL is the row's value. Over
// the default frame (UNBOUNDED PRECEDING → CURRENT ROW) that is a running
// fold — a recurrence that sees the previous state at every row, which is
// what the aggregate functions cannot express (Fibonacci: `{a:0, b:1}`
// `{a:b, b:a+b}` `b`). Over a bounded frame the fold restarts at the
// frame's first row.
type wStreamExpr struct {
	Init, Every, Final string
	fold               *exprFold // compiled once, shared by every frame and partition
}

func (wStreamExpr) windowFunc() {}

// WStreamExpr makes a window function from a fold — the window form of
// StreamExprAgg; see wStreamExpr. A nil FINAL is the absent value (SQL's
// NULL): a fold that carries a previous row has nothing at the first.
func WStreamExpr(initExpr, everyExpr, finalExpr string) WindowFunc {
	return wStreamExpr{Init: initExpr, Every: everyExpr, Final: finalExpr, fold: newExprFold(initExpr, everyExpr, finalExpr)}
}

// value normalises a fold result the way aggResult types an aggregation
// result (int64 / float64 / string / bool / time / JSON text); nil stays
// nil — the absent value.
func (f wStreamExpr) value(v any) any {
	if v == nil {
		return nil
	}
	return aggValue("WStreamExpr", v)
}

// fail turns a fold error into the panic every aggregation expression
// raises on a wrong expression — loud, never a silent 0.
func (f wStreamExpr) fail(err error) {
	panic(fmt.Errorf("WStreamExpr(%q, %q, %q): %w", f.Init, f.Every, f.Final, err))
}

// foldFrame folds the frame [start, end] of the sorted partition.
func (f wStreamExpr) foldFrame(all []Record, indices []int, start, end int) any {
	state, err := f.fold.begin()
	if err != nil {
		f.fail(err)
	}
	for i := start; i <= end; i++ {
		if state, err = f.fold.step(state, all[indices[i]]); err != nil {
			f.fail(err)
		}
	}
	v, err := f.fold.result(state)
	if err != nil {
		f.fail(err)
	}
	return f.value(v)
}

// isRunningFrame reports the default ROWS frame, UNBOUNDED PRECEDING →
// CURRENT ROW: the one frame a fold can carry across rows instead of
// restarting at every row's frame start.
func isRunningFrame(frame WindowFrame) bool {
	return !frame.Range && frame.Preceding < 0 && frame.Following == 0
}

// runningFoldValues computes every -stream-expr spec of cfg whose frame is
// the running frame in ONE pass over the sorted partition (O(n), where
// re-folding each row's frame would be O(n²)): keyed by spec index, one
// value per position. Nil when the frame is not the running one or no
// spec is a fold; the caller computes everything else per row.
func runningFoldValues(cfg WindowConfig, all []Record, indices []int) map[int][]any {
	if !isRunningFrame(cfg.Frame) {
		return nil
	}
	var out map[int][]any
	for si, spec := range cfg.Specs {
		f, ok := spec.Function.(wStreamExpr)
		if !ok {
			continue
		}
		vals := make([]any, len(indices))
		state, err := f.fold.begin()
		if err != nil {
			f.fail(err)
		}
		for pos, idx := range indices {
			if state, err = f.fold.step(state, all[idx]); err != nil {
				f.fail(err)
			}
			v, err := f.fold.result(state)
			if err != nil {
				f.fail(err)
			}
			vals[pos] = f.value(v)
		}
		if out == nil {
			out = make(map[int][]any)
		}
		out[si] = vals
	}
	return out
}

// swFold streams a fold over the running frame: the state carries from
// row to row and resets at a partition boundary.
type swFold struct {
	f     wStreamExpr
	state map[string]any // nil until the partition's first row
}

func (s *swFold) update(record Record, _ int, _ []OrderField, _ *Record) any {
	var err error
	if s.state == nil {
		if s.state, err = s.f.fold.begin(); err != nil {
			s.f.fail(err)
		}
	}
	if s.state, err = s.f.fold.step(s.state, record); err != nil {
		s.f.fail(err)
	}
	v, err := s.f.fold.result(s.state)
	if err != nil {
		s.f.fail(err)
	}
	return s.f.value(v)
}
func (s *swFold) reset() { s.state = nil }

// swSlidingFold streams a fold over a bounded frame (ROWS N PRECEDING →
// CURRENT ROW): a ring of the frame's records, re-folded from the frame's
// first row at every step — a fold has no Remove, so the frame is O(N)
// work per row, still O(N) memory.
type swSlidingFold struct {
	f    wStreamExpr
	ring []Record
	size int
	head int // index of the oldest record
	n    int // records held (≤ size)
}

func (s *swSlidingFold) update(record Record, _ int, _ []OrderField, _ *Record) any {
	if s.n < s.size {
		s.ring[(s.head+s.n)%s.size] = record
		s.n++
	} else {
		s.ring[s.head] = record
		s.head = (s.head + 1) % s.size
	}
	state, err := s.f.fold.begin()
	if err != nil {
		s.f.fail(err)
	}
	for i := 0; i < s.n; i++ {
		if state, err = s.f.fold.step(state, s.ring[(s.head+i)%s.size]); err != nil {
			s.f.fail(err)
		}
	}
	v, err := s.f.fold.result(state)
	if err != nil {
		s.f.fail(err)
	}
	return s.f.value(v)
}
func (s *swSlidingFold) reset() { s.head, s.n = 0, 0 }
