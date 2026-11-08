package commands

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

// childGuard runs `generate go -run` and `generate rust -run`'s children
// (go build or cargo, then the program) so that nothing they made outlives
// them. ssql catches an interrupt while they run instead of dying of it,
// passes it on to the child, waits for the child to exit, removes the
// build, and only then dies of the same signal, so a shell loop around it
// still stops. Without it Ctrl-C killed ssql before its cleanup ran, and
// every interrupted run left a ~47 MB build in /tmp (nine were found,
// 2026-11-08). A SIGKILL cannot be caught; nothing can clean up after it.
type childGuard struct {
	sigs chan os.Signal
	got  os.Signal
}

// errInterrupted is what a step returns once a signal has arrived; the
// guard's finish dies of the signal before the error can be printed.
var errInterrupted = errors.New("interrupted")

func newChildGuard() *childGuard {
	g := &childGuard{sigs: make(chan os.Signal, 1)}
	signal.Notify(g.sigs, guardSignals...)
	return g
}

// interrupted reports whether a signal has arrived, including one that
// came between two steps.
func (g *childGuard) interrupted() bool {
	if g.got == nil {
		select {
		case s := <-g.sigs:
			g.got = s
		default:
		}
	}
	return g.got != nil
}

// run starts cmd and waits for it, passing on any signal ssql receives
// (a terminal's Ctrl-C reaches the child too; a kill names ssql alone).
func (g *childGuard) run(cmd *exec.Cmd) error {
	if g.interrupted() {
		return errInterrupted
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case err := <-done:
			if g.got != nil {
				return errInterrupted
			}
			return err
		case s := <-g.sigs:
			if g.got == nil {
				g.got = s
			}
			_ = cmd.Process.Signal(s)
		}
	}
}

// finish removes what the run made, stops catching signals, and, if one
// arrived, dies of it now that nothing is left behind.
func (g *childGuard) finish(cleanup func()) {
	cleanup()
	signal.Stop(g.sigs)
	if g.interrupted() {
		dieOf(g.got)
	}
}
