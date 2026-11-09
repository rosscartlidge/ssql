package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestRustCargoLock: the pinned lock (rustCargoLock) fits the manifest
// every crate is built with. Its root is "ssqlgen" with the manifest's
// three dependencies, its DataFusion is rustDataFusionCrate's major, and
// a renamed root still sorts where cargo writes it, or `--locked` would
// refuse the build. SSQL_RUST_LOCK_UPDATE=1 regenerates it with cargo
// (the newest compatible versions); run the rust lane after.
func TestRustCargoLock(t *testing.T) {
	if os.Getenv("SSQL_RUST_LOCK_UPDATE") != "" {
		updateRustCargoLock(t)
	}
	names := regexp.MustCompile(`(?m)^name = "([^"]+)"$`)
	root := regexp.MustCompile(`(?m)^name = "ssqlgen"\nversion = "0.1.0"\ndependencies = \[\n "datafusion",\n "futures",\n "tokio",\n\]$`)
	if n := len(root.FindAllString(rustCargoLock, -1)); n != 1 {
		t.Fatalf("the lock's root package ssqlgen with datafusion, futures and tokio: found %d", n)
	}
	df := regexp.MustCompile(`(?m)^name = "datafusion"\nversion = "([^"]+)"$`).FindStringSubmatch(rustCargoLock)
	if df == nil || !strings.HasPrefix(df[1], rustDataFusionCrate+".") {
		t.Errorf("the lock's datafusion is %v, the manifest asks for %s", df, rustDataFusionCrate)
	}
	// Every package name compileRustSource and the lane give a crate.
	for _, pkg := range []string{"ssqlgen_1234567890", "ssqllane", "ssqloverflow"} {
		lock := RustCargoLock(pkg)
		if !strings.Contains(lock, "\nname = \""+pkg+"\"\n") {
			t.Errorf("%s: the root is not renamed", pkg)
			continue
		}
		var got []string
		for _, m := range names.FindAllStringSubmatch(lock, -1) {
			got = append(got, m[1])
		}
		if !sort.StringsAreSorted(got) {
			t.Errorf("%s: the renamed root no longer sorts where cargo writes it", pkg)
		}
	}
}

// updateRustCargoLock writes rust_cargo.lock from cargo's resolution of
// the manifest.
func updateRustCargoLock(t *testing.T) {
	cargo, err := rustCargo()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(RustCargoToml("ssqlgen", 3)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := exec.Command(cargo, "generate-lockfile")
	gen.Dir = dir
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("cargo generate-lockfile: %v\n%s", err, out)
	}
	lock, err := os.ReadFile(filepath.Join(dir, "Cargo.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("rust_cargo.lock", lock, 0o644); err != nil {
		t.Fatal(err)
	}
	rustCargoLock = string(lock)
	t.Logf("wrote rust_cargo.lock (%d bytes); run the rust lane before committing it", len(lock))
}

// TestRustCargoTomlOptLevel: the program compiles at the level asked for
// and its dependencies stay at 3, so a -run (0) and a -build (3) share
// one DataFusion build.
func TestRustCargoTomlOptLevel(t *testing.T) {
	run, build := RustCargoToml("ssqlgen_1", 0), RustCargoToml("ssqlgen_2", 3)
	if !strings.Contains(run, "[profile.release]\nopt-level = 3\n") || !strings.Contains(run, "\n[profile.release.package.ssqlgen_1]\nopt-level = 0\n") {
		t.Errorf("-run: the program at 0, the dependencies at 3:\n%s", run)
	}
	if strings.Contains(build, "profile.release.package") || !strings.Contains(build, "[profile.release]\nopt-level = 3\n") {
		t.Errorf("-build: everything at 3:\n%s", build)
	}
}
