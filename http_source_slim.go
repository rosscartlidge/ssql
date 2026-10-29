//go:build slim

package ssql

// The slim build has no net/http (DFC151: it was a third of the browser
// wasm and nothing a browser or WASI user can reach needs it). A URL
// source refuses loudly, naming the build; IsHTTPURL (http_url.go) is
// real so the refusal is reached, not a "no such file" on the URL.

import (
	"fmt"
	"io"
)

// slimBuild lets a test in a mixed file skip what only the full build has.
const slimBuild = true

func noHTTP(url string) error {
	return fmt.Errorf("%s: URL sources are not in this build (slim: no net/http; use the full ssql)", url)
}

// OpenHTTPStream refuses: see noHTTP.
func OpenHTTPStream(url string) (io.ReadCloser, error) { return nil, noHTTP(url) }

// OpenHTTPFile refuses: see noHTTP.
func OpenHTTPFile(url string) (*HTTPFile, error) { return nil, noHTTP(url) }

// HTTPFile exists so the callers that read one compile; OpenHTTPFile
// never returns one in this build, so no method is ever reached.
type HTTPFile struct{}

func (h *HTTPFile) Requests() int64                              { return 0 }
func (h *HTTPFile) Size() int64                                  { return 0 }
func (h *HTTPFile) Prefetch(ranges []ByteRange) error            { return nil }
func (h *HTTPFile) DropPrefetched()                              {}
func (h *HTTPFile) ReadAt(p []byte, off int64) (int, error)      { return 0, io.EOF }
func (h *HTTPFile) Read(p []byte) (int, error)                   { return 0, io.EOF }
func (h *HTTPFile) Seek(offset int64, whence int) (int64, error) { return 0, nil }
func (h *HTTPFile) Close() error                                 { return nil }
