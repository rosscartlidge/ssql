package ssql

import (
	"errors"
	"testing"
)

// DFC142 step 3: one conversion of a pipeline panic to an error.

func TestRecoverKeepsErrorValues(t *testing.T) {
	run := func() (err error) {
		defer Recover(&err)
		for range panicOnThird() {
		}
		return nil
	}
	err := run()
	var ce *CellError
	if !errors.As(err, &ce) || ce.Row != 3 {
		t.Fatalf("Recover gave %v, want the row-3 *CellError", err)
	}
}

func TestRecoverWrapsNonErrorValues(t *testing.T) {
	run := func() (err error) {
		defer Recover(&err)
		panic("not an error")
	}
	err := run()
	if err == nil || err.Error() != "pipeline panic: not an error" {
		t.Fatalf("Recover gave %v, want the wrapped string", err)
	}
}

func TestRecoverNoPanicIsNoop(t *testing.T) {
	already := errors.New("already")
	run := func() (err error) {
		defer Recover(&err)
		return already
	}
	if err := run(); err != already {
		t.Fatalf("Recover changed a returned error: %v", err)
	}
}

func TestRunReturnsPanicAsError(t *testing.T) {
	err := Run(func() error {
		for range panicOnThird() {
		}
		return nil
	})
	var ce *CellError
	if !errors.As(err, &ce) {
		t.Fatalf("Run gave %v, want the *CellError", err)
	}
	if err := Run(func() error { return nil }); err != nil {
		t.Fatalf("Run on a clean body: %v", err)
	}
	want := errors.New("mine")
	if err := Run(func() error { return want }); err != want {
		t.Fatalf("Run replaced the body's error: %v", err)
	}
}
