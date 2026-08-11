package mmap

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMap(t *testing.T) {
	dir := t.TempDir()

	t.Run("reads file contents", func(t *testing.T) {
		p := filepath.Join(dir, "data.bin")
		want := bytes.Repeat([]byte("hello mmap\n"), 1000)
		if err := os.WriteFile(p, want, 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := Map(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(m.Data, want) {
			t.Errorf("mapped data mismatch: %d vs %d bytes", len(m.Data), len(want))
		}
		runtime.KeepAlive(m)
	})

	t.Run("empty file maps to empty data", func(t *testing.T) {
		p := filepath.Join(dir, "empty.bin")
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := Map(p)
		if err != nil {
			t.Fatalf("empty file must not error: %v", err)
		}
		if len(m.Data) != 0 {
			t.Errorf("want empty data, got %d bytes", len(m.Data))
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		if _, err := Map(filepath.Join(dir, "nope.bin")); err == nil {
			t.Error("missing file must error")
		}
	})

	t.Run("survives GC while reachable", func(t *testing.T) {
		p := filepath.Join(dir, "gc.bin")
		want := bytes.Repeat([]byte("x"), 1<<20)
		if err := os.WriteFile(p, want, 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := Map(p)
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		runtime.GC()
		if !bytes.Equal(m.Data, want) {
			t.Error("mapping invalid after GC despite being reachable")
		}
		runtime.KeepAlive(m)
	})
}
