//go:build !unix

package commands

import "os"

// guardSignals: only an interrupt is portable.
var guardSignals = []os.Signal{os.Interrupt}

// dieOf: no way to die of a signal here, so the conventional status.
func dieOf(os.Signal) { os.Exit(130) }
