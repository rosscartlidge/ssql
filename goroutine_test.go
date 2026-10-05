package ssql

// DFC142 step 1: no helper that pulls its input in a goroutine of its
// own may let a stage panic escape the caller's recover. Each test
// consumes a source that panics with a *CellError on its third element
// through one helper, inside a function that recovers, and asserts the
// recover saw the CellError. Before the fix every one of these tore the
// test binary down from the helper's goroutine (exit status 2).

import (
	"errors"
	"iter"
	"testing"
	"time"
)

// panicOnThird yields 1, 2 and then panics with a *CellError, the way a
// fail-fast reader does on a bad cell.
func panicOnThird() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 1; ; i++ {
			if i == 3 {
				panic(&CellError{Row: 3, Column: "v", Value: "x", Type: FieldTypeInt})
			}
			if !yield(i) {
				return
			}
		}
	}
}

// recoverFrom runs fn and returns what it panicked with (nil if nothing).
func recoverFrom(fn func()) (r any) {
	defer func() { r = recover() }()
	fn()
	return nil
}

func wantCellError(t *testing.T, r any) {
	t.Helper()
	if r == nil {
		t.Fatal("no panic reached the consumer's recover")
	}
	err, ok := r.(error)
	if !ok {
		t.Fatalf("panic value %T is not an error", r)
	}
	var ce *CellError
	if !errors.As(err, &ce) || ce.Row != 3 {
		t.Fatalf("recovered %v, want the row-3 *CellError", err)
	}
}

func TestLazyTeePanicReachesConsumer(t *testing.T) {
	r := recoverFrom(func() {
		streams := LazyTee(panicOnThird(), 2)
		for range streams[0] {
		}
		for range streams[1] {
		}
	})
	wantCellError(t, r)
}

func TestTimeoutPanicReachesConsumer(t *testing.T) {
	r := recoverFrom(func() {
		for range Timeout[int](5 * time.Second)(panicOnThird()) {
		}
	})
	wantCellError(t, r)
}

func TestToChannelWithErrorsPanicOnErrCh(t *testing.T) {
	itemCh, errCh := ToChannelWithErrors(Safe(panicOnThird()))
	n := 0
	for range itemCh {
		n++
	}
	err := <-errCh
	if n != 2 {
		t.Errorf("got %d items before the failure, want 2", n)
	}
	var ce *CellError
	if !errors.As(err, &ce) {
		t.Fatalf("errCh delivered %v, want the *CellError", err)
	}
}

// Timeout must call yield from the consumer's goroutine (the
// range-over-func contract); a value forwarded from the producer
// goroutine must still arrive in order and the timeout must still cut
// an endless source.
func TestTimeoutForwardsInOrderAndStops(t *testing.T) {
	var got []int
	for v := range Timeout[int](time.Second)(From([]int{1, 2, 3})) {
		got = append(got, v)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("got %v, want [1 2 3]", got)
	}
	endless := func(yield func(int) bool) {
		for i := 0; ; i++ {
			if !yield(i) {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	n := 0
	start := time.Now()
	for range Timeout[int](50 * time.Millisecond)(endless) {
		n++
	}
	if n == 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout did not cut the endless source cleanly (n=%d, %v)", n, time.Since(start))
	}
}

func TestToChannelErrReportsPanic(t *testing.T) {
	ch, wait := ToChannelErr(panicOnThird())
	n := 0
	for range ch {
		n++
	}
	err := wait()
	if n != 2 {
		t.Errorf("got %d items before the failure, want 2", n)
	}
	var ce *CellError
	if !errors.As(err, &ce) {
		t.Fatalf("wait() = %v, want the *CellError", err)
	}
	ch, wait = ToChannelErr(From([]int{1, 2, 3}))
	for range ch {
	}
	if err := wait(); err != nil {
		t.Fatalf("clean sequence: wait() = %v", err)
	}
}
