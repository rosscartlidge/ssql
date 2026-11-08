//go:build unix

package commands

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// guardSignals are the signals a childGuard catches: Ctrl-C, a plain kill,
// and a closed terminal.
var guardSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// dieOf ends ssql the way the signal would have, so the shell sees
// "killed by SIGINT" (status 130) and a loop around ssql stops.
func dieOf(s os.Signal) {
	sig, ok := s.(syscall.Signal)
	if !ok {
		os.Exit(1)
	}
	signal.Reset(sig)
	_ = syscall.Kill(os.Getpid(), sig)
	time.Sleep(time.Second) // the signal is delivered asynchronously
	os.Exit(128 + int(sig))
}
