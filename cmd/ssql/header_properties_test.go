package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// TestHeaderPropertiesSurvive (DFC147 §5.7): the zone a source writes into
// the `_schema` header must reach the sink through every command that
// rewrites the header — the ones that derive their output schema from
// the input's (cast, fill, describe, …) once rebuilt it from a struct
// literal and dropped every stream property (SampleRate had been lost
// that way for months). One tz-stamped stream, every command, the header
// out must still say Sydney.
func TestHeaderPropertiesSurvive(t *testing.T) {
	bin := corpusBin(t)
	data := corpusData(t)
	src := bin + " from csv " + filepath.Join(data, "times.csv") + " -type ts time -tz Australia/Sydney"
	stages := []string{
		"update -set x 1",
		"update -set-expr h 'hour(ts)'",
		"where -if id gt 0",
		"sort id",
		"group-by id -count c",
		"group-by id -collect n ns",
		"include id ts",
		"exclude n",
		"limit 3",
		"offset 1",
		"distinct",
		"top 2 -field n",
		"sample 3",
		"cast -type n float",
		"cast -type ts string",
		"fill -default n 0",
		"describe",
		"resample -time ts -every 1h -value n",
		"window -sum n total",
		"extract -field ts -re '(?P<y>[0-9]{4})'",
		"update -set-expr tags '[\"a\", \"b\"]'",
		"update -set-expr tags '[\"a\", \"b\"]' | " + bin + " explode tags",
		"update -set-expr o '{\"k\": 1}' | " + bin + " flatten o",
		"unpivot -id id -value n",
		"tee /dev/null",
		"fft -field n",
	}
	for _, stage := range stages {
		stage := stage
		name := strings.Fields(stage)[0]
		if strings.Contains(stage, "|") {
			name = name + "_" + strings.Fields(stage[strings.Index(stage, "|")+1:])[1]
		}
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", src+" | "+bin+" "+stage+" | head -1")
			cmd.Env = append(os.Environ(), "TZ=UTC")
			var stderr strings.Builder
			cmd.Stderr = &stderr // notes (a sample seed, resample duplicates) must not pass for the header
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s: %v\n%s%s", stage, err, out, stderr.String())
			}
			line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
			schema, ok := lib.ParseSchemaHeaderFromBytes([]byte(line))
			if !ok {
				t.Fatalf("%s: first line is not a schema header: %s", stage, line)
			}
			if schema.TZ != "Australia/Sydney" {
				t.Errorf("%s: header lost the zone: %s", stage, line)
			}
		})
	}
}

// TestNowFollowsZone: now() reads the pipeline zone, so hour(now()) is the
// local hour under TZ and under -tz, whatever the machine's own zone.
func TestNowFollowsZone(t *testing.T) {
	bin := corpusBin(t)
	data := corpusData(t)
	for _, c := range []struct{ env, flag, zone string }{
		{"TZ=Australia/Sydney", "", "Australia/Sydney"},
		{"TZ=UTC", "-tz America/New_York", "America/New_York"},
	} {
		loc, _ := time.LoadLocation(c.zone)
		cmd := exec.Command("bash", "-c", bin+" from csv "+filepath.Join(data, "times.csv")+" "+c.flag+" | "+bin+" update -set-expr h 'hour(now())' | "+bin+" include h | "+bin+" to csv | tail -1")
		cmd.Env = append(os.Environ(), c.env)
		before := time.Now().In(loc).Hour()
		out, err := cmd.CombinedOutput()
		after := time.Now().In(loc).Hour()
		if err != nil {
			t.Fatalf("%v: %v\n%s", c, err, out)
		}
		got := strings.TrimSpace(string(out))
		if got != strconv.Itoa(before) && got != strconv.Itoa(after) {
			t.Errorf("%s %s: hour(now()) = %s, want %d (local hour in %s)", c.env, c.flag, got, before, c.zone)
		}
	}
}
