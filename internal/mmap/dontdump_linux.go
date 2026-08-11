//go:build linux

package mmap

import "syscall"

// madviseDontdump excludes the mapping from core dumps — a 1.2 GB input
// file has no business tripling a crash dump. Advisory; errors ignored.
func madviseDontdump(data []byte) {
	_ = syscall.Madvise(data, 16 /* MADV_DONTDUMP */)
}
