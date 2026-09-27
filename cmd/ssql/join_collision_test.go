package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A non-key column present on both sides of a join is refused in EVERY
// lane, with the same message. Until v4.108 only exec refused: record
// codegen let the right side win, typed the left, and the SQL emitted
// two columns of one name (found building DFC137 §3). The merge rules
// live in one resolver (resolveJoin) that every lane calls with the
// field lists it can see: a named file's header, a <(pipeline)>'s
// advisory columns folded through its stages' schemaOps, a typed
// schema. The flags that resolve a collision (-suffix, -exclude-left,
// -exclude-right) give the same rows in exec and record codegen; typed
// and SQL refuse them loudly rather than drop them.
func TestJoinCollisionIsLoudEverywhere(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	left := filepath.Join(dir, "L.csv")
	right := filepath.Join(dir, "R.csv")
	os.WriteFile(left, []byte("id,name,city\n1,alice,Oslo\n4,dan,Oslo\n"), 0o644)
	os.WriteFile(right, []byte("order_id,cust,city\n10,1,Oslo\n11,4,Rome\n"), 0o644)

	run := func(t *testing.T, lane, stage string) (string, string, error) {
		t.Helper()
		stage = strings.ReplaceAll(strings.ReplaceAll(stage, "{R}", right), "{bin}", bin)
		pipeline := bin + " from csv " + left + " | " + bin + " " + stage + " | " + bin + " to csv"
		switch lane {
		case "record", "typed":
			pipeline = "export SSQL_MODE=" + lane + " && (" + pipeline + ") | " + bin + " generate go -run"
		case "sql":
			pipeline = "export SSQL_MODE=record && (" + pipeline + ") | " + bin + " generate sql"
		}
		cmd := exec.Command("bash", "-c", "set -o pipefail; "+pipeline)
		cmd.Env = append(os.Environ(), "SSQL_MODULE_DIR="+mustRepoRoot(t))
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		return out.String(), errb.String(), err
	}
	lanes := []string{"exec", "record", "typed", "sql"}

	// The collision itself, against a named file and a pipeline.
	for _, stage := range []string{
		"join {R} -on id cust",
		"join <({bin} from csv {R}) -on id cust",
		"join <({bin} from csv {R} | {bin} where -if order_id gt 0) -on id cust",
	} {
		for _, lane := range lanes {
			out, stderr, err := run(t, lane, stage)
			if err == nil || !strings.Contains(stderr, "join field collision: city") {
				t.Errorf("%s [%s]: want the collision refused, got err=%v\nstdout: %.200s\nstderr: %.300s", stage, lane, err, out, stderr)
			}
		}
	}
	// A pipeline that removes the colliding column is not a collision.
	for _, lane := range []string{"exec", "record", "typed", "sql"} {
		_, stderr, err := run(t, lane, "join <({bin} from csv {R} | {bin} exclude city) -on id cust")
		if err != nil {
			t.Errorf("exclude city inside the source [%s]: unexpected failure: %v\n%s", lane, err, stderr)
		}
	}

	// The resolving flags: exec and record agree byte for byte; typed and
	// SQL refuse with a reason rather than ignore them.
	resolved := map[string]string{
		"join {R} -on id cust -suffix _r":                       "id,name,city,order_id_r,city_r\n1,alice,Oslo,10,Oslo\n4,dan,Oslo,11,Rome\n",
		"join {R} -on id cust -exclude-left":                    "id,order_id,cust\n1,10,1\n4,11,4\n",
		"join <({bin} from csv {R}) -on id cust -exclude-right": "id,name,city\n1,alice,Oslo\n4,dan,Oslo\n",
		// -as makes the join a lookup: the key and the renamed fields only
		"join {R} -on id cust -as city rcity": "id,name,city,rcity\n1,alice,Oslo,Oslo\n4,dan,Oslo,Rome\n",
	}
	for stage, want := range resolved {
		for _, lane := range []string{"exec", "record"} {
			out, stderr, err := run(t, lane, stage)
			if err != nil || out != want {
				t.Errorf("%s [%s]: want\n%sgot err=%v\n%s\nstderr: %.300s", stage, lane, want, err, out, stderr)
			}
		}
		for _, lane := range []string{"typed", "sql"} {
			_, stderr, err := run(t, lane, stage)
			if err == nil || !(strings.Contains(stderr, "not supported in typed mode") || strings.Contains(stderr, "no SQL translation") || strings.Contains(stderr, "only single-clause joins without -as renames")) {
				t.Errorf("%s [%s]: want a loud refusal, got err=%v\nstderr: %.300s", stage, lane, err, stderr)
			}
		}
	}
}
