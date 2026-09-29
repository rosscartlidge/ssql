package commands

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

// peekDelimitedHeader must find the header of a source that delivers
// more than its 1 MB peek buffer in one read (any regular file over
// 1 MB): it used to ask for Buffered()+1, overshoot the limit and give
// up, so schema mode on a big CSV listed no fields.
func TestPeekDelimitedHeaderLargeSource(t *testing.T) {
	var b strings.Builder
	b.WriteString("a,b,c\n")
	for i := 0; i < 200000; i++ {
		b.WriteString("1,2,3\n") // ~1.2 MB
	}
	data := b.String()
	for _, src := range []struct {
		name string
		r    io.Reader
	}{
		{"whole file at once", bytes.NewReader([]byte(data))},
		{"trickle", iotest(data, 7)},
	} {
		header, rest := peekDelimitedHeader(src.r, ',')
		if !slices.Equal(header, []string{"a", "b", "c"}) {
			t.Fatalf("%s: header %v", src.name, header)
		}
		all, _ := io.ReadAll(rest)
		if string(all) != data {
			t.Fatalf("%s: the stream handed on is not the whole input (%d of %d bytes)", src.name, len(all), len(data))
		}
	}
	// a header past the limit is nil, not a hang
	long := strings.Repeat("x", 2<<20) + "\nrow\n"
	if h, _ := peekDelimitedHeader(strings.NewReader(long), ','); h != nil {
		t.Fatalf("over-limit header should be nil, got %d fields", len(h))
	}
}

// iotest delivers s in chunks of n bytes, like a pipe.
func iotest(s string, n int) io.Reader {
	return &chunkReader{s: s, n: n}
}

type chunkReader struct {
	s string
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	k := c.n
	if k > len(c.s) {
		k = len(c.s)
	}
	if k > len(p) {
		k = len(p)
	}
	copy(p, c.s[:k])
	c.s = c.s[k:]
	return k, nil
}
