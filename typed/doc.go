// Package typed is ssql's high-performance compiled-style data path.
//
// The main ssql package uses a Record type (map[string]any) for maximum
// flexibility — you can read any CSV without declaring its schema. That
// flexibility costs roughly 5–23x in CPU and 10–4000x in memory compared
// to working with concrete struct types directly.
//
// This package provides the typed alternative. You declare your schema
// as Go structs once; every per-row operation runs against direct field
// offsets with no map lookups, no type assertions, and (after escape
// analysis) often no per-row allocation at all.
//
// # When to use this package
//
//   - Use ssql.Record for prototyping, REPL-style work, or pipelines
//     where the schema is dynamic or unknown at compile time.
//   - Use this package when the schema is known and the pipeline is hot.
//
// # Example
//
//	type Employee struct {
//	    Name   string
//	    DeptID string  `ssql:"dept_id"`
//	    Years  int64
//	    Salary float64
//	}
//
//	type Department struct {
//	    DeptID   string `ssql:"dept_id"`
//	    DeptName string `ssql:"dept_name"`
//	}
//
//	type Senior struct {
//	    Name     string
//	    DeptName string
//	}
//
//	employees := typed.ReadCSV[Employee]("employees.csv")
//	depts     := typed.ReadCSV[Department]("departments.csv")
//
//	seniors := typed.Where(func(e Employee) bool {
//	    return e.Years >= 5
//	})(employees)
//
//	joined := typed.HashJoin(seniors, depts,
//	    func(e Employee) string   { return e.DeptID },
//	    func(d Department) string { return d.DeptID },
//	    func(e Employee, d Department) Senior {
//	        return Senior{Name: e.Name, DeptName: d.DeptName}
//	    })
//
//	if err := typed.WriteCSV(joined, "seniors.csv"); err != nil {
//	    log.Fatal(err)
//	}
//
// # Field-name conventions
//
// CSV column names map to struct fields case-insensitively. Override
// the column name with a struct tag — `ssql:"name"` is preferred,
// `csv:"name"` is accepted for ecosystem compatibility. A tag value of
// "-" excludes the field from CSV I/O.
//
// # Supported field types
//
// Phase 1 supports: string, bool, int, int64, float64.
// Pointer-to-T (nullable columns), int32, uint64, and time.Time are
// planned for Phase 1.5.
//
// # Design principle
//
// All reflection happens once at setup time (header parsing, encoder
// construction). The per-row data path is reflection-free — every field
// write is a precomputed closure that does an unsafe.Add to a known
// byte offset and writes a typed value directly. This is what allows
// the compiler to inline aggressively and the GC to stay quiet.
package typed
