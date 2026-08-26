package ssql

// HTTP(S) sources with Range support (DFC112). One mechanism, no
// cloud SDKs: any URL — including presigned S3/GCS/Azure URLs, where
// auth stays the cloud CLI's problem — becomes a readable source.
// Plain streaming works everywhere; random access (byte-offset
// sampling, parquet footer/column reads) works wherever the server
// honours Range, and refuses loudly where it doesn't.

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// IsHTTPURL reports whether path is an http(s) URL — the routing test
// used by `from` and its subcommands.
func IsHTTPURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

var httpSourceClient = &http.Client{Timeout: 0} // per-request bodies stream; no global deadline

// OpenHTTPStream issues a plain GET and returns the streaming body —
// the `from https://…` read path. Callers close it.
func OpenHTTPStream(url string) (io.ReadCloser, error) {
	resp, err := httpSourceClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, httpStatusError(url, resp.StatusCode)
	}
	return resp.Body, nil
}

// HTTPFile is a random-access view of an http(s) URL: every ReadAt is
// a Range request. It implements io.ReaderAt, io.ReadSeeker and
// Size() — the same surface the parquet reader and the byte-offset
// samplers need, so both work over HTTP wholesale.
type HTTPFile struct {
	url  string
	size int64
	pos  int64
}

// OpenHTTPFile probes the URL (HEAD, falling back to a 1-byte Range
// GET for HEAD-less servers) for its size and Range support. Servers
// that don't honour Range refuse loudly here — random access without
// Range would silently degrade to full downloads per read.
func OpenHTTPFile(url string) (*HTTPFile, error) {
	probe := func() (int64, bool, error) {
		resp, err := httpSourceClient.Head(url)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			return 0, false, err
		}
		defer resp.Body.Close()
		ranges := strings.Contains(strings.ToLower(resp.Header.Get("Accept-Ranges")), "bytes")
		return resp.ContentLength, ranges, nil
	}
	size, ranges, err := probe()
	if err != nil || size < 0 || !ranges {
		// HEAD unhelpful — try a real 1-byte Range GET: a 206 with
		// Content-Range proves both size and Range support.
		req, rerr := http.NewRequest("GET", url, nil)
		if rerr != nil {
			return nil, rerr
		}
		req.Header.Set("Range", "bytes=0-0")
		resp, rerr := httpSourceClient.Do(req)
		if rerr != nil {
			return nil, fmt.Errorf("probing %s: %w", url, rerr)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusPartialContent {
			if resp.StatusCode == http.StatusOK {
				return nil, fmt.Errorf("%s: server ignores Range requests — random access (parquet, -sample) needs Range; plain streaming reads still work", url)
			}
			return nil, httpStatusError(url, resp.StatusCode)
		}
		cr := resp.Header.Get("Content-Range") // "bytes 0-0/14628366"
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			fmt.Sscanf(cr[i+1:], "%d", &size)
		}
		if size <= 0 {
			return nil, fmt.Errorf("%s: cannot determine size from Content-Range %q", url, cr)
		}
	}
	return &HTTPFile{url: url, size: size}, nil
}

func (h *HTTPFile) Size() int64 { return h.size }

func (h *HTTPFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= h.size {
		return 0, io.EOF
	}
	end := off + int64(len(p)) - 1
	if end >= h.size {
		end = h.size - 1
	}
	req, err := http.NewRequest("GET", h.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))
	resp, err := httpSourceClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, httpStatusError(h.url, resp.StatusCode)
	}
	n, err := io.ReadFull(resp.Body, p[:end-off+1])
	if err == io.ErrUnexpectedEOF {
		err = nil
	}
	if err == nil && off+int64(n) >= h.size {
		err = io.EOF
	}
	if err == io.EOF && n > 0 {
		// io.ReaderAt contract: returning n == len(requested) with EOF
		// is allowed; parquet's reader tolerates it.
		return n, io.EOF
	}
	return n, err
}

// Read/Seek make HTTPFile an io.ReadSeeker (parquet.ReaderAtSeeker).
func (h *HTTPFile) Read(p []byte) (int, error) {
	n, err := h.ReadAt(p, h.pos)
	h.pos += int64(n)
	return n, err
}

func (h *HTTPFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		h.pos = offset
	case io.SeekCurrent:
		h.pos += offset
	case io.SeekEnd:
		h.pos = h.size + offset
	default:
		return 0, fmt.Errorf("invalid whence %d", whence)
	}
	if h.pos < 0 {
		return 0, fmt.Errorf("negative seek position")
	}
	return h.pos, nil
}

func httpStatusError(url string, code int) error {
	switch code {
	case http.StatusForbidden, http.StatusUnauthorized:
		return fmt.Errorf("%s: HTTP %d — if this is a presigned URL it may have expired; regenerate it (e.g. `aws s3 presign …`)", url, code)
	case http.StatusNotFound:
		return fmt.Errorf("%s: HTTP 404 — not found", url)
	default:
		return fmt.Errorf("%s: HTTP %d", url, code)
	}
}

// HTTPURLExt returns the lowercase extension of a URL's PATH,
// ignoring query and fragment — filepath.Ext on a presigned URL
// ("…/x.csv?X-Amz-Signature=…") would return ".csv?x-amz-…".
func HTTPURLExt(rawurl string) string {
	u := rawurl
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndexByte(u, '.'); i >= 0 && i > strings.LastIndexByte(u, '/') {
		return strings.ToLower(u[i:])
	}
	return ""
}

// Retry logic is deliberately absent: v1 has no retry magic — errors
// surface loudly and the user reruns. Revisit only with evidence.
