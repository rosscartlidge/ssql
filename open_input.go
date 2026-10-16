package ssql

import (
	"io"
	"os"
)

// OpenInput opens a source by name. An http:// or https:// URL streams
// its body ([OpenHTTPStream]); anything else is a local file. Every
// file reader in this package and in typed opens through it, so a URL a
// pipeline reads in exec reads the same in the program `generate go`
// writes for it. (Until 2026-10-16 the readers called os.Open and `from
// URL` was an exec-only feature: the generated programs failed with "no
// such file or directory" on the URL.)
func OpenInput(name string) (io.ReadCloser, error) {
	if IsHTTPURL(name) {
		return OpenHTTPStream(name)
	}
	return os.Open(name)
}

// ReadInput is os.ReadFile for a name [OpenInput] accepts.
func ReadInput(name string) ([]byte, error) {
	if !IsHTTPURL(name) {
		return os.ReadFile(name)
	}
	rc, err := OpenHTTPStream(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// InputAt is a random-access source: what the Parquet readers need
// (ReadAt and Seek) plus Read and Close. `*os.File` and [*HTTPFile]
// (HTTP range requests) are the two forms [OpenInputAt] returns.
type InputAt interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

// OpenInputAt opens a source for random access: a URL as an [HTTPFile]
// (each ReadAt is a range request, coalesced by Prefetch), a file as
// itself.
func OpenInputAt(name string) (InputAt, error) {
	if IsHTTPURL(name) {
		return OpenHTTPFile(name)
	}
	return os.Open(name)
}
