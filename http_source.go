//go:build !slim

package ssql

// HTTP(S) sources with Range support (DFC112). One mechanism, no
// cloud SDKs: any URL — including presigned S3/GCS/Azure URLs, where
// auth stays the cloud CLI's problem — becomes a readable source.
// Plain streaming works everywhere; random access (byte-offset
// sampling, parquet footer/column reads) works wherever the server
// honours Range, and refuses loudly where it doesn't.

import (
	"cmp"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// slimBuild lets a test in a mixed file skip what only the full build has.
const slimBuild = false

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
	reqs atomic.Int64 // Range requests issued (readahead's test oracle)

	mu         sync.RWMutex
	prefetched []prefetchedRange // see Prefetch
}

type prefetchedRange struct {
	off  int64
	data []byte
}

// httpPrefetchConcurrency caps the Range requests Prefetch keeps in
// flight: enough to hide a long round trip, few enough to be polite.
const httpPrefetchConcurrency = 16

// Requests reports how many Range requests this file has issued —
// the deterministic oracle for readahead tests (request COUNT, not
// wall time, so the gate can't flake).
func (h *HTTPFile) Requests() int64 { return h.reqs.Load() }

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

// Prefetch fetches the ranges concurrently and keeps them in memory;
// a later ReadAt that falls wholly inside one is served from there.
// The remote parquet reader prefetches the column chunks it is about
// to read: arrow reads them one ReadAt at a time, so over a long
// round trip the cost was latency × chunks (34 serial requests at
// ~350ms took 13.5s for a 1.6MB file, 2026-10-11) — the request count
// was already right (DFC112's negative result), the serialisation was
// not. A read outside every range still goes to the network, so
// correctness never depends on the ranges being exact.
func (h *HTTPFile) Prefetch(ranges []ByteRange) error {
	ranges = coalesceRanges(ranges, httpPrefetchMaxRange)
	got := make([]prefetchedRange, len(ranges))
	errs := make([]error, len(ranges))
	sem := make(chan struct{}, httpPrefetchConcurrency)
	var wg sync.WaitGroup
	for i, r := range ranges {
		if r.Off < 0 || r.Len <= 0 || r.Off >= h.size {
			continue
		}
		if r.Off+r.Len > h.size {
			r.Len = h.size - r.Off
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			buf := make([]byte, r.Len)
			n, err := h.fetch(buf, r.Off)
			if err == io.EOF && int64(n) == r.Len {
				err = nil
			}
			got[i], errs[i] = prefetchedRange{off: r.Off, data: buf[:n]}, err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	h.mu.Lock()
	for _, g := range got {
		if len(g.data) > 0 {
			h.prefetched = append(h.prefetched, g)
		}
	}
	h.mu.Unlock()
	return nil
}

// httpPrefetchMaxRange caps a coalesced range: adjacent chunks merge
// into one request up to this size, so a small file is a round trip or
// two while a large one still spreads across concurrent requests.
var httpPrefetchMaxRange int64 = 4 << 20 // a var so the concurrency gate can force several ranges

// coalesceRanges sorts the ranges and merges ones that touch or
// overlap while the merged range stays within limit (a single range
// larger than limit stays whole).
func coalesceRanges(ranges []ByteRange, limit int64) []ByteRange {
	rs := slices.Clone(ranges)
	slices.SortFunc(rs, func(a, b ByteRange) int { return cmp.Compare(a.Off, b.Off) })
	var out []ByteRange
	for _, r := range rs {
		if n := len(out); n > 0 {
			last := &out[n-1]
			end := max(last.Off+last.Len, r.Off+r.Len)
			if r.Off <= last.Off+last.Len && end-last.Off <= limit {
				last.Len = end - last.Off
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// DropPrefetched releases the memory Prefetch holds.
func (h *HTTPFile) DropPrefetched() {
	h.mu.Lock()
	h.prefetched = nil
	h.mu.Unlock()
}

func (h *HTTPFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= h.size {
		return 0, io.EOF
	}
	h.mu.RLock()
	for _, r := range h.prefetched {
		if off >= r.off && off+int64(len(p)) <= r.off+int64(len(r.data)) {
			n := copy(p, r.data[off-r.off:])
			h.mu.RUnlock()
			if off+int64(n) >= h.size {
				return n, io.EOF
			}
			return n, nil
		}
	}
	h.mu.RUnlock()
	return h.fetch(p, off)
}

// fetch reads p at off with one Range request.
func (h *HTTPFile) fetch(p []byte, off int64) (int, error) {
	end := off + int64(len(p)) - 1
	if end >= h.size {
		end = h.size - 1
	}
	req, err := http.NewRequest("GET", h.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))
	h.reqs.Add(1)
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

// Retry logic is deliberately absent: v1 has no retry magic — errors
// surface loudly and the user reruns. Revisit only with evidence.
