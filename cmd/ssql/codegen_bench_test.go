package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCodegenBench is a side-by-side comparison: generate the SAME
// pipeline two ways (Record vs typed), build both, run both, report.
//
// Run with:
//
//	go test ./cmd/ssql/ -run TestCodegenBench -timeout 10m -v
//
// Skipped under -short. The dataset is materialized once under
// os.TempDir/ssql-codegen-bench so re-runs are cheap.
func TestCodegenBench(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping codegen comparison under -short")
	}

	dir := filepath.Join(os.TempDir(), "ssql-codegen-bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	empCSV := filepath.Join(dir, "employees.csv")
	deptCSV := filepath.Join(dir, "departments.csv")
	const empRows = 1_000_000
	const deptRows = 1_000

	if !fileExists(empCSV) {
		t.Logf("generating %d-row employees.csv...", empRows)
		writeCodegenEmployees(t, empCSV, empRows, deptRows)
	}
	if !fileExists(deptCSV) {
		t.Logf("generating %d-row departments.csv...", deptRows)
		writeCodegenDepartments(t, deptCSV, deptRows)
	}

	// Build a fresh ssql binary into the test temp dir.
	bin := filepath.Join(t.TempDir(), "ssql")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build ssql: %v\n%s", err, out)
	}

	// The pipeline expressed once; we generate both flavors of code from
	// it. The typed program cannot use a flag-bound output filename (the
	// flagOutput indirection would require *flagOutput; for fairness we
	// drop the output file and route both to /dev/null).
	pipelineFor := func(mode string) string {
		return fmt.Sprintf(
			"export SSQLGO=%s && %s from %s | %s where -if years ge 5 | %s join %s -using dept_id | %s to csv | %s generate go",
			mode, bin, empCSV, bin, bin, deptCSV, bin, bin,
		)
	}

	// Generate, build, and time each codegen variant.
	//
	// As of v4.40 SSQLGO=typed and SSQLGO=parallel are equivalent —
	// both go through the planner, which picks Stream[T] vs
	// iter.Seq[T] per stage based on capability reach analysis. For
	// this pipeline (where + join + write CSV) the planner picks the
	// parallel form throughout. We benchmark only "1" (Record) and
	// "typed" — running "parallel" would just produce identical
	// timings, since they emit the same code.
	results := make(map[string]variant)
	for _, mode := range []string{"1", "typed"} {
		v := buildAndRun(t, mode, pipelineFor(mode))
		results[mode] = v
		t.Logf("%-13s  wall=%-10s  rss=%-12s  src=%d bytes", v.label, v.wall, v.rss, v.srcBytes)
	}

	// Also time the same pipeline as a CLI bash chain — what users
	// actually run interactively before they discover code generation.
	// Each command spawns its own process and uses ssql.Record over
	// JSONL pipes between stages. The right-side join file is wrapped
	// with <(ssql from ...) — required by ssql join to ensure the
	// schema-header is present.
	cliPipeline := fmt.Sprintf(
		"%s from %s | %s where -if years ge 5 | %s join <(%s from %s) -using dept_id | %s to csv > /dev/null",
		bin, empCSV, bin, bin, bin, deptCSV, bin,
	)
	results["cli"] = runCLIPipeline(t, cliPipeline)
	t.Logf("%-13s  wall=%-10s  rss=%-12s",
		results["cli"].label, results["cli"].wall, results["cli"].rss)

	// Print a small comparison table that the reader of the test log
	// can scan. Keep it under a reasonable width.
	cli := results["cli"]
	rec := results["1"]
	typ := results["typed"]
	cliVsTypedTime := float64(cli.wall) / float64(typ.wall)
	recVsTypedTime := float64(rec.wall) / float64(typ.wall)

	fmt.Println()
	fmt.Println("┌──────────────────────────────────────────────────────────────────────┐")
	fmt.Println("│  Codegen benchmark: same pipeline, three execution models            │")
	fmt.Println("├──────────────────┬──────────────┬──────────────┬────────────────────┤")
	fmt.Println("│  Mode            │  Wall time   │  Peak RSS    │  Source size       │")
	fmt.Println("├──────────────────┼──────────────┼──────────────┼────────────────────┤")
	fmt.Printf("│  %-16s│  %-12s│  %-12s│  %-18s│\n", cli.label,
		cli.wall.Round(time.Millisecond), formatKB(cli.rssKB), "(no source)")
	fmt.Printf("│  %-16s│  %-12s│  %-12s│  %-18s│\n", rec.label,
		rec.wall.Round(time.Millisecond), formatKB(rec.rssKB), formatBytes(rec.srcBytes))
	fmt.Printf("│  %-16s│  %-12s│  %-12s│  %-18s│\n", typ.label,
		typ.wall.Round(time.Millisecond), formatKB(typ.rssKB), formatBytes(typ.srcBytes))
	fmt.Println("├──────────────────┴──────────────┴──────────────┴────────────────────┤")
	fmt.Printf("│  typed vs CLI pipeline:        %.2fx faster                            │\n", cliVsTypedTime)
	fmt.Printf("│  typed vs Record codegen:      %.2fx faster                            │\n", recVsTypedTime)
	fmt.Println("└──────────────────────────────────────────────────────────────────────┘")
	fmt.Println()

	// Sanity assertion: the typed program shouldn't be slower than
	// either the CLI pipeline or the Record codegen.
	if recVsTypedTime < 1.0 {
		t.Errorf("typed program is slower than Record (%v vs %v) — regression?", typ.wall, rec.wall)
	}
	if cliVsTypedTime < 1.0 {
		t.Errorf("typed program is slower than CLI pipeline (%v vs %v) — regression?", typ.wall, cli.wall)
	}
}

// runCLIPipeline times the multi-process bash chain and captures the
// peak RSS reported by /usr/bin/time -v for the bash wrapper. Note
// that GNU time on Linux reports max RSS of the immediate child only
// (the bash process), not the aggregate over its forked subprocesses
// — so the RSS number understates per-stage peak. Wall time is exact.
func runCLIPipeline(t *testing.T, pipeline string) variant {
	t.Helper()
	timed := exec.Command("/usr/bin/time", "-v", "bash", "-c", pipeline)

	var stderr bytes.Buffer
	timed.Stderr = &stderr
	timed.Stdout = nil

	start := time.Now()
	if err := timed.Run(); err != nil {
		t.Fatalf("CLI pipeline run: %v\n%s", err, stderr.String())
	}
	wall := time.Since(start)

	rssKB := parseTimeRSS(stderr.String())
	return variant{
		label: "CLI pipeline",
		wall:  wall,
		rssKB: rssKB,
		rss:   formatKB(rssKB),
	}
}

type variant struct {
	label    string
	wall     time.Duration
	rssKB    int64
	rss      string
	srcBytes int
}

func buildAndRun(t *testing.T, mode, pipeline string) variant {
	t.Helper()

	label := "Record"
	if mode == "typed" || mode == "parallel" {
		// Both env values produce the same code as of v4.40 — the
		// planner picks Stream[T] vs iter.Seq[T] per stage.
		label = "typed (planner)"
	}

	// Generate the Go source.
	gen := exec.Command("bash", "-c", pipeline)
	src, err := gen.Output()
	if err != nil {
		t.Fatalf("%s codegen failed: %v", label, err)
	}

	// Write to its own module dir.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod := "module bench\n\ngo 1.24\n\nrequire github.com/rosscartlidge/ssql/v4 v4.0.0\n\nreplace github.com/rosscartlidge/ssql/v4 => " + repo + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("%s go mod tidy: %v\n%s", label, err, out)
	}
	prog := filepath.Join(dir, "prog")
	build := exec.Command("go", "build", "-ldflags", "-s -w", "-o", prog, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s go build:\n%s", label, out)
	}

	// Run via /usr/bin/time -v so we can capture peak RSS.
	timed := exec.Command("/usr/bin/time", "-v", prog)
	timed.Dir = dir
	timed.Stdout = nil // discard the (large) CSV output

	var stderr bytes.Buffer
	timed.Stderr = &stderr

	start := time.Now()
	if err := timed.Run(); err != nil {
		t.Fatalf("%s run: %v\n%s", label, err, stderr.String())
	}
	wall := time.Since(start)

	// Parse "Maximum resident set size (kbytes): N" from time -v output.
	rssKB := parseTimeRSS(stderr.String())

	return variant{
		label:    label,
		wall:     wall,
		rssKB:    rssKB,
		rss:      formatKB(rssKB),
		srcBytes: len(src),
	}
}

func parseTimeRSS(s string) int64 {
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Maximum resident set size") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				v := strings.TrimSpace(parts[len(parts)-1])
				n, err := strconv.ParseInt(v, 10, 64)
				if err == nil {
					return n
				}
			}
		}
	}
	return 0
}

func formatKB(kb int64) string {
	if kb <= 0 {
		return "?"
	}
	switch {
	case kb >= 1024*1024:
		return fmt.Sprintf("%.1f GB", float64(kb)/(1024*1024))
	case kb >= 1024:
		return fmt.Sprintf("%.1f MB", float64(kb)/1024)
	default:
		return fmt.Sprintf("%d KB", kb)
	}
}

func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeCodegenEmployees(t *testing.T, path string, rows, deptCount int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"id", "name", "dept_id", "years", "salary"})
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < rows; i++ {
		w.Write([]string{
			strconv.Itoa(i),
			fmt.Sprintf("user-%d", i),
			fmt.Sprintf("D%04d", r.IntN(deptCount)),
			strconv.Itoa(r.IntN(20)),
			strconv.FormatFloat(40000+r.Float64()*60000, 'f', 2, 64),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
}

func writeCodegenDepartments(t *testing.T, path string, deptCount int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"dept_id", "dept_name", "location"})
	for i := 0; i < deptCount; i++ {
		w.Write([]string{
			fmt.Sprintf("D%04d", i),
			fmt.Sprintf("Dept-%d", i),
			fmt.Sprintf("City-%d", i%50),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
}

func init() {
	// Ensure GOMAXPROCS is at the default; some test runners set it to 1.
	runtime.GOMAXPROCS(0)
}
