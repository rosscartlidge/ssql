package commands

import (
	"bytes"
	"slices"
	"testing"

	cf "github.com/rosscartlidge/autocli/v4"
)

func TestSchemaModeRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSchemaModeOutput(&buf, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	got := readSchemaModeInput(&buf)
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("round-trip: got %v, want %v", got, want)
	}
	// Empty / absent header → nil.
	if got := readSchemaModeInput(bytes.NewReader(nil)); got != nil {
		t.Errorf("empty input: got %v, want nil", got)
	}
}

// TestRunSchemaModeTransform drives the subprocess transform path
// in-process: feed a schema header on stdin, run the op, read the
// header back off stdout.
func TestRunSchemaModeTransform(t *testing.T) {
	cases := []struct {
		cmd  string
		args []string
		want []string
	}{
		{"rename", []string{"-as", "name", "person"}, []string{"person", "dept", "salary"}},
		{"exclude", []string{"salary"}, []string{"name", "dept"}},
		{"group-by", []string{"dept", "-count", "n"}, []string{"dept", "n"}},
		{"where", []string{"-if", "salary", "gt", "1"}, []string{"name", "dept", "salary"}}, // identity
		{"pivot", []string{"dept", "salary"}, nil},                                          // undeterminable → empty
	}
	for _, c := range cases {
		var in, out bytes.Buffer
		if err := writeSchemaModeOutput(&in, []string{"name", "dept", "salary"}); err != nil {
			t.Fatal(err)
		}
		ctx := &cf.Context{RawArgs: c.args}
		ctx.SetStdin(&in)
		ctx.SetStdout(&out)
		if err := runSchemaModeTransform(ctx, c.cmd); err != nil {
			t.Fatalf("%s: %v", c.cmd, err)
		}
		got := readSchemaModeInput(&out)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s %v: got %v, want %v", c.cmd, c.args, got, c.want)
		}
	}
}

// TestRunSchemaModeTransform_StripsCommandName confirms a leading
// command name in RawArgs (if the framework includes it) is dropped
// before the op decodes argv.
func TestRunSchemaModeTransform_StripsCommandName(t *testing.T) {
	var in, out bytes.Buffer
	_ = writeSchemaModeOutput(&in, []string{"name", "dept"})
	ctx := &cf.Context{RawArgs: []string{"rename", "-as", "name", "person"}}
	ctx.SetStdin(&in)
	ctx.SetStdout(&out)
	if err := runSchemaModeTransform(ctx, "rename"); err != nil {
		t.Fatal(err)
	}
	if got, want := readSchemaModeInput(&out), []string{"person", "dept"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
