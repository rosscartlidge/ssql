// 10-million-row, 3-chained-join benchmark validating the headline claim
// from doc/research/typed-code-generation.md.
//
// Run: go test -bench=Scale -benchtime=1x -run=^$ -timeout=30m ./typed/...
//
// First invocation generates ~600 MB of CSV under os.TempDir; subsequent
// runs reuse it. Skip with -short.
package typed

import (
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/rosscartlidge/ssql/v4"
)

const (
	scaleDataRows    = 10_000_000
	scaleDeptCount   = 1_000
	scaleRegionCount = 50
	scaleCityCount   = 200
)

// ---- typed schema ----

type scaleData struct {
	ID     int64
	Name   string
	DeptID string `ssql:"dept_id"`
	Age    int64
	Salary float64
}

type scaleDept struct {
	DeptID   string `ssql:"dept_id"`
	DeptName string `ssql:"dept_name"`
	RegionID string `ssql:"region_id"`
}

type scaleRegion struct {
	RegionID   string `ssql:"region_id"`
	RegionName string `ssql:"region_name"`
	CityID     string `ssql:"city_id"`
}

type scaleCity struct {
	CityID  string `ssql:"city_id"`
	City    string
	Country string
}

// Progressively-widened join result types (one per join stage).
type scaleJoin1 struct {
	ID       int64
	Name     string
	DeptID   string
	Age      int64
	Salary   float64
	DeptName string
	RegionID string
}

type scaleJoin2 struct {
	ID         int64
	Name       string
	DeptID     string
	Age        int64
	Salary     float64
	DeptName   string
	RegionID   string
	RegionName string
	CityID     string
}

type scaleJoin3 struct {
	ID         int64
	Name       string
	DeptID     string
	Age        int64
	Salary     float64
	DeptName   string
	RegionID   string
	RegionName string
	CityID     string
	City       string
	Country    string
}

// ---- setup ----

var (
	scaleSetupOnce sync.Once
	scaleDir       string
	scaleDataFile  string
	scaleDeptFile  string
	scaleRegFile   string
	scaleCityFile  string
)

func setupScaleData(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping scale bench in -short mode")
	}
	scaleSetupOnce.Do(func() {
		scaleDir = filepath.Join(os.TempDir(), "ssql-typed-scale")
		if err := os.MkdirAll(scaleDir, 0o755); err != nil {
			b.Fatal(err)
		}
		scaleDataFile = filepath.Join(scaleDir, "data.csv")
		scaleDeptFile = filepath.Join(scaleDir, "depts.csv")
		scaleRegFile = filepath.Join(scaleDir, "regions.csv")
		scaleCityFile = filepath.Join(scaleDir, "cities.csv")

		writeScaleCities(b, scaleCityFile)
		writeScaleRegions(b, scaleRegFile)
		writeScaleDepts(b, scaleDeptFile)
		writeScaleData(b, scaleDataFile)
		b.Logf("scale dataset prepared in %s (re-used on subsequent runs)", scaleDir)
	})
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeScaleCities(b *testing.B, path string) {
	if fileExists(path) {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"city_id", "city", "country"})
	countries := []string{"US", "UK", "DE", "FR", "JP", "AU", "BR", "CA", "IN", "ZA"}
	for i := 0; i < scaleCityCount; i++ {
		w.Write([]string{
			fmt.Sprintf("C%03d", i),
			fmt.Sprintf("City-%d", i),
			countries[i%len(countries)],
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		b.Fatal(err)
	}
}

func writeScaleRegions(b *testing.B, path string) {
	if fileExists(path) {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"region_id", "region_name", "city_id"})
	r := rand.New(rand.NewPCG(11, 22))
	for i := 0; i < scaleRegionCount; i++ {
		w.Write([]string{
			fmt.Sprintf("R%03d", i),
			fmt.Sprintf("Region-%d", i),
			fmt.Sprintf("C%03d", r.IntN(scaleCityCount)),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		b.Fatal(err)
	}
}

func writeScaleDepts(b *testing.B, path string) {
	if fileExists(path) {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"dept_id", "dept_name", "region_id"})
	r := rand.New(rand.NewPCG(33, 44))
	for i := 0; i < scaleDeptCount; i++ {
		w.Write([]string{
			fmt.Sprintf("D%04d", i),
			fmt.Sprintf("Dept-%d", i),
			fmt.Sprintf("R%03d", r.IntN(scaleRegionCount)),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		b.Fatal(err)
	}
}

func writeScaleData(b *testing.B, path string) {
	if fileExists(path) {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"id", "name", "dept_id", "age", "salary"})
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < scaleDataRows; i++ {
		w.Write([]string{
			strconv.Itoa(i),
			fmt.Sprintf("user-%d", i),
			fmt.Sprintf("D%04d", r.IntN(scaleDeptCount)),
			strconv.Itoa(20 + r.IntN(40)),
			strconv.FormatFloat(40000+r.Float64()*60000, 'f', 2, 64),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		b.Fatal(err)
	}
}

// ---- benchmarks ----

// BenchmarkScaleRecord3Join runs the headline workload using the current
// ssql.Record API: 10M rows -> filter -> 3 chained joins -> count.
func BenchmarkScaleRecord3Join(b *testing.B) {
	setupScaleData(b)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data, err := ssql.ReadCSV(scaleDataFile)
		if err != nil {
			b.Fatal(err)
		}
		depts, err := ssql.ReadCSV(scaleDeptFile)
		if err != nil {
			b.Fatal(err)
		}
		regs, err := ssql.ReadCSV(scaleRegFile)
		if err != nil {
			b.Fatal(err)
		}
		cities, err := ssql.ReadCSV(scaleCityFile)
		if err != nil {
			b.Fatal(err)
		}

		filtered := ssql.Where(func(r ssql.Record) bool {
			return ssql.GetOr(r, "age", int64(0)) > 30
		})(data)
		j1 := ssql.InnerJoin(depts, ssql.OnFields("dept_id"))(filtered)
		j2 := ssql.InnerJoin(regs, ssql.OnFields("region_id"))(j1)
		j3 := ssql.InnerJoin(cities, ssql.OnFields("city_id"))(j2)

		count := 0
		for range j3 {
			count++
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
		b.ReportMetric(float64(count), "rows_out")
	}
}

// BenchmarkScaleTyped3Join is the typed-struct equivalent of
// BenchmarkScaleRecord3Join. Same workload, same data files.
func BenchmarkScaleTyped3Join(b *testing.B) {
	setupScaleData(b)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data := ReadCSV[scaleData](scaleDataFile)
		depts := ReadCSV[scaleDept](scaleDeptFile)
		regs := ReadCSV[scaleRegion](scaleRegFile)
		cities := ReadCSV[scaleCity](scaleCityFile)

		filtered := Where(func(r scaleData) bool { return r.Age > 30 })(data)

		j1 := HashJoin(filtered, depts,
			func(l scaleData) string { return l.DeptID },
			func(r scaleDept) string { return r.DeptID },
			func(l scaleData, r scaleDept) scaleJoin1 {
				return scaleJoin1{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: r.DeptName, RegionID: r.RegionID,
				}
			})

		j2 := HashJoin(j1, regs,
			func(l scaleJoin1) string  { return l.RegionID },
			func(r scaleRegion) string { return r.RegionID },
			func(l scaleJoin1, r scaleRegion) scaleJoin2 {
				return scaleJoin2{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: l.DeptName, RegionID: l.RegionID,
					RegionName: r.RegionName, CityID: r.CityID,
				}
			})

		j3 := HashJoin(j2, cities,
			func(l scaleJoin2) string { return l.CityID },
			func(r scaleCity) string  { return r.CityID },
			func(l scaleJoin2, r scaleCity) scaleJoin3 {
				return scaleJoin3{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: l.DeptName, RegionID: l.RegionID,
					RegionName: l.RegionName, CityID: l.CityID,
					City: r.City, Country: r.Country,
				}
			})

		count := 0
		for range j3 {
			count++
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
		b.ReportMetric(float64(count), "rows_out")
	}
}
