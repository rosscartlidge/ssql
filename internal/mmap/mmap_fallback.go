//go:build !linux && !darwin

package mmap

import "os"

// Mapped is the fallback form: Data is an ordinary heap slice from
// os.ReadFile (Windows/386/wasi and friends) — the binary works
// everywhere, just without the mmap win. Lifetime is plain GC.
type Mapped struct {
	Data []byte
}

// Map reads the whole file. Matches the unix variant's contract.
func Map(filename string) (*Mapped, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return &Mapped{Data: data}, nil
}
