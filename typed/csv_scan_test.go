package typed

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type scanRow struct {
	ID    int64   `ssql:"id"`
	Name  string  `ssql:"name"`
	Score float64 `ssql:"score"`
	Note  *string `ssql:"note"`
	OK    bool    `ssql:"ok"`
}

// TestReadCSVParallelMatchesSerial is the differential gate for the
// zero-copy scanner: the parallel reader (the scanner for plain lines,
// encoding/csv for quoted ones) must read a file with every CSV shape —
// quoted fields holding commas and doubled quotes, CRLF endings, blank
// lines, padded cells, empty cells in optional and required columns, a
// column the struct lacks — to exactly what the serial encoding/csv
// reader reads, in every shard count.
func TestReadCSVParallelMatchesSerial(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("id,name,score,note,extra,ok\r\n")
	for i := 0; i < 2000; i++ {
		switch i % 7 {
		case 0:
			sb.WriteString(`` + itoa(i) + `,"Smith, John",1.5,"say ""hi""",x,true` + "\r\n")
		case 1:
			sb.WriteString(itoa(i) + ",plain,2.25,,x,false\n")
		case 2:
			sb.WriteString(itoa(i) + ",  padded  ,  3  ,note,x,true\n")
		case 3:
			sb.WriteString("\n") // blank line: skipped
			sb.WriteString(itoa(i) + ",after blank,4,n,x,false\n")
		case 4:
			sb.WriteString(itoa(i) + `,"quoted plain",5,"",x,true` + "\n")
		case 5:
			sb.WriteString(itoa(i) + ",unicode é ü,6.5,ñ,x,false\n")
		default:
			sb.WriteString(itoa(i) + ",last,7,,x,true")
			if i != 1999 {
				sb.WriteString("\n")
			}
		}
	}
	path := filepath.Join(t.TempDir(), "mixed.csv")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var serial []scanRow
	ReadCSV[scanRow](path)(func(r scanRow) bool { serial = append(serial, r); return true })
	if len(serial) != 2000 {
		t.Fatalf("serial read %d rows, want 2000", len(serial))
	}
	for _, n := range []int{1, 2, 3, 7, 64} {
		var par []scanRow
		ReadCSVParallel[scanRow](path, n).Serial()(func(r scanRow) bool { par = append(par, r); return true })
		sort.Slice(par, func(a, b int) bool { return par[a].ID < par[b].ID })
		if !reflect.DeepEqual(derefNotes(par), derefNotes(serial)) {
			for i := range serial {
				if i >= len(par) || !reflect.DeepEqual(derefNotes(par[i:i+1]), derefNotes(serial[i:i+1])) {
					t.Fatalf("n=%d: row %d differs\n parallel %+v\n serial   %+v", n, i, par[i], serial[i])
				}
			}
			t.Fatalf("n=%d: %d rows, want %d", n, len(par), len(serial))
		}
	}
}

// TestReadCSVParallelStringsSurviveTheMapping: a kept string column is a
// copy, not a view of the mapping — readable after the file is gone.
func TestReadCSVParallelStringsSurviveTheMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.csv")
	if err := os.WriteFile(path, []byte("id,name,score,note,ok\n1,alpha,1,n1,true\n2,beta,2,,false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rows []scanRow
	ReadCSVParallel[scanRow](path, 2).Serial()(func(r scanRow) bool { rows = append(rows, r); return true })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].ID < rows[b].ID })
	if len(rows) != 2 || rows[0].Name != "alpha" || rows[1].Name != "beta" || rows[0].Note == nil || *rows[0].Note != "n1" {
		t.Fatalf("rows %+v", rows)
	}
}

// TestReadCSVParallelFieldCount: a row with the wrong number of fields is
// a ReadError naming the row, as encoding/csv reported it.
func TestReadCSVParallelFieldCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.csv")
	if err := os.WriteFile(path, []byte("id,name,score,note,ok\n1,a,1,,true\n2,b,2\n3,c,3,,false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	re := catchReadError(t, func() { drain(ReadCSVParallel[scanRow](path, 1)) })
	if !strings.Contains(re.Error(), "wrong number of fields") {
		t.Fatalf("error %v", re)
	}
}

// TestReadCSVParallelMalformedQuote: a bare quote in an unquoted field is
// encoding/csv's error, as before the scanner.
func TestReadCSVParallelMalformedQuote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.csv")
	if err := os.WriteFile(path, []byte("id,name,score,note,ok\n1,a,1,,true\n2,b\"c,2,,false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	re := catchReadError(t, func() { drain(ReadCSVParallel[scanRow](path, 1)) })
	if !strings.Contains(re.Error(), "bare") {
		t.Fatalf("error %v", re)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func derefNotes(rows []scanRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		n := "<nil>"
		if r.Note != nil {
			n = *r.Note
		}
		out[i] = strconv.FormatInt(r.ID, 10) + "|" + r.Name + "|" + strconv.FormatFloat(r.Score, 'g', -1, 64) + "|" + n + "|" + strconv.FormatBool(r.OK)
	}
	return out
}
