package ssql

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteBinPrologueResolves: the remote-side resolver picks the first
// installed ssql among the fixed absolute candidates — here a fake under
// $HOME/go/bin, the go-install location — without consulting PATH, and
// when none exists fails with one line naming the host, the paths tried
// and the remedies (exit 127).
func TestRemoteBinPrologueResolves(t *testing.T) {
	if _, err := os.Stat("/usr/bin/ssql"); err == nil {
		t.Skip("/usr/bin/ssql exists here — it would win the resolution")
	}
	if _, err := os.Stat("/usr/local/bin/ssql"); err == nil {
		t.Skip("/usr/local/bin/ssql exists here — it would win the resolution")
	}
	home := t.TempDir()
	fake := filepath.Join(home, "go", "bin", "ssql")
	if err := os.MkdirAll(filepath.Dir(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fake, []byte("#!/bin/bash\necho fake-ssql \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(cmd string) (string, error) {
		c := exec.Command("bash", "-c", cmd)
		c.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		out, err := c.CombinedOutput()
		return string(out), err
	}

	out, err := run(BuildRemoteCommand("ssql", "/data/x.csv", "", nil))
	if err != nil || !strings.Contains(out, "fake-ssql from /data/x.csv") {
		t.Errorf("resolver should run ~/go/bin/ssql: %v\n%s", err, out)
	}
	// With pushdown the stages are a pipe, so only the last one's stdout
	// is visible — but it, too, must be the resolved binary.
	out, err = run(BuildRemoteCommand("ssql", "/data/x.csv", "", [][]string{{"where", "-if", "a", "eq", "1"}}))
	if err != nil || !strings.Contains(out, "fake-ssql where -if a eq 1") {
		t.Errorf("resolver should run ~/go/bin/ssql for every stage: %v\n%s", err, out)
	}
	if !strings.Contains(BuildRemoteCommand("ssql", "/d", "", nil), `"/usr/bin/ssql" "/usr/local/bin/ssql" "$HOME/go/bin/ssql" "$HOME/.local/bin/ssql"`) {
		t.Errorf("candidate list changed: %s", BuildRemoteCommand("ssql", "/d", "", nil))
	}

	out, err = run(RemoteScriptCommand("ssql", "/tmp/s.ssql", "typed") + " </dev/null")
	if err != nil || !strings.Contains(out, "fake-ssql generate go -script /tmp/s.ssql -mode typed -run") {
		t.Errorf("script command should resolve the same way: %v\n%s", err, out)
	}

	// Nothing installed: loud, exit 127, remedies named.
	os.Remove(fake)
	out, err = run(BuildRemoteCommand("ssql", "/data/x.csv", "", nil))
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 127 {
		t.Fatalf("missing ssql should exit 127, got %v\n%s", err, out)
	}
	for _, want := range []string{"ssql: not found on", "tried /usr/bin/ssql", "/go/bin/ssql", "go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest", "-remote-bin"} {
		if !strings.Contains(out, want) {
			t.Errorf("error should contain %q:\n%s", want, out)
		}
	}

	// An absolute path is used as given, no prologue.
	if got := BuildRemoteCommand("/opt/ssql", "/d", "", nil); got != "/opt/ssql from /d" {
		t.Errorf("absolute remoteBin: %q", got)
	}
	if got := RemoteScriptCommand("/opt/ssql", "/tmp/s", "record"); strings.Contains(got, "SSQL=") || !strings.HasPrefix(got, "trap 'rm -f /tmp/s' EXIT; cat > /tmp/s && /opt/ssql generate go") {
		t.Errorf("absolute script command: %q", got)
	}
}

// TestReadCatalogBinColumn: an optional `bin` column names ssql per row
// (absolute only) and round-trips through WriteCatalog.
func TestReadCatalogBinColumn(t *testing.T) {
	dir := t.TempDir()
	cat := filepath.Join(dir, "c.csv")
	os.WriteFile(cat, []byte("host,path,bin,region\nnode1,/data/a.csv,/home/ops/go/bin/ssql,eu\nnode2,/data/b.csv,,us\n"), 0o644)
	entries, err := ReadCatalog(cat)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Bin != "/home/ops/go/bin/ssql" || entries[1].Bin != "" || entries[0].Metadata["region"] != "eu" || entries[0].Metadata["bin"] != "" {
		t.Errorf("bin column not parsed: %+v", entries)
	}
	if got := shardBin(entries[0], "ssql"); got != "/home/ops/go/bin/ssql" {
		t.Errorf("row bin should win: %q", got)
	}
	if got := shardBin(entries[1], "/opt/ssql"); got != "/opt/ssql" {
		t.Errorf("-remote-bin should apply to rows without bin: %q", got)
	}
	var sb strings.Builder
	if err := WriteCatalog(&sb, entries); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sb.String(), "host,path,bin,region\nnode1,/data/a.csv,/home/ops/go/bin/ssql,eu\n") {
		t.Errorf("WriteCatalog should carry bin:\n%s", sb.String())
	}

	os.WriteFile(cat, []byte("host,path,bin\nnode1,/data/a.csv,ssql\n"), 0o644)
	if _, err := ReadCatalog(cat); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("relative bin must be rejected: %v", err)
	}
}
