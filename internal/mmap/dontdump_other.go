//go:build darwin

package mmap

// darwin has no MADV_DONTDUMP; nothing to do.
func madviseDontdump([]byte) {}
