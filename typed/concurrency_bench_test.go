// 10M-row, 3-chained-join benchmark using the PoC parallel runtime
// (Stream[T] + HashJoinParallel). Reuses the dataset from
// scale_bench_test.go so the four numbers (Record / typed-serial /
// typed-parallel / DuckDB) are directly comparable on the same files.
//
// Run:
//
//	go test -bench=ScaleParallel -benchtime=1x -run=^$ -timeout=30m ./typed/...
package typed

import (
	"iter"
	"runtime"
	"testing"
)

// BenchmarkScaleTypedParallel3Join is the channel-based parallel
// variant — the original Parallel(in, n) entry point that uses a
// single shared work channel. KEPT FOR REFERENCE: this design is
// actually 3x slower than serial because every row pays ~200ns of
// channel transit cost across the 4 pipeline stages.
//
// See BenchmarkScaleTypedSliceParallel3Join below for the slice-based
// variant that actually delivers a speedup.
func BenchmarkScaleTypedParallel3Join(b *testing.B) {
	setupScaleData(b)
	n := runtime.GOMAXPROCS(0)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data := ReadCSV[scaleData](scaleDataFile)
		depts := ReadCSV[scaleDept](scaleDeptFile)
		regs := ReadCSV[scaleRegion](scaleRegFile)
		cities := ReadCSV[scaleCity](scaleCityFile)

		// Convert the input to a parallel Stream as early as possible
		// so filter + all three joins run in parallel.
		stream := Parallel(data, n)

		filtered := stream.Where(func(r scaleData) bool { return r.Age > 30 })

		j1 := HashJoinParallel(filtered, depts,
			func(l scaleData) string { return l.DeptID },
			func(r scaleDept) string { return r.DeptID },
			func(l scaleData, r scaleDept) scaleJoin1 {
				return scaleJoin1{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: r.DeptName, RegionID: r.RegionID,
				}
			})

		j2 := HashJoinParallel(j1, regs,
			func(l scaleJoin1) string  { return l.RegionID },
			func(r scaleRegion) string { return r.RegionID },
			func(l scaleJoin1, r scaleRegion) scaleJoin2 {
				return scaleJoin2{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: l.DeptName, RegionID: l.RegionID,
					RegionName: r.RegionName, CityID: r.CityID,
				}
			})

		j3 := HashJoinParallel(j2, cities,
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
		for range j3.Serial() {
			count++
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
		b.ReportMetric(float64(count), "rows_out")
		b.ReportMetric(float64(n), "shards")
	}
}

// BenchmarkScaleTypedSliceParallel3Join uses ParallelFromSlice (no
// channels on the input side) and SerialCount (no channels on the
// output side). The input slice is materialized once before the
// timer starts so the bench measures pure compute parallelism.
//
// Compare this against BenchmarkScaleTypedCompute (single-threaded
// preloaded) for an apples-to-apples speedup figure.
func BenchmarkScaleTypedSliceParallel3Join(b *testing.B) {
	setupScaleData(b)

	// Materialize all four sources before the timer — same fairness
	// rule as ScaleTypedCompute.
	var data []scaleData
	for r := range ReadCSV[scaleData](scaleDataFile) {
		data = append(data, r)
	}
	var depts []scaleDept
	for r := range ReadCSV[scaleDept](scaleDeptFile) {
		depts = append(depts, r)
	}
	var regs []scaleRegion
	for r := range ReadCSV[scaleRegion](scaleRegFile) {
		regs = append(regs, r)
	}
	var cities []scaleCity
	for r := range ReadCSV[scaleCity](scaleCityFile) {
		cities = append(cities, r)
	}

	n := runtime.GOMAXPROCS(0)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		stream := ParallelFromSlice(data, n)

		filtered := stream.Where(func(r scaleData) bool { return r.Age > 30 })

		j1 := HashJoinParallel(filtered, slicesValues(depts),
			func(l scaleData) string { return l.DeptID },
			func(r scaleDept) string { return r.DeptID },
			func(l scaleData, r scaleDept) scaleJoin1 {
				return scaleJoin1{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: r.DeptName, RegionID: r.RegionID,
				}
			})

		j2 := HashJoinParallel(j1, slicesValues(regs),
			func(l scaleJoin1) string  { return l.RegionID },
			func(r scaleRegion) string { return r.RegionID },
			func(l scaleJoin1, r scaleRegion) scaleJoin2 {
				return scaleJoin2{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: l.DeptName, RegionID: l.RegionID,
					RegionName: r.RegionName, CityID: r.CityID,
				}
			})

		j3 := HashJoinParallel(j2, slicesValues(cities),
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

		count := j3.SerialCount()
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
		b.ReportMetric(float64(count), "rows_out")
		b.ReportMetric(float64(n), "shards")
	}
}

// Helper: slices.Values is in std but defining a thin wrapper saves
// importing "slices" everywhere this file already pulls in.
func slicesValues[T any](s []T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, v := range s {
			if !yield(v) {
				return
			}
		}
	}
}

// BenchmarkScaleTypedSerialCompute3Join is the single-threaded compute
// equivalent of BenchmarkScaleTypedSliceParallel3Join — same preloaded
// slices, same workload, no Stream / no goroutines. Lets us calculate
// the parallel speedup factor at compute level (excluding CSV I/O).
func BenchmarkScaleTypedSerialCompute3Join(b *testing.B) {
	setupScaleData(b)
	var data []scaleData
	for r := range ReadCSV[scaleData](scaleDataFile) {
		data = append(data, r)
	}
	var depts []scaleDept
	for r := range ReadCSV[scaleDept](scaleDeptFile) {
		depts = append(depts, r)
	}
	var regs []scaleRegion
	for r := range ReadCSV[scaleRegion](scaleRegFile) {
		regs = append(regs, r)
	}
	var cities []scaleCity
	for r := range ReadCSV[scaleCity](scaleCityFile) {
		cities = append(cities, r)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		filtered := Where(func(r scaleData) bool { return r.Age > 30 })(slicesValues(data))

		j1 := HashJoin(filtered, slicesValues(depts),
			func(l scaleData) string { return l.DeptID },
			func(r scaleDept) string { return r.DeptID },
			func(l scaleData, r scaleDept) scaleJoin1 {
				return scaleJoin1{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: r.DeptName, RegionID: r.RegionID,
				}
			})

		j2 := HashJoin(j1, slicesValues(regs),
			func(l scaleJoin1) string  { return l.RegionID },
			func(r scaleRegion) string { return r.RegionID },
			func(l scaleJoin1, r scaleRegion) scaleJoin2 {
				return scaleJoin2{
					ID: l.ID, Name: l.Name, DeptID: l.DeptID, Age: l.Age, Salary: l.Salary,
					DeptName: l.DeptName, RegionID: l.RegionID,
					RegionName: r.RegionName, CityID: r.CityID,
				}
			})

		j3 := HashJoin(j2, slicesValues(cities),
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
