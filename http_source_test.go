//go:build !slim

package ssql

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rangeServer serves dir with Range support (http.FileServer honours
// Range natively — the same reason it's the recommended real server).
func rangeServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv
}

// noRangeServer strips Range handling: full body regardless.
func noRangeServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(r.URL.Path, "/")))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(b) // ignores Range → 200 with full body
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeHTTPFixture(t *testing.T, rows int) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("id,val\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "%d,v%d\n", i, i*3)
	}
	if err := os.WriteFile(filepath.Join(dir, "fx.csv"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHTTPFileReadAt(t *testing.T) {
	dir := writeHTTPFixture(t, 1000)
	srv := rangeServer(t, dir)
	h, err := OpenHTTPFile(srv.URL + "/fx.csv")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(dir, "fx.csv"))
	if h.Size() != st.Size() {
		t.Fatalf("size %d, want %d", h.Size(), st.Size())
	}
	buf := make([]byte, 6)
	if n, err := h.ReadAt(buf, 0); n != 6 || (err != nil) {
		t.Fatalf("ReadAt head: n=%d err=%v", n, err)
	}
	if string(buf) != "id,val" {
		t.Fatalf("head = %q", buf)
	}
	// Tail read crossing EOF.
	tail := make([]byte, 64)
	n, err := h.ReadAt(tail, h.Size()-10)
	if n != 10 {
		t.Fatalf("tail n=%d err=%v", n, err)
	}
}

func TestHTTPFileNoRangeRefusesLoudly(t *testing.T) {
	dir := writeHTTPFixture(t, 100)
	srv := noRangeServer(t, dir)
	if _, err := OpenHTTPFile(srv.URL + "/fx.csv"); err == nil ||
		!strings.Contains(err.Error(), "ignores Range") {
		t.Fatalf("want loud no-Range refusal, got %v", err)
	}
	// Streaming still works against the same server.
	body, err := OpenHTTPStream(srv.URL + "/fx.csv")
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
}

func TestHTTPStreamErrors(t *testing.T) {
	dir := writeHTTPFixture(t, 10)
	srv := rangeServer(t, dir)
	if _, err := OpenHTTPStream(srv.URL + "/nope.csv"); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Fatalf("404: %v", err)
	}
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(deny.Close)
	if _, err := OpenHTTPStream(deny.URL + "/x.csv"); err == nil ||
		!strings.Contains(err.Error(), "presigned") {
		t.Fatalf("403 should mention presigned expiry: %v", err)
	}
}

// TestSampleCSVHTTPIdentity is the oracle that matters: the same seed
// over HTTP must select the IDENTICAL rows as the local file — the
// transport must not change the selection.
func TestSampleCSVHTTPIdentity(t *testing.T) {
	dir := writeHTTPFixture(t, 5000)
	srv := rangeServer(t, dir)
	local, err := SampleCSVFile(filepath.Join(dir, "fx.csv"), 50, 42)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := SampleCSVFile(srv.URL+"/fx.csv", 50, 42)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(seq func(func(Record) bool)) []int64 {
		var out []int64
		for r := range seq {
			out = append(out, GetOr(r, "id", int64(-1)))
		}
		return out
	}
	l, r := ids(local), ids(remote)
	if len(l) != 50 || !slices.Equal(l, r) {
		t.Fatalf("transport changed selection: local %v… remote %v…", l[:5], r[:5])
	}
}

func TestCoalesceRanges(t *testing.T) {
	in := []ByteRange{{100, 50}, {0, 100}, {150, 10}, {300, 10}, {305, 20}, {1000, 900}}
	got := coalesceRanges(in, 200)
	want := []ByteRange{{0, 160}, {300, 25}, {1000, 900}}
	if !slices.Equal(got, want) {
		t.Errorf("coalesce = %v, want %v", got, want)
	}
	// The limit stops a merge; touching ranges past it stay separate.
	if got := coalesceRanges([]ByteRange{{0, 150}, {150, 100}}, 200); len(got) != 2 {
		t.Errorf("limit ignored: %v", got)
	}
}

// OpenInput, ReadInput and OpenInputAt (2026-10-16) take a file or a
// URL: the one opener every reader goes through, so what exec reads
// the generated programs read too. The equivalence cases url_* are the
// gate across the lanes; this pins the helper itself.
func TestOpenInputFileOrURL(t *testing.T) {
	dir := writeHTTPFixture(t, 50)
	srv := rangeServer(t, dir)
	local := filepath.Join(dir, "fx.csv")
	want, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{local, srv.URL + "/fx.csv"} {
		rc, err := OpenInput(name)
		if err != nil {
			t.Fatalf("OpenInput(%s): %v", name, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || string(got) != string(want) {
			t.Fatalf("OpenInput(%s): %v, %d bytes, want %d", name, err, len(got), len(want))
		}
		if got, err := ReadInput(name); err != nil || string(got) != string(want) {
			t.Fatalf("ReadInput(%s): %v, %d bytes, want %d", name, err, len(got), len(want))
		}
		ra, err := OpenInputAt(name)
		if err != nil {
			t.Fatalf("OpenInputAt(%s): %v", name, err)
		}
		head := make([]byte, 6)
		if n, err := ra.ReadAt(head, 0); n != 6 || err != nil || string(head) != "id,val" {
			t.Fatalf("OpenInputAt(%s).ReadAt: n=%d err=%v head=%q", name, n, err, head)
		}
		if err := ra.Close(); err != nil {
			t.Fatalf("OpenInputAt(%s).Close: %v", name, err)
		}
	}
	if _, err := OpenInput(filepath.Join(dir, "missing.csv")); err == nil {
		t.Fatal("OpenInput(missing file) succeeded")
	}
	if _, err := OpenInput(srv.URL + "/missing.csv"); err == nil {
		t.Fatal("OpenInput(missing URL) succeeded")
	}
}
