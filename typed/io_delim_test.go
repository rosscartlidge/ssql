package typed

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
)

type tsvRow struct {
	Name   string
	Age    int64
	Salary float64
	Dept   string `ssql:"dept_id"`
}

func TestReadDelimDefaultTab(t *testing.T) {
	in := "name\tage\tsalary\tdept_id\nalice\t30\t50000\tD1\nbob\t25\t40000\tD2\n"
	got := slices.Collect(ReadDelimFromReader[tsvRow](strings.NewReader(in)))
	want := []tsvRow{
		{Name: "alice", Age: 30, Salary: 50000, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Dept: "D2"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ReadDelim default tab: got %#v, want %#v", got, want)
	}
}

func TestReadDelimCustomComma(t *testing.T) {
	in := "name,age,salary,dept_id\nalice,30,50000,D1\nbob,25,40000,D2\n"
	got := slices.Collect(ReadDelimFromReader[tsvRow](strings.NewReader(in), WithDelim(',')))
	want := []tsvRow{
		{Name: "alice", Age: 30, Salary: 50000, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Dept: "D2"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ReadDelim comma: got %#v, want %#v", got, want)
	}
}

func TestReadDelimPipe(t *testing.T) {
	in := "name|age|salary|dept_id\nalice|30|50000|D1\n"
	got := slices.Collect(ReadDelimFromReader[tsvRow](strings.NewReader(in), WithDelim('|')))
	if len(got) != 1 || got[0].Name != "alice" || got[0].Dept != "D1" {
		t.Errorf("ReadDelim pipe: got %#v", got)
	}
}

func TestReadDelimEmpty(t *testing.T) {
	got := slices.Collect(ReadDelimFromReader[tsvRow](strings.NewReader("")))
	if len(got) != 0 {
		t.Errorf("ReadDelim empty: got %#v", got)
	}
}

func TestReadDelimHeaderOnly(t *testing.T) {
	got := slices.Collect(ReadDelimFromReader[tsvRow](strings.NewReader("name\tage\tsalary\tdept_id\n")))
	if len(got) != 0 {
		t.Errorf("ReadDelim header-only: got %#v", got)
	}
}

func TestReadDelimUnterminatedLastLine(t *testing.T) {
	in := "name\tage\tsalary\tdept_id\nalice\t30\t50000\tD1"
	got := slices.Collect(ReadDelimFromReader[tsvRow](strings.NewReader(in)))
	if len(got) != 1 || got[0].Name != "alice" {
		t.Errorf("ReadDelim unterminated: got %#v", got)
	}
}

func TestWriteDelimRoundTrip(t *testing.T) {
	in := []tsvRow{
		{Name: "alice", Age: 30, Salary: 50000, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Dept: "D2"},
	}
	var buf bytes.Buffer
	if err := WriteDelimToWriter(slices.Values(in), &buf); err != nil {
		t.Fatalf("WriteDelimToWriter: %v", err)
	}
	got := slices.Collect(ReadDelimFromReader[tsvRow](&buf))
	if !slices.Equal(got, in) {
		t.Errorf("WriteDelim round-trip: got %#v, want %#v", got, in)
	}
}

func TestWriteDelimCustomDelim(t *testing.T) {
	in := []tsvRow{{Name: "alice", Age: 30, Salary: 50000, Dept: "D1"}}
	var buf bytes.Buffer
	if err := WriteDelimToWriter(slices.Values(in), &buf, WithDelim('|')); err != nil {
		t.Fatalf("WriteDelimToWriter: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("alice|30")) {
		t.Errorf("WriteDelim pipe: missing 'alice|30' in %q", buf.String())
	}
	got := slices.Collect(ReadDelimFromReader[tsvRow](&buf, WithDelim('|')))
	if !slices.Equal(got, in) {
		t.Errorf("WriteDelim pipe round-trip: got %#v, want %#v", got, in)
	}
}

func TestSplitLine(t *testing.T) {
	cases := []struct {
		in    string
		delim byte
		want  []string
	}{
		{"a\tb\tc", '\t', []string{"a", "b", "c"}},
		{"a,b,c", ',', []string{"a", "b", "c"}},
		{"a\t\tb", '\t', []string{"a", "", "b"}},
		{"\ta", '\t', []string{"", "a"}},
		{"a\t", '\t', []string{"a", ""}},
		{"", '\t', []string{""}},
		{"abc", '\t', []string{"abc"}},
	}
	for _, c := range cases {
		got := splitLine([]byte(c.in), c.delim, nil)
		if !slices.Equal(got, c.want) {
			t.Errorf("splitLine(%q, %q): got %#v, want %#v", c.in, c.delim, got, c.want)
		}
	}
}

func TestReadDelimParallelMatchesSerial(t *testing.T) {
	// 1000 rows
	var b bytes.Buffer
	b.WriteString("name\tage\tsalary\tdept_id\n")
	for i := 0; i < 1000; i++ {
		b.WriteString("user")
		b.WriteByte('\t')
		b.WriteString("30")
		b.WriteByte('\t')
		b.WriteString("50000.5")
		b.WriteByte('\t')
		b.WriteString("D1")
		b.WriteByte('\n')
	}

	tmp := t.TempDir() + "/data.tsv"
	if err := writeAllFile(tmp, b.Bytes()); err != nil {
		t.Fatalf("writeAllFile: %v", err)
	}

	serial := slices.Collect(ReadDelim[tsvRow](tmp))

	for _, n := range []int{1, 2, 4, 7, 16} {
		stream := ReadDelimParallel[tsvRow](tmp, n)
		par := slices.Collect(stream.Serial())
		if len(par) != len(serial) {
			t.Errorf("ReadDelimParallel n=%d: row count %d, want %d", n, len(par), len(serial))
			continue
		}
		// Order across shards is not guaranteed but here every row is
		// identical; just check totals & first/last shard mid-row.
		for i, r := range par {
			if r != serial[0] {
				t.Errorf("ReadDelimParallel n=%d: row %d = %#v, want %#v", n, i, r, serial[0])
				break
			}
		}
	}
}

func TestStreamWriteDelimRoundTrip(t *testing.T) {
	in := []tsvRow{
		{Name: "alice", Age: 30, Salary: 50000, Dept: "D1"},
		{Name: "bob", Age: 25, Salary: 40000, Dept: "D2"},
		{Name: "carol", Age: 28, Salary: 60000, Dept: "D1"},
		{Name: "dave", Age: 33, Salary: 70000, Dept: "D3"},
	}
	stream := ParallelFromSlice(in, 3)
	var buf bytes.Buffer
	if err := stream.WriteDelimToWriter(&buf); err != nil {
		t.Fatalf("Stream.WriteDelimToWriter: %v", err)
	}
	got := slices.Collect(ReadDelimFromReader[tsvRow](&buf))
	if len(got) != len(in) {
		t.Errorf("Stream.WriteDelim round-trip: got %d rows, want %d", len(got), len(in))
	}
	// Order may differ across shards — compare as multisets via sort.
	slices.SortFunc(got, func(a, b tsvRow) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	want := slices.Clone(in)
	slices.SortFunc(want, func(a, b tsvRow) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	if !slices.Equal(got, want) {
		t.Errorf("Stream.WriteDelim multiset: got %#v, want %#v", got, want)
	}
}

func writeAllFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
