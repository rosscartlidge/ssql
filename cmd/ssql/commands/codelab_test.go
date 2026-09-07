package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codelabdata "github.com/rosscartlidge/ssql/v4/doc/codelab-data"
)

// TestCodelabWritesEmbeddedFixtures pins `ssql codelab`'s contract: every
// file the codelab runner uses is embedded byte-for-byte, an existing
// file is never overwritten silently, and -force restores the originals.
func TestCodelabWritesEmbeddedFixtures(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "play")
	written, err := codelabdata.Write(dir, false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	names := codelabdata.Names()
	if len(written) != len(names) || len(names) < 9 {
		t.Fatalf("wrote %d files, embedded %d: %v", len(written), len(names), names)
	}
	for _, name := range []string{"employees.csv", "employees.parquet", "orders.csv", "customers.csv", "sensor.csv", "signal.csv", "app.log", "README.md", "codelab-run.sh"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "..", "doc", "codelab-data", name))
		if err != nil {
			t.Fatalf("checked-in fixture %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: embedded copy differs from doc/codelab-data", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "embed.go")); err == nil {
		t.Errorf("embed.go must not ship with the data")
	}
	if st, err := os.Stat(filepath.Join(dir, "codelab-run.sh")); err != nil || st.Mode()&0o111 == 0 {
		t.Errorf("codelab-run.sh must ship executable: %v %v", st, err)
	}

	// A reader edits a fixture; a second plain run must refuse, naming it.
	if err := os.WriteFile(filepath.Join(dir, "employees.csv"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = codelabdata.Write(dir, false)
	if err == nil || !strings.Contains(err.Error(), "employees.csv") || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("second Write should refuse and name the clash: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "employees.csv")); string(got) != "edited\n" {
		t.Fatalf("refused Write still touched the file: %q", got)
	}
	if _, err := codelabdata.Write(dir, true); err != nil {
		t.Fatalf("Write -force: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "employees.csv"))
	want, _ := codelabdata.Files.ReadFile("employees.csv")
	if !bytes.Equal(got, want) {
		t.Fatalf("-force did not restore employees.csv")
	}
}
