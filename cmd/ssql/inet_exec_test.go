package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInetExec is DFC162's inet type in the interpreted lane, through the
// binary: every door declares the column inet, and every stage after it
// keeps the type. The `_schema` header is the oracle, not the printed
// text: a value stringified somewhere prints the same address but is no
// longer an inet, and sorts as text from there on (DFC155 §7.6). The
// result values in every lane are TestPipelineEquivalence's inet_* cases.
func TestInetExec(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ips := write("ips.csv", "ip,n\n10.0.0.9,1\n9.0.0.1,2\n::1,3\n10.0.0.0/8,4\n0:0:0:0:0:0:0:1,5\n")
	tsv := write("ips.tsv", "ip\tn\n10.0.0.9\t1\n9.0.0.1\t2\n")
	jsonl := write("ips.jsonl", "{\"ip\":\"10.0.0.9\",\"n\":1}\n{\"ip\":\"9.0.0.1\",\"n\":2}\n")
	run := func(t *testing.T, pipeline string) string {
		t.Helper()
		out, err := shell(pipeline)
		if err != nil {
			t.Fatalf("%s: %v\n%s", pipeline, err, out)
		}
		return out
	}
	src := bin + " from csv -type ip inet " + ips

	t.Run("every_door_and_stage_keeps_the_type", func(t *testing.T) {
		for name, p := range map[string]string{
			"cast":            bin + " from csv " + ips + " | " + bin + " cast -type ip inet",
			"inet() retypes":  bin + " from csv " + ips + " | " + bin + " update -set-expr ip 'inet(ip)'",
			"ipnet() column":  bin + " from csv " + ips + " | " + bin + " update -set-expr ip 'ipnet(ip, 8)'",
			"csv -type":       src,
			"tsv -type":       bin + " from tsv -type ip inet " + tsv,
			"jsonl -type":     bin + " from jsonl " + jsonl + " -type ip inet",
			"header hop":      src + " | " + bin + " where -if n gt 0 | " + bin + " limit 9",
			"sort":            src + " | " + bin + " sort ip",
			"sort -spill":     src + " | " + bin + " sort -spill " + dir + " -memory 64 ip",
			"where":           src + " | " + bin + " where -if ip lt 10.0.0.1",
			"group-by key":    src + " | " + bin + " group-by ip -count c",
			"min max first":   src + " | " + bin + " group-by -min ip ip -max ip hi -first ip f",
			"distinct":        src + " | " + bin + " include ip | " + bin + " distinct",
			"join key":        src + " | " + bin + " join <(" + src + " | " + bin + " include ip) -on ip ip",
			"update copy":     src + " | " + bin + " update -set-field ip2 ip | " + bin + " include ip",
			"unpivot id":      src + " | " + bin + " unpivot -id ip",
			"top":             src + " | " + bin + " top 2 -field ip",
			"sidecar csv hop": src + " | " + bin + " to csv -sidecar " + filepath.Join(dir, "side.csv") + " && " + bin + " from csv " + filepath.Join(dir, "side.csv"),
		} {
			t.Run(name, func(t *testing.T) {
				if out := run(t, p); !strings.Contains(out, `"ip":"inet"`) {
					t.Errorf("the ip column is no longer inet:\n%s", out)
				}
			})
		}
	})

	t.Run("order_and_equality", func(t *testing.T) {
		out := run(t, src+" | "+bin+" sort ip | "+bin+" to csv")
		if want := "ip,n\n9.0.0.1,2\n10.0.0.0/8,4\n10.0.0.9,1\n::1,3\n::1,5\n"; out != want {
			t.Errorf("sort ip:\n%s\nwant\n%s", out, want)
		}
		out = run(t, src+" | "+bin+" group-by ip -count c | "+bin+" sort ip | "+bin+" to csv")
		if !strings.Contains(out, "::1,2") {
			t.Errorf("::1 and 0:0:0:0:0:0:0:1 are one key:\n%s", out)
		}
	})

	t.Run("parquet_is_text", func(t *testing.T) {
		// No columnar file keeps an IP address type exactly (DFC162 §2):
		// inet is written as its text, reads back as a string, and a cast
		// declares it again.
		pq := filepath.Join(dir, "ips.parquet")
		run(t, src+" | "+bin+" to parquet "+pq)
		out := run(t, bin+" from "+pq+" | "+bin+" limit 1")
		if !strings.Contains(out, `"ip":"string"`) {
			t.Errorf("a parquet inet column reads as text:\n%s", out)
		}
		out = run(t, bin+" from "+pq+" | "+bin+" cast -type ip inet | "+bin+" sort ip | "+bin+" to csv")
		if want := "ip,n\n9.0.0.1,2\n10.0.0.0/8,4\n10.0.0.9,1\n::1,3\n::1,5\n"; out != want {
			t.Errorf("parquet round trip:\n%s\nwant\n%s", out, want)
		}
	})

	t.Run("duckdb_inet_struct_cast", func(t *testing.T) {
		// ipint and ipkey on DuckDB read INET as its struct (ip_type,
		// address, mask), which DuckDB does not document: address is the
		// value for IPv4 and the value − 2^127 for IPv6. Pinned here so a
		// DuckDB that changes it fails this, not a user's ipkey.
		duck := duckdbBinary()
		if duck == "" {
			t.Skip("no duckdb")
		}
		out, err := shell(duck + ` -csv -noheader -c "SELECT CAST(CAST(x AS INET) AS STRUCT(ip_type UTINYINT, address HUGEINT, mask USMALLINT)) FROM (VALUES ('10.0.0.1/24'), ('::'), ('8000::')) t(x)"`)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		for _, want := range []string{"'ip_type': 1, 'address': 167772161, 'mask': 24", "'ip_type': 2, 'address': -170141183460469231731687303715884105728, 'mask': 128", "'ip_type': 2, 'address': 0, 'mask': 128"} {
			if !strings.Contains(out, want) {
				t.Errorf("DuckDB's INET struct changed: want %q in\n%s", want, out)
			}
		}
	})

	t.Run("bad_values_are_loud", func(t *testing.T) {
		bad := write("bad.csv", "ip\n10.0.0.1\n010.0.0.1\n")
		for name, c := range map[string]struct{ pipeline, want string }{
			"cast":       {bin + " from csv " + bad + " | " + bin + " cast -type ip inet", "is not an inet"},
			"csv -type":  {bin + " from csv -type ip inet " + bad, "010.0.0.1"},
			"where":      {src + " | " + bin + " where -if ip lt 10.0.0", "is an inet but"},
			"contains":   {src + " | " + bin + " where -if ip contains 10", "ipin("},
			"sum":        {src + " | " + bin + " group-by -sum ip s", "has no arithmetic"},
			"avg":        {src + " | " + bin + " group-by -avg ip s", "has no arithmetic"},
			"window sum": {src + " | " + bin + " window -sum ip s", "has no arithmetic"},
			"cast int":   {src + " | " + bin + " cast -type ip int", "ipint"},
		} {
			t.Run(name, func(t *testing.T) {
				out, err := shell(c.pipeline)
				if err == nil || !strings.Contains(out, c.want) {
					t.Errorf("want an error mentioning %q, got err=%v\n%s", c.want, err, out)
				}
			})
		}
	})
}
