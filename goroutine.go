package ssql

import "sync"

// panicGroup runs goroutine bodies and re-raises the FIRST panic in the
// goroutine that calls Wait or rethrow — the root package's copy of
// typed's shardGroup (DFC142 step 1). The library reports a bad row by
// panicking with an error value, and the CLI, a generated program or a
// service recovers it in the goroutine that drives the pipeline. A
// helper that pulls its input in a goroutine of its own (LazyTee,
// Timeout, ToChannelWithErrors) would otherwise let that panic tear the
// process down from the helper's goroutine, bypassing every recover.
type panicGroup struct {
	wg    sync.WaitGroup
	mu    sync.Mutex
	panic any
}

// Go runs fn in a new goroutine, capturing a panic instead of crashing.
// fn's own deferred calls run before the capture, so a channel fn closes
// in a defer is closed before the panic is stored: a consumer must
// Wait (or waitQuiet then rethrow) after seeing the close, not merely
// rethrow.
func (g *panicGroup) Go(fn func()) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				g.mu.Lock()
				if g.panic == nil {
					g.panic = r
				}
				g.mu.Unlock()
			}
		}()
		fn()
	}()
}

// Wait blocks until every goroutine has finished, then re-raises the
// first captured panic (if any) in the caller's goroutine.
func (g *panicGroup) Wait() {
	g.wg.Wait()
	g.rethrow()
}

// waitQuiet blocks without re-raising; rethrow must follow in the
// goroutine that should receive the panic.
func (g *panicGroup) waitQuiet() { g.wg.Wait() }

// rethrow re-raises the captured panic, if any, in the caller.
func (g *panicGroup) rethrow() {
	g.mu.Lock()
	r := g.panic
	g.mu.Unlock()
	if r != nil {
		panic(r)
	}
}

// captured reports the captured panic as an error without re-raising
// it, for helpers that deliver failures on a channel.
func (g *panicGroup) captured() error {
	g.mu.Lock()
	r := g.panic
	g.mu.Unlock()
	if r == nil {
		return nil
	}
	return panicError(r)
}
