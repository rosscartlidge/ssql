package main

// TestStageCodeNeverExits: a generated STAGE (init or stmt fragment) must
// not exit the process. Inside a program, main() turns a panic(error)
// into "Error: …" + exit 1 through ssql.Run; inside a library function
// (generate go -package, DFC142 §5a) ssql.Safely turns it into the
// stream's last element. An os.Exit buried in a stage body bypasses both
// and kills the embedding service. Sinks (final fragments) may exit:
// they are dropped from a library function anyway. The list covers every
// stage family that emits its own error path.

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestStageCodeNeverExits(t *testing.T) {
	bin := corpusBin(t)
	data := corpusData(t)
	pipelines := []string{
		`{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} where -if-expr 'age > 30' | {{.bin}} update -set-expr tier 'age > 35 ? "senior" : "junior"' -set-expr len 'len(name)'`,
		`{{.bin}} from lines {{.data}}/app.log | {{.bin}} extract -field line -re '^(?P<ts>\S+) (?P<level>[A-Z]+) (?P<msg>.*)$'`,
		`{{.bin}} from csv {{.data}}/epochs.csv | {{.bin}} resample -time ts -every 5m -value v -fill linear`,
		`{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} cast -type age float | {{.bin}} sort -desc salary | {{.bin}} limit 3 | {{.bin}} top 2 -field salary`,
		`{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} group-by dept -count n -sum salary total -rollup`,
		`{{.bin}} from csv {{.data}}/orders.csv | {{.bin}} join {{.data}}/customers.csv -using customer_id`,
		`{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} window -partition dept -order salary -row-number rn -lag salary 1 prev`,
		`{{.bin}} from csv {{.data}}/employees.csv | {{.bin}} sample 3 -seed 1 | {{.bin}} fill -down dept | {{.bin}} distinct`,
		`{{.bin}} from csv {{.data}}/sales.csv | {{.bin}} pivot -row region -col product -val amount -func sum`,
		`{{.bin}} from csv {{.data}}/setops_left.csv | {{.bin}} except -file {{.data}}/setops_right.csv`,
	}
	for _, mode := range []string{"1", "typed"} {
		for _, p := range pipelines {
			cmdline := strings.NewReplacer("{{.bin}}", bin, "{{.data}}", data).Replace(p)
			out, err := exec.Command("bash", "-c", "export SSQLGO="+mode+" && "+cmdline).CombinedOutput()
			if err != nil {
				t.Fatalf("mode %s: %s\n%s", mode, p, out)
			}
			for _, line := range strings.Split(string(out), "\n") {
				if !strings.HasPrefix(line, "{") {
					continue
				}
				var frag struct {
					Type    string `json:"type"`
					Code    string `json:"code"`
					Command string `json:"command"`
				}
				if err := json.Unmarshal([]byte(line), &frag); err != nil {
					continue
				}
				if frag.Type == "final" {
					continue
				}
				if strings.Contains(frag.Code, "os.Exit(") {
					t.Errorf("mode %s: stage %q exits the process:\n%s", mode, frag.Command, frag.Code)
				}
			}
		}
	}
}
