// typed_pipeline demonstrates the ssql/typed package alongside the
// equivalent ssql.Record-based pipeline.
//
// The same workload (read CSV -> filter -> hash join -> write CSV) is
// run with both APIs against the same generated data, and the timings
// are printed so you can see the difference live.
//
// Default workload: 100k rows joined against a 100-row lookup. Pass
// -rows N to scale up — at 1M rows the typed pipeline is ~5x faster
// and uses ~10x less memory than the Record pipeline.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/typed"
)

// ---- typed schema ----

type Employee struct {
	ID     int64
	Name   string
	DeptID string `ssql:"dept_id"`
	Years  int64
	Salary float64
}

type Department struct {
	DeptID   string `ssql:"dept_id"`
	DeptName string `ssql:"dept_name"`
	Location string
}

type Senior struct {
	Name     string
	Years    int64
	Salary   float64
	DeptName string `ssql:"dept_name"`
	Location string
}

func main() {
	rows := flag.Int("rows", 100_000, "number of employee rows to generate")
	flag.Parse()

	dir, err := os.MkdirTemp("", "ssql-typed-example-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	empCSV := filepath.Join(dir, "employees.csv")
	deptCSV := filepath.Join(dir, "departments.csv")

	fmt.Printf("Generating %d employees and 100 departments in %s ...\n", *rows, dir)
	generateData(empCSV, deptCSV, *rows)

	fmt.Println()
	fmt.Println("Pipeline:  read employees + departments, keep employees with")
	fmt.Println("           Years >= 5, join on dept_id, write seniors.csv")
	fmt.Println()

	// Record-based version
	recordOut := filepath.Join(dir, "seniors_record.csv")
	recordTime, recordCount := timeIt(func() int {
		return runRecord(empCSV, deptCSV, recordOut)
	})
	fmt.Printf("ssql.Record:  %s   %d output rows\n", recordTime, recordCount)

	// Typed version
	typedOut := filepath.Join(dir, "seniors_typed.csv")
	typedTime, typedCount := timeIt(func() int {
		return runTyped(empCSV, deptCSV, typedOut)
	})
	fmt.Printf("ssql/typed:   %s   %d output rows\n", typedTime, typedCount)

	if recordCount != typedCount {
		log.Fatalf("output row counts differ: record=%d typed=%d", recordCount, typedCount)
	}

	speedup := float64(recordTime) / float64(typedTime)
	fmt.Printf("\nspeedup:      %.1fx\n", speedup)
}

func runRecord(empPath, deptPath, outPath string) int {
	employees, err := ssql.ReadCSV(empPath)
	if err != nil {
		log.Fatal(err)
	}
	depts, err := ssql.ReadCSV(deptPath)
	if err != nil {
		log.Fatal(err)
	}
	seniors := ssql.Where(func(r ssql.Record) bool {
		return ssql.GetOr(r, "years", int64(0)) >= 5
	})(employees)
	joined := ssql.InnerJoin(depts, ssql.OnFields("dept_id"))(seniors)

	// Tee through a counter so we get a row count without re-reading the file.
	count := 0
	counted := func(yield func(ssql.Record) bool) {
		for r := range joined {
			count++
			if !yield(r) {
				return
			}
		}
	}
	if err := ssql.WriteCSV(counted, outPath); err != nil {
		log.Fatal(err)
	}
	return count
}

func runTyped(empPath, deptPath, outPath string) int {
	employees := typed.ReadCSV[Employee](empPath)
	depts := typed.ReadCSV[Department](deptPath)

	seniors := typed.Where(func(e Employee) bool {
		return e.Years >= 5
	})(employees)

	joined := typed.HashJoin(seniors, depts,
		func(e Employee) string { return e.DeptID },
		func(d Department) string { return d.DeptID },
		func(e Employee, d Department) Senior {
			return Senior{
				Name:     e.Name,
				Years:    e.Years,
				Salary:   e.Salary,
				DeptName: d.DeptName,
				Location: d.Location,
			}
		})

	count := 0
	counted := func(yield func(Senior) bool) {
		for s := range joined {
			count++
			if !yield(s) {
				return
			}
		}
	}
	if err := typed.WriteCSV(counted, outPath); err != nil {
		log.Fatal(err)
	}
	return count
}

func timeIt(fn func() int) (time.Duration, int) {
	start := time.Now()
	out := fn()
	return time.Since(start), out
}

func generateData(empPath, deptPath string, rows int) {
	const numDepts = 100
	r := rand.New(rand.NewPCG(1, 2))

	dept, err := os.Create(deptPath)
	if err != nil {
		log.Fatal(err)
	}
	dw := csv.NewWriter(dept)
	dw.Write([]string{"dept_id", "dept_name", "location"})
	for i := 0; i < numDepts; i++ {
		dw.Write([]string{
			fmt.Sprintf("D%03d", i),
			fmt.Sprintf("Dept-%d", i),
			fmt.Sprintf("City-%d", i%20),
		})
	}
	dw.Flush()
	dept.Close()

	emp, err := os.Create(empPath)
	if err != nil {
		log.Fatal(err)
	}
	ew := csv.NewWriter(emp)
	ew.Write([]string{"id", "name", "dept_id", "years", "salary"})
	for i := 0; i < rows; i++ {
		ew.Write([]string{
			strconv.Itoa(i),
			fmt.Sprintf("user-%d", i),
			fmt.Sprintf("D%03d", r.IntN(numDepts)),
			strconv.Itoa(r.IntN(20)),
			strconv.FormatFloat(40000+r.Float64()*60000, 'f', 2, 64),
		})
	}
	ew.Flush()
	emp.Close()
}
