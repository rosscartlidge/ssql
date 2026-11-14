package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A generated program links a heavy format only when its pipeline reads
// or writes it (DFC168): Arrow, Parquet and excelize are their own
// packages, imported by the stage that uses them. Before, every program
// linked all three (43 MB, ~1.4 s to link) because the root and typed
// packages held them. The table builds each program, runs it, and reads
// its module list (go version -m): a CSV program carries neither, an
// xlsx program excelize only, an Arrow or Parquet program Arrow only.
//
// Running each program matters too: a generated `to xlsx` named an
// undefined ctx and had never compiled until this test built one.
func TestGeneratedProgramLinksOnlyItsFormats(t *testing.T) {
	t.Parallel()
	bin := corpusBin(t)
	repo := mustRepoRoot(t)
	dir := t.TempDir()
	csv := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(csv, []byte("id,name\n1,a\n2,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Fixtures in the other formats, written by the CLI.
	for _, f := range []string{"in.parquet", "in.arrow", "in.xlsx"} {
		ext := strings.TrimPrefix(filepath.Ext(f), ".")
		sh := bin + " from " + csv + " | " + bin + " to " + ext + " " + filepath.Join(dir, f)
		if out, err := exec.Command("bash", "-c", sh).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", sh, err, out)
		}
	}
	const arrowMod, xlsxMod = "github.com/apache/arrow", "github.com/xuri/excelize"
	cases := []struct {
		name, src, sink string
		want, not       []string
	}{
		{"csv-to-csv", "from in.csv", "to csv", nil, []string{arrowMod, xlsxMod}},
		{"csv-to-xlsx", "from in.csv", "to xlsx out.xlsx", []string{xlsxMod}, []string{arrowMod}},
		{"csv-to-xlsx-sheet", "from in.csv", "to xlsx out.xlsx -sheet S", []string{xlsxMod}, []string{arrowMod}},
		{"xlsx-to-csv", "from in.xlsx", "to csv", []string{xlsxMod}, []string{arrowMod}},
		{"csv-to-arrow", "from in.csv", "to arrow out.arrow", []string{arrowMod}, []string{xlsxMod}},
		{"arrow-to-csv", "from in.arrow", "to csv", []string{arrowMod}, []string{xlsxMod}},
		{"csv-to-parquet", "from in.csv", "to parquet out.parquet", []string{arrowMod}, []string{xlsxMod}},
		{"parquet-to-csv", "from in.parquet", "to csv", []string{arrowMod}, []string{xlsxMod}},
	}
	for _, mode := range []string{"record", "typed"} {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				prog := filepath.Join(t.TempDir(), "prog")
				sh := "(export SSQL_MODE=" + mode + "; " + bin + " " + c.src + " | " + bin + " where -if id gt 0 | " + bin + " " + c.sink + ") | " + bin + " generate go -build " + prog
				cmd := exec.Command("bash", "-c", sh)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "SSQL_MODULE_DIR="+repo)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", sh, err, out)
				}
				run := exec.Command(prog)
				run.Dir = t.TempDir()
				for _, f := range []string{"in.csv", "in.parquet", "in.arrow", "in.xlsx"} {
					if err := os.Symlink(filepath.Join(dir, f), filepath.Join(run.Dir, f)); err != nil {
						t.Fatal(err)
					}
				}
				if out, err := run.CombinedOutput(); err != nil {
					t.Fatalf("running the program: %v\n%s", err, out)
				}
				mods, err := exec.Command("go", "version", "-m", prog).CombinedOutput()
				if err != nil {
					t.Fatalf("go version -m: %v\n%s", err, mods)
				}
				for _, m := range c.want {
					if !strings.Contains(string(mods), m) {
						t.Errorf("the program does not link %s", m)
					}
				}
				for _, m := range c.not {
					if strings.Contains(string(mods), m) {
						t.Errorf("the program links %s, which its pipeline does not use", m)
					}
				}
			})
		}
	}
}
