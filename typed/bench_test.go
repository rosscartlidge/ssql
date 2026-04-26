// Benchmarks comparing the typed package against the Record-based ssql
// pipeline on identical workloads.
//
// Run all: go test -bench=. -run=^$ ./typed/...
// Compute-only (no CSV): go test -bench=Compute -run=^$ ./typed/...
//
// The benchmark generates ~1M-row data once on first run; the file
// lives under t.TempDir-style paths to avoid polluting the repo.
package typed

import (
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

const (
	benchDataRows   = 1_000_000
	benchLookupRows = 1_000
)

// Schemas for the typed benchmarks.
type benchData struct {
	ID     int64
	Name   string
	DeptID string `ssql:"dept_id"`
	Age    int64
	Salary float64
}

type benchLookup struct {
	DeptID   string `ssql:"dept_id"`
	DeptName string `ssql:"dept_name"`
	Location string
}

type benchJoined struct {
	ID       int64
	Name     string
	DeptID   string
	Age      int64
	Salary   float64
	DeptName string
	Location string
}

var (
	benchSetupOnce sync.Once
	benchDir       string
	benchDataFile  string
	benchLookupFile string

	preloadOnce  sync.Once
	preloadedRecordRows   []ssql.Record
	preloadedRecordLookup []ssql.Record
	preloadedTypedRows    []benchData
	preloadedTypedLookup  []benchLookup
)

func setupBenchData(b *testing.B) {
	benchSetupOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ssql-typed-bench-*")
		if err != nil {
			b.Fatal(err)
		}
		benchDir = dir
		benchDataFile = filepath.Join(dir, "data.csv")
		benchLookupFile = filepath.Join(dir, "lookup.csv")
		writeBenchLookup(b, benchLookupFile)
		writeBenchData(b, benchDataFile)
	})
}

func writeBenchLookup(b *testing.B, path string) {
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"dept_id", "dept_name", "location"})
	for i := 0; i < benchLookupRows; i++ {
		w.Write([]string{
			fmt.Sprintf("D%04d", i),
			fmt.Sprintf("Dept-%d", i),
			fmt.Sprintf("City-%d", i%50),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		b.Fatal(err)
	}
}

func writeBenchData(b *testing.B, path string) {
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"id", "name", "dept_id", "age", "salary"})
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < benchDataRows; i++ {
		w.Write([]string{
			strconv.Itoa(i),
			fmt.Sprintf("user-%d", i),
			fmt.Sprintf("D%04d", r.IntN(benchLookupRows)),
			strconv.Itoa(20 + r.IntN(40)),
			strconv.FormatFloat(40000+r.Float64()*60000, 'f', 2, 64),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		b.Fatal(err)
	}
}

func setupPreloaded(b *testing.B) {
	setupBenchData(b)
	preloadOnce.Do(func() {
		dataSeq, err := ssql.ReadCSV(benchDataFile)
		if err != nil {
			b.Fatal(err)
		}
		for r := range dataSeq {
			preloadedRecordRows = append(preloadedRecordRows, r)
		}
		lookupSeq, err := ssql.ReadCSV(benchLookupFile)
		if err != nil {
			b.Fatal(err)
		}
		for r := range lookupSeq {
			preloadedRecordLookup = append(preloadedRecordLookup, r)
		}
		for r := range ReadCSV[benchData](benchDataFile) {
			preloadedTypedRows = append(preloadedTypedRows, r)
		}
		for r := range ReadCSV[benchLookup](benchLookupFile) {
			preloadedTypedLookup = append(preloadedTypedLookup, r)
		}
	})
}

// BenchmarkRecordEnd2End is the current ssql.Record pipeline:
// CSV in -> filter -> join -> count.
func BenchmarkRecordEnd2End(b *testing.B) {
	setupBenchData(b)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data, err := ssql.ReadCSV(benchDataFile)
		if err != nil {
			b.Fatal(err)
		}
		lookup, err := ssql.ReadCSV(benchLookupFile)
		if err != nil {
			b.Fatal(err)
		}
		filtered := ssql.Where(func(r ssql.Record) bool {
			return ssql.GetOr(r, "age", int64(0)) > 30
		})(data)
		joined := ssql.InnerJoin(lookup, ssql.OnFields("dept_id"))(filtered)
		count := 0
		for range joined {
			count++
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
	}
}

// BenchmarkTypedEnd2End is the typed equivalent: CSV in -> filter -> join -> count.
func BenchmarkTypedEnd2End(b *testing.B) {
	setupBenchData(b)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data := ReadCSV[benchData](benchDataFile)
		lookup := ReadCSV[benchLookup](benchLookupFile)
		filtered := Where(func(r benchData) bool { return r.Age > 30 })(data)
		joined := HashJoin(filtered, lookup,
			func(l benchData) string   { return l.DeptID },
			func(r benchLookup) string { return r.DeptID },
			func(l benchData, r benchLookup) benchJoined {
				return benchJoined{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID,
					Age: l.Age, Salary: l.Salary,
					DeptName: r.DeptName, Location: r.Location,
				}
			})
		count := 0
		for range joined {
			count++
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
	}
}

// BenchmarkRecordCompute isolates filter+join+sink cost on Record values
// already in memory. CSV reading is excluded.
func BenchmarkRecordCompute(b *testing.B) {
	setupPreloaded(b)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data := slices.Values(preloadedRecordRows)
		lookup := slices.Values(preloadedRecordLookup)
		filtered := ssql.Where(func(r ssql.Record) bool {
			return ssql.GetOr(r, "age", int64(0)) > 30
		})(data)
		joined := ssql.InnerJoin(lookup, ssql.OnFields("dept_id"))(filtered)
		count := 0
		for range joined {
			count++
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
	}
}

// BenchmarkTypedCompute is the typed equivalent: filter+join+sink over
// preloaded struct slices.
func BenchmarkTypedCompute(b *testing.B) {
	setupPreloaded(b)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data := slices.Values(preloadedTypedRows)
		lookup := slices.Values(preloadedTypedLookup)
		filtered := Where(func(r benchData) bool { return r.Age > 30 })(data)
		joined := HashJoin(filtered, lookup,
			func(l benchData) string   { return l.DeptID },
			func(r benchLookup) string { return r.DeptID },
			func(l benchData, r benchLookup) benchJoined {
				return benchJoined{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID,
					Age: l.Age, Salary: l.Salary,
					DeptName: r.DeptName, Location: r.Location,
				}
			})
		count := 0
		for range joined {
			count++
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
	}
}
