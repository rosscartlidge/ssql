package ssql

import (
	"bytes"
	"fmt"
	"testing"
)

// headerlessJSONL is another tool's NDJSON export: no _schema line, every
// row the same shape.
func headerlessJSONL(n int) []byte {
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `{"id":%d,"name":"n%d","score":%d.5,"ok":true}`+"\n", i, i, i%100)
	}
	return b.Bytes()
}

// TestReadJSONLFromReaderSharesSchema: headerless same-shaped records
// share ONE Schema (the #1 performance rule) and carry no synthetic
// _line_number; a record with a different shape gets its own.
func TestReadJSONLFromReaderSharesSchema(t *testing.T) {
	data := append(headerlessJSONL(3), []byte(`{"id":9,"extra":1}`+"\n")...)
	var recs []Record
	for r := range ReadJSONLFromReader(bytes.NewReader(data)) {
		recs = append(recs, r)
	}
	if len(recs) != 4 {
		t.Fatalf("got %d records", len(recs))
	}
	if recs[0].Schema() != recs[1].Schema() || recs[1].Schema() != recs[2].Schema() {
		t.Error("same-shaped headerless records must share one Schema")
	}
	if recs[3].Schema() == recs[0].Schema() {
		t.Error("a differently-shaped record must not reuse the cached Schema")
	}
	for _, r := range recs {
		if r.Has("_line_number") {
			t.Fatal("ReadJSONLFromReader must not inject _line_number")
		}
	}
	if got := GetOr(recs[3], "extra", int64(0)); got != 1 {
		t.Errorf("extra = %d", got)
	}
}

func BenchmarkHeaderlessJSONL(b *testing.B) {
	data := headerlessJSONL(100000)
	b.Run("ReadJSONLFromReader", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			n := 0
			for range ReadJSONLFromReader(bytes.NewReader(data)) {
				n++
			}
		}
	})
	b.Run("ReadJSONFastFromReader", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			n := 0
			for range ReadJSONFastFromReader(bytes.NewReader(data)) {
				n++
			}
		}
	})
}
