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
const scaleGenVersion = 3

const scaleRows = 3_000_000 // ~120MB CSV: clears every known trap threshold

var (
	scaleDirOnce sync.Once
	scaleDirPath string
	scaleDirErr  error
)

// scaleDir returns the cached fixture dir, generating on first use:
// big.csv (3M rows), big.jsonl, big.json (a JSON array) and big.parquet
// derived from it.
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
			bin + " from csv big.csv | " + bin + " to json big.json",
			bin + " from csv big.csv | " + bin + " to parquet big.parquet",
			// A hive layout of the same rows (DFC149): six partition
			// files under parts/dept=X/, the key removed from the files
			// as DuckDB's PARTITION_BY writes them.
			"for d in eng sales ops legal hr labs; do mkdir -p parts/dept=$d && " + bin + " from parquet big.parquet | " + bin + " where -if dept eq $d | " + bin + " exclude dept | " + bin + " to parquet parts/dept=$d/big.parquet || exit 1; done",
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
// writeFollowGraphs writes the join -follow fixtures into dir once: a
// pseudo-random graph (1 M edges, 200 k nodes, 5 out-edges each, weights
// 1..97) and a layered DAG (13 layers of 3, complete between layers).
func writeFollowGraphs(t *testing.T, dir string) {
	t.Helper()
	graph := filepath.Join(dir, "follow_graph.csv")
	if _, err := os.Stat(graph); err == nil {
		return
	}
	var b bytes.Buffer
	b.WriteString("src,dst,w\n")
	for n := 0; n < 1_000_000; n++ {
		fmt.Fprintf(&b, "%d,%d,%d\n", n%200000, (n*7919+(n/200000)*104729+13)%200000, n%97+1)
	}
	var d bytes.Buffer
	d.WriteString("src,dst\n")
	for n := 0; n < 108; n++ {
		fmt.Fprintf(&d, "%d,%d\n", (n/9)*3+(n/3)%3, (n/9+1)*3+n%3)
	}
	if err := os.WriteFile(filepath.Join(dir, "follow_dag.csv"), d.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(graph, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

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
	t.Run("sort-spill", func(t *testing.T) {
		// DFC137 §1: the out-of-core sort on 3M rows with a 16 MB run
		// budget (dozens of runs) must be one write and one read of the
		// data beyond the sort — a re-sort per run, a run reopened per
		// pull, or an unbuffered codec passes the output oracle and blows
		// this. Byte-identical to the in-memory sort is the equivalence
		// gate's business; this is the ceiling. Generous, no baseline.
		budget(t, dir, bin+" from csv big.csv | "+bin+" sort dept -desc score -spill "+dir+" -memory 16M | "+bin+" count", 90*time.Second)
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
	t.Run("window-running-frame-exec", func(t *testing.T) {
		// A running sum and a dense rank over six partitions of 500 k rows.
		// Before 2026-10-27 the per-row frame scan was O(n²) per partition:
		// 50 k rows took 53 s and doubling quadrupled it (the 14.6 M-row
		// benchmark of DFC157 §4 was still running at a 30-minute cap).
		// One pass now; the budget covers the materialisation and sort.
		budget(t, dir, bin+" from csv big.csv | "+bin+" window -partition dept -order id -sum score run -dense-rank dr | "+bin+" count", 60*time.Second)
	})
	t.Run("follow", func(t *testing.T) {
		// join -follow (DFC161) over a 1 M-edge graph of 200 k nodes,
		// every node reachable from 0: the BFS and Dijkstra visit each
		// edge once (≈ 2 s and 3 s exec, 3 s typed with the build cached;
		// a per-round rescan of the frontier or of the edges is
		// quadratic and blows the ceiling). -all-paths over a DAG of 13
		// layers of 3 (each node to every node of the next layer) must
		// stream its (3^13 − 1)/2 routes depth first. The count is
		// asserted too: a walk that stops early is fast.
		writeFollowGraphs(t, dir)
		repo, _ := filepath.Abs("../..")
		seed := "printf 'node\\n0\\n' | " + bin + " from csv - | "
		budget(t, dir, seed+bin+" join follow_graph.csv -on node src -follow dst -depth d | "+bin+" count | grep -qx 200000", 30*time.Second)
		budget(t, dir, seed+bin+" join follow_graph.csv -on node src -follow dst -lowest-sum w t | "+bin+" count | grep -qx 200000", 30*time.Second)
		budget(t, dir, seed+bin+" join follow_dag.csv -on node src -follow dst -all-paths -depth d | "+bin+" count | grep -qx 797161", 30*time.Second)
		budget(t, dir, "printf 'node\\n0\\n' > follow_seed.csv && SSQL_MODULE_DIR="+repo+" "+bin+" generate go -run -mode typed -pipeline '"+
			bin+" from csv follow_seed.csv | "+bin+" join follow_graph.csv -on node src -follow dst -lowest-sum w t | "+bin+" count' | grep -qx 200000", 90*time.Second)
	})
	t.Run("window-running-frame-typed", func(t *testing.T) {
		// The typed window had the same scan (typed/window.go); same
		// pipeline through generate go -run, budget covers the build.
		repo, _ := filepath.Abs("../..")
		budget(t, dir, "SSQL_MODULE_DIR="+repo+" "+bin+" generate go -run -mode typed -pipeline '"+
			bin+" from csv big.csv | "+bin+" window -partition dept -order id -sum score run -dense-rank dr | "+bin+" count' > /dev/null", 90*time.Second)
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
	t.Run("generate-go-run-json-array-typed", func(t *testing.T) {
		// `from json FILE.json` (a JSON ARRAY) in typed mode reads with
		// typed.ReadJSONParallel — one scanner feeding decoding shards
		// (2026-10-09; before: record fallback for the whole program, as
		// JSONL was before roadmap item 9). Same ceiling as the JSONL twin.
		repo, _ := filepath.Abs("../..")
		budget(t, dir, "SSQL_MODULE_DIR="+repo+" "+bin+" generate go -run -mode typed -pipeline '"+
			bin+" from json big.json | "+bin+" group-by dept -count n | "+bin+" to csv' > /dev/null", 60*time.Second)
	})
	t.Run("catalog-hive-local-fast-path", func(t *testing.T) {
		// DFC149 §4.4: local Parquet shards are read in-process. Measured
		// 2026-10-14 on the six 500k-row partitions: the glob read 1.9 s,
		// the fast path 2.2 s, the subprocess-per-shard path (a child
		// ssql writing JSONL this process parses back) 8.9 s — the
		// ceiling is what catches that path coming back for local files.
		budget(t, dir, bin+" from catalog -hive 'parts/dept=*/big.parquet' | "+bin+" count", 6*time.Second)
		// Pruning opens one file: a sixth of the work.
		budget(t, dir, bin+" from catalog -hive 'parts/dept=*/big.parquet' -if dept eq hr | "+bin+" count", 2*time.Second)
		// The typed program (§4.6, 2026-10-17): the shards through the
		// typed Parquet reader with the key a string field, the same
		// ceiling as the typed Parquet cases (compile included). Before,
		// the catalog source compiled the whole program as records.
		repo, _ := filepath.Abs("../..")
		budget(t, dir, "SSQL_MODULE_DIR="+repo+" "+bin+" generate go -run -mode typed -pipeline '"+
			bin+" from catalog -hive \"parts/dept=*/big.parquet\" | "+bin+" group-by dept -count n | "+bin+" to csv' > /dev/null", 60*time.Second)
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
