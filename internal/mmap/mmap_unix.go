//go:build linux || darwin

// Package mmap memory-maps files read-only for the parallel slurp readers
// (doc/research/mmap-readers-proposal.md). Versus os.ReadFile it avoids the
// kernel→user-space copy AND the file-sized Go heap allocation — measured
// 1.7–1.9× faster on a 1.23 GB CSV slurp.
//
// Lifetime: the mapping is released by a GC cleanup when the *Mapped
// becomes unreachable. Callers whose closures read Data MUST keep the
// *Mapped reachable for the duration (runtime.KeepAlive at the end of the
// closure) — slices into mapped memory are NOT traced by the GC and keep
// nothing alive on their own. Because release is GC-driven, unmapping may
// be delayed; that costs address space only, since clean file-backed pages
// are reclaimable by the kernel regardless.
//
// SAFETY CONTRACT for callers: data derived from Data must not outlive the
// *Mapped unless it was COPIED. In particular, zero-copy string aliasing
// (unsafe.String into the buffer — see typed.splitLineAlias) is UNSAFE
// here: the GC cannot see those references, so the mapping can be released
// while the strings live. That is why typed.ReadDelimParallel deliberately
// stays on os.ReadFile.
//
// Files modified while mapped (MAP_SHARED semantics) can deliver SIGBUS if
// truncated under us — the documented trade of every mmap reader; ssql's
// contract is that inputs are stable for the duration of a run.
package mmap

import (
	"os"
	"runtime"
	"syscall"
)

// Mapped is a read-only memory-mapped file. Data aliases kernel-managed
// memory valid while the Mapped is reachable.
type Mapped struct {
	Data []byte
}

// Map maps filename read-only. Empty files return an empty Data with no
// mapping. The mapping is released by a GC cleanup once the returned
// *Mapped is unreachable.
func Map(filename string) (*Mapped, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size == 0 {
		return &Mapped{}, nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	madviseDontdump(data)

	m := &Mapped{Data: data}
	runtime.AddCleanup(m, func(d []byte) { _ = syscall.Munmap(d) }, data)
	return m, nil
}
