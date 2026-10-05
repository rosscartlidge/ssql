package ssql

import "fmt"

// The library reports a failure it meets mid-stream — a cell that does
// not fit its column, a value a cast cannot convert, a group that cannot
// be ordered — by panicking with an error value, because an
// iter.Seq[T] has no error slot and a coerced zero is worse than a
// stop (DFC124). Every such value is an error (DFC142 step 2), the data
// errors are typed (*CellError, *LineError, *CastError, *CompareError),
// and the panic surfaces in the goroutine that pulls the pipeline. This
// file is where a program turns that into an ordinary error: the CLI,
// every generated program and an embedding service call Recover or Run
// around the loop that drives the pipeline. Safely (core.go) is the
// same conversion as a FilterWithErrors, for code that wants the
// failure as a stream element instead.

// Recover converts a pipeline panic into *err. Use as
//
//	defer ssql.Recover(&err)
//
// in the function (with a named error return) whose loop drives the
// pipeline. An error value is kept as is, so errors.As works through
// it; any other value is wrapped. With no panic in flight it is a
// no-op, and an *err already set is left alone.
func Recover(err *error) {
	r := recover()
	if r == nil {
		return
	}
	if err == nil {
		panic(r)
	}
	if *err == nil {
		*err = panicError(r)
	}
}

// Run calls fn and returns its error, or a pipeline panic as an error:
//
//	err := ssql.Run(func() error {
//	    for r := range pipeline(source) { … }
//	    return nil
//	})
func Run(fn func() error) (err error) {
	defer Recover(&err)
	return fn()
}

// panicError is the one conversion of a recovered value to an error.
func panicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("pipeline panic: %v", r)
}
