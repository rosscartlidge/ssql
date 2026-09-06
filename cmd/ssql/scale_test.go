package main

// The scale gate (DFC113): budget assertions on a large cached
// fixture. Opt-in — run with:
//
//	SSQL_SCALE=1 go test ./cmd/ssql -run TestScaleBudgets -timeout=20m
//
// Rationale: correctness oracles pass while complexity/resource
// behavior is wrong — five fixture-invisible bugs in one week (the
// table in DFC113). Every case asserts a WALL-TIME CEILING with wide
// headroom (never flickers on a loaded machine) that a
// complexity-class or read-amplification regression blows through.
// No stored baselines, no run-to-run comparisons — those rot.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// scaleGenVersion keys the cached fixture directory: bump when the
// generator changes and stale fixtures regenerate.
const scaleGenVersion = 1

const scaleRows = 3_000_000 // ~120MB CSV: clears every known trap threshold

var (
	scaleDirOnce sync.Once
	scaleDirPath string
	scaleDirErr  error
)

// scaleDir returns the cached fixture dir, generating on first use:
// big.csv (3M rows), big.jsonl and big.parquet derived from it.
func scaleDir(t *testing.T) string {
	t.Helper()
	scaleDirOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), fmt.Sprintf("ssql-scale-v%d", scaleGenVersion))
		marker := filepath.Join(dir, ".complete")
		if _, err := os.Stat(marker); err == nil {
			scaleDirPath = dir
			return
		}
		os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			scaleDirErr = err
			return
		}
		f, err := os.Create(filepath.Join(dir, "big.csv"))
		if err != nil {
			scaleDirErr = err
			return
		}
		w := bytes.NewBuffer(make([]byte, 0, 1<<20))
		w.WriteString("id,dept,region,score\n")
		depts := []string{"eng", "sales", "ops", "legal", "hr", "labs"}
		for i := 0; i < scaleRows; i++ {
			fmt.Fprintf(w, "%d,%s,r%d,%d\n", i, depts[i%len(depts)], i%97, (i*7)%1000)
			if w.Len() > 1<<20 {
				f.Write(w.Bytes())
				w.Reset()
			}
		}
		f.Write(w.Bytes())
		f.Close()

		bin := corpusBin(t)
		for _, cmdline := range []string{
			bin + " from csv big.csv | " + bin + " tee big.jsonl > /dev/null",
			bin + " from csv big.csv | " + bin + " to parquet big.parquet",
		} {
			c := exec.Command("bash", "-c", cmdline)
			c.Dir = dir
			if out, err := c.CombinedOutput(); err != nil {
				scaleDirErr = fmt.Errorf("fixture derive (%s): %v\n%s", cmdline, err, out)
				return
			}
		}
		os.WriteFile(marker, []byte("ok"), 0o644)
		scaleDirPath = dir
	})
	if scaleDirErr != nil {
		t.Fatal(scaleDirErr)
	}
	return scaleDirPath
}

// budget runs cmdline in dir and fails the test if wall time exceeds
// ceiling (or the command errors).
func budget(t *testing.T, dir, cmdline string, ceiling time.Duration) time.Duration {
	t.Helper()
	c := exec.Command("bash", "-c", cmdline)
	c.Dir = dir
	t0 := time.Now()
	out, err := c.CombinedOutput()
	wall := time.Since(t0)
	if err != nil {
		t.Fatalf("command failed (%v): %s\n%s", err, cmdline, out)
	}
	if wall > ceiling {
		t.Errorf("BUDGET BLOWN: %s took %v (ceiling %v) — a complexity or read-amplification regression", cmdline, wall.Round(time.Millisecond), ceiling)
	}
	return wall
}

func TestScaleBudgets(t *testing.T) {
	if os.Getenv("SSQL_SCALE") == "" {
		t.Skip("scale gate is opt-in: set SSQL_SCALE=1 (see DFC113)")
	}
	bin := corpusBin(t)
	dir := scaleDir(t)

	t.Run("schema-mode-parquet", func(t *testing.T) {
		// Footer read; the old full-decode bug ≈ many seconds.
		budget(t, dir, "SSQL_MODE=schema "+bin+" from big.parquet", 1*time.Second)
	})
	t.Run("schema-mode-csv", func(t *testing.T) {
		budget(t, dir, "SSQL_MODE=schema "+bin+" from big.csv", 1*time.Second)
	})
	t.Run("sample-source", func(t *testing.T) {
		// Byte-offset sampling; the 4MB-per-line bug read ~4GB here.
		budget(t, dir, bin+" from csv big.csv -sample 1000 -sample-seed 7 > /dev/null", 1*time.Second)
	})
	t.Run("from-last", func(t *testing.T) {
		// Seek-based tail: O(N) lines regardless of file size. A full
		// read here would take ~3s+; the budget is the feature.
		budget(t, dir, bin+" from csv big.csv -last 10 > /dev/null", 1*time.Second)
	})
	t.Run("records-parquet", func(t *testing.T) {
		budget(t, dir, bin+" from big.parquet -records", 1*time.Second)
	})
	t.Run("records-csv", func(t *testing.T) {
		// One newline scan (~0.02s/120MB); a parse-path blows this.
		budget(t, dir, bin+" from csv big.csv -records", 3*time.Second)
	})
	t.Run("resample", func(t *testing.T) {
		// DFC121: the merge is O(n + grid); a per-grid-point rescan is
		// O(n·m) and passes every functional test. 3M rows onto a
		// coarse grid — generous absolute ceiling, no baseline.
		budget(t, dir, bin+" from csv big.csv | "+bin+" resample -time id -every 1000s -value score > /dev/null", 60*time.Second)
	})
	t.Run("exec-csv-scan", func(t *testing.T) {
		// Parse-amplification guard: the full exec scan, generous cap.
		budget(t, dir, bin+" from csv big.csv | "+bin+" count", 60*time.Second)
	})
	t.Run("generate-go-run-optimised", func(t *testing.T) {
		// `generate go -run` optimises by default: the parquet read is
		// pruned to the group-by column before the program is compiled.
		// Budget covers go build + a 3M-row pruned parallel read; an
		// unpruned read of all four columns still fits, so this pins
		// "the default path works end to end", and the unit tests pin
		// "the pruning rule applied". Compiles against THIS checkout.
		repo, _ := filepath.Abs("../..")
		budget(t, dir, "SSQL_MODULE_DIR="+repo+" "+bin+" generate go -run -mode typed -pipeline '"+
			bin+" from parquet big.parquet | "+bin+" group-by dept -count n | "+bin+" to csv' > /dev/null", 90*time.Second)
	})
	t.Run("generate-go-run-jsonl-typed", func(t *testing.T) {
		// `from jsonl` in typed mode keeps the pipeline typed (roadmap
		// item 9). Before: record-mode fallback, 14.8 s / 3.9 GB on this
		// group-by; after: ~4 s / 25 MB with the serial reflection reader.
		// Ceiling covers go build + the read; the fallback would blow it.
		repo, _ := filepath.Abs("../..")
		budget(t, dir, "SSQL_MODULE_DIR="+repo+" "+bin+" generate go -run -mode typed -pipeline '"+
			bin+" from jsonl big.jsonl | "+bin+" group-by dept -count n | "+bin+" to csv' > /dev/null", 60*time.Second)
	})
	t.Run("jsonl-scan", func(t *testing.T) {
		// The legacy per-line-Unmarshal reader class was 4× the csv
		// scan; cap at a similar generous ceiling.
		budget(t, dir, bin+" from jsonl big.jsonl | "+bin+" count", 60*time.Second)
	})

	t.Run("serve", func(t *testing.T) {
		cmd := exec.Command(bin, "serve", "-listen-http", "127.0.0.1:0", "-dir", dir)
		stderrPipe, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		addr := ""
		buf := make([]byte, 4096)
		deadline := time.Now().Add(10 * time.Second)
		var acc string
		for time.Now().Before(deadline) && addr == "" {
			n, _ := stderrPipe.Read(buf)
			acc += string(buf[:n])
			if i := strings.Index(acc, "listening on "); i >= 0 {
				rest := acc[i+len("listening on "):]
				if j := strings.IndexAny(rest, " \n"); j > 0 {
					addr = rest[:j]
				}
			}
		}
		if addr == "" {
			t.Fatal("serve did not announce address")
		}
		post := func(pipeline string, ceiling time.Duration) (map[string]any, time.Duration) {
			body, _ := json.Marshal(map[string]string{"pipeline": pipeline})
			t0 := time.Now()
			resp, err := http.Post("http://"+addr+"/api/execute?mode=buffered", "application/json", bytes.NewReader(body))
			wall := time.Since(t0)
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()
			var env map[string]any
			json.NewDecoder(resp.Body).Decode(&env)
			if wall > ceiling {
				t.Errorf("BUDGET BLOWN: %s took %v (ceiling %v)", pipeline, wall.Round(time.Millisecond), ceiling)
			}
			return env, wall
		}

		t.Run("early-exit", func(t *testing.T) {
			// The pipe-ownership deadlock hung forever here.
			post("ssql from csv big.csv | ssql limit 10", 15*time.Second)
		})
		t.Run("records-cache", func(t *testing.T) {
			// First run pays one newline count; second must hit the
			// argv-keyed cache — automates the manual 0.18s→0.03s check.
			_, w1 := post("ssql from csv big.csv | ssql limit 5", 15*time.Second)
			env, w2 := post("ssql from csv big.csv | ssql limit 5", 15*time.Second)
			if env["inputRows"] == nil {
				t.Fatal("inputRows missing from envelope")
			}
			if w2 > w1 && w2-w1 > 500*time.Millisecond {
				t.Errorf("records cache not hitting: first %v, second %v", w1, w2)
			}
			if w2 > 2*time.Second {
				t.Errorf("cached head run took %v — cache miss or scan regression", w2)
			}
		})
	})
}
