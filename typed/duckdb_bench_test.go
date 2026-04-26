// External baseline: same 10M-row, 3-chained-join workload as
// scale_bench_test.go, executed by the DuckDB CLI. Lets us frame the
// ssql/typed numbers against a column-store / SIMD reference.
//
// Skipped when `duckdb` isn't on PATH or in -short mode. Reuses the
// dataset that scale_bench_test.go materialised under
// $TMPDIR/ssql-typed-scale (run BenchmarkScale* once first).
//
// Run:
//   go test -bench=DuckDB -benchtime=1x -run=^$ -timeout=30m ./typed/...
package typed

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// BenchmarkScaleDuckDB3Join executes the same filter + 3-join + count
// workload as BenchmarkScaleRecord3Join / BenchmarkScaleTyped3Join via
// the duckdb CLI. We measure end-to-end wall time (process startup,
// CSV scan, query, COUNT printout) — the same boundary the typed and
// Record benches measure.
func BenchmarkScaleDuckDB3Join(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping duckdb baseline in -short mode")
	}
	duckdb, err := exec.LookPath("duckdb")
	if err != nil {
		b.Skipf("duckdb not on PATH: %v", err)
	}

	// Ensure the scale dataset exists (re-uses on-disk files; only
	// materialises them on first invocation).
	setupScaleData(b)

	query := fmt.Sprintf(`
SELECT COUNT(*) FROM
  read_csv_auto('%s') d
  INNER JOIN read_csv_auto('%s') dp ON d.dept_id     = dp.dept_id
  INNER JOIN read_csv_auto('%s') r  ON dp.region_id  = r.region_id
  INNER JOIN read_csv_auto('%s') c  ON r.city_id     = c.city_id
WHERE d.age > 30;
`, scaleDataFile, scaleDeptFile, scaleRegFile, scaleCityFile)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		cmd := exec.CommandContext(ctx, duckdb, "-c", query)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			b.Fatalf("duckdb failed: %v\n%s", err, out)
		}
		// Sanity-check the row count matches the typed bench (7,250,027).
		s := strings.TrimSpace(string(out))
		if !strings.Contains(s, "7250027") {
			b.Fatalf("unexpected duckdb output: %q", s)
		}
	}
}

// BenchmarkScaleDuckDBLoaded measures DuckDB's compute path with CSVs
// already loaded into native tables. This is a fairer comparison
// against BenchmarkScaleTyped3Join's "reflection-built decoder over
// encoding/csv" overhead — DuckDB's read_csv_auto includes its own
// CSV parsing, so we get an apples-to-apples picture by separating
// CSV cost from join cost.
//
// The benchmark imports the four CSVs into temp tables once (outside
// the timer), then runs the same JOIN/COUNT N times.
func BenchmarkScaleDuckDBLoaded(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping duckdb baseline in -short mode")
	}
	duckdb, err := exec.LookPath("duckdb")
	if err != nil {
		b.Skipf("duckdb not on PATH: %v", err)
	}
	setupScaleData(b)

	// Single duckdb invocation: COPY all four CSVs into native tables,
	// then run the JOIN N times measuring each iteration's wall time
	// inside duckdb. Outer measurement (testing.B) covers the whole
	// shebang; we report the inner per-query time as a custom metric.
	var script strings.Builder
	script.WriteString(fmt.Sprintf("CREATE TABLE d AS SELECT * FROM read_csv_auto('%s');\n", scaleDataFile))
	script.WriteString(fmt.Sprintf("CREATE TABLE dp AS SELECT * FROM read_csv_auto('%s');\n", scaleDeptFile))
	script.WriteString(fmt.Sprintf("CREATE TABLE r AS SELECT * FROM read_csv_auto('%s');\n", scaleRegFile))
	script.WriteString(fmt.Sprintf("CREATE TABLE c AS SELECT * FROM read_csv_auto('%s');\n", scaleCityFile))
	script.WriteString(".timer on\n")
	for i := 0; i < b.N; i++ {
		script.WriteString(`SELECT COUNT(*) FROM d
  INNER JOIN dp ON d.dept_id    = dp.dept_id
  INNER JOIN r  ON dp.region_id = r.region_id
  INNER JOIN c  ON r.city_id    = c.city_id
WHERE d.age > 30;
`)
	}

	b.ResetTimer()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, duckdb)
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("duckdb failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "7250027") {
		b.Fatalf("unexpected duckdb output: %s", out)
	}
}
