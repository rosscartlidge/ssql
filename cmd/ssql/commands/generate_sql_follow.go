package commands

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// translateTraverse renders `traverse` (DFC161) as a recursive CTE in
// a derived table. The upstream is the edges (with their reverses under
// -both, deduplicated by UNION); __seeds is the starts — the -start
// values and the -start-file rows, UNIONed, so each distinct row once
// (§11) — numbered (__sid keeps several starts apart); __walk's anchor
// is every start at depth 0, and each step joins the routes the last
// step found to the edges again, never to a node already on the path.
// That is -all-paths, in every dialect. The keyed modes keep one route
// per (start, node): DuckDB's USING KEY with the recurring table, and an
// in-round QUALIFY that carries the whole tie key — without it two
// candidates for one node in one round leave an arbitrary one (the
// 565-minute Darwin of DFC161 §4). Postgres and DataFusion have no keyed
// recursion and refuse them, by name.
func translateTraverse(q *sqlQuery, args []string, funcFrags []*lib.CodeFragment) error {
	var f followSpec
	lowest := false
	for i := 0; i < len(args); i++ {
		need := func(n int) error {
			if i+n >= len(args) {
				return fmt.Errorf("traverse %s needs %d argument(s)", args[i], n)
			}
			return nil
		}
		switch args[i] {
		case "-from", "-to", "-start", "-start-file", "-depth", "-path", "-max-depth":
			if err := need(1); err != nil {
				return err
			}
			switch v := args[i+1]; args[i] {
			case "-from":
				f.src = v
			case "-to":
				f.dst = v
			case "-start":
				f.starts = append(f.starts, v)
			case "-start-file":
				f.startFile = v
			case "-depth":
				f.depth = v
			case "-path":
				f.path = v
			case "-max-depth":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return fmt.Errorf("traverse -max-depth %s: not a positive number", v)
				}
				f.maxDepth = n
			}
			i++
		case "-sum", "-lowest-sum":
			if err := need(2); err != nil {
				return err
			}
			s := ssql.FollowSum{Field: args[i+1], Name: args[i+2]}
			if args[i] == "-sum" {
				f.sums = append(f.sums, s)
			} else {
				f.lowest, lowest = s, true
			}
			i += 2
		case "-all-paths":
			f.mode = ssql.FollowAllPaths
		case "-fewest-hops":
		case "-both":
			f.both = true
		default:
			return fmt.Errorf("traverse: %s has no SQL translation", args[i])
		}
	}
	if lowest {
		f.mode = ssql.FollowLowestSum
	}
	if f.src == "" || f.dst == "" {
		return fmt.Errorf("traverse needs -from SRC and -to DST")
	}
	if len(f.starts) == 0 && f.startFile == "" {
		return fmt.Errorf("traverse needs a start: -start NODE or -start-file FILE")
	}

	// What the dialect cannot do, refused by name.
	if sqlDialectCur != dialectDuckDB {
		switch f.mode {
		case ssql.FollowFewestHops:
			return dialectRefuse("traverse -fewest-hops (the default mode)", "no keyed recursion (USING KEY) to visit each node once; use -all-paths and pick with window -partition NODE -order … -row-number rn | where -if rn eq 1")
		case ssql.FollowLowestSum:
			return dialectRefuse("traverse -lowest-sum", "no keyed recursion (USING KEY) to keep each node's cheapest route; use -all-paths -sum and pick with window -partition NODE -order TOTAL -row-number rn | where -if rn eq 1")
		}
		if f.path != "" {
			return dialectRefuse("traverse -path", "the path is a list, a nested value this dialect does not output")
		}
	}

	// The edges are the upstream: its columns and their kinds, taken
	// before the start file's translation records its own.
	edgeFields := q.columns
	edgeKinds := maps.Clone(sqlColumnKinds)
	edgeType := func(fl string) string { return edgeKinds[fl] }

	// The start file: its source text and columns.
	var startSrc string
	var startFields []string
	if f.startFile != "" {
		if strings.HasPrefix(f.startFile, "/dev/fd/") && len(funcFrags) > 0 {
			side := buildJoinQuery(funcFrags[len(funcFrags)-1])
			if side == nil {
				return fmt.Errorf("traverse: the -start-file pipeline has no SQL translation")
			}
			startSrc = sqlSubquery("(" + renderSelect(side) + ")")
			// The side query's own columns keep their order; the
			// fragments' advisory types are a sorted fallback.
			startFields = side.columns
			if startFields == nil {
				startFields = fragmentFields(funcFrags[len(funcFrags)-1].FuncBody)
			}
			clear(sqlColumnKinds)
			maps.Copy(sqlColumnKinds, edgeKinds)
		} else {
			src, _, err := sqlSideFileSource(f.startFile)
			if err != nil {
				return err
			}
			startSrc = src
			if _, schema, err := readAuxInput(f.startFile); err == nil && schema != nil {
				startFields = schema.Fields
			}
		}
	}
	if _, err := followCheck(edgeFields, edgeType, startFields, f); err != nil {
		return err
	}
	values, err := traverseStartValues(f.starts, f.src, edgeType(f.src))
	if err != nil {
		return err
	}
	totals := f.weights()
	totalTypes := make([]string, len(totals))
	for i, w := range totals {
		// ssql:domain numeric
		switch base, scale := lib.ParseWireType(edgeType(w.Field)); base {
		case lib.TypeInt, lib.TypeInt128:
			totalTypes[i] = sqlInt128Type()
		case lib.TypeFloat:
			totalTypes[i] = sqlFloatType()
		case lib.TypeDecimal:
			totalTypes[i] = sqlDecimalType(scale)
		default:
			return fmt.Errorf("traverse: the type of edge field %q is unknown, so its total has no SQL type — read the edges from a CSV file, or use generate go", w.Field)
		}
	}
	if f.both && edgeFields == nil {
		return fmt.Errorf("traverse -both: the edges' columns are unknown, so their reverses cannot be written — read the edges from a CSV file, or use generate go")
	}

	// The starts: each -start value a row of SRC (the start file's other
	// columns NULL), then the file's rows; UNION keeps each distinct row
	// once, as FollowStarts does.
	var parts []string
	for _, v := range values {
		cols := []string{sqlTypedLiteral(v) + " AS " + quoteIdent(f.src)}
		for _, c := range startFields {
			if c != f.src {
				cols = append(cols, "NULL AS "+quoteIdent(c))
			}
		}
		parts = append(parts, "SELECT "+strings.Join(cols, ", "))
	}
	union := "\n  UNION\n  "
	if startSrc != "" {
		sel := "*"
		if startFields != nil {
			qs := make([]string, len(startFields))
			for i, c := range startFields {
				qs[i] = quoteIdent(c)
			}
			sel = strings.Join(qs, ", ")
		} else if len(values) > 0 {
			if sqlDialectCur != dialectDuckDB {
				return dialectRefuse("traverse -start with a -start-file", "the start file's columns are unknown, so the -start rows cannot be padded to them — read the starts from a CSV file")
			}
			union = "\n  UNION BY NAME\n  "
		}
		parts = append(parts, "SELECT DISTINCT "+sel+" FROM "+startSrc)
	}
	seeds := strings.Join(parts, union)
	seedCols := startFields
	if f.startFile == "" {
		seedCols = []string{f.src}
	}
	if seedCols == nil && sqlDialectCur != dialectDuckDB {
		return dialectRefuse("traverse", "the start file's columns are unknown — name them with `include`, or use a CSV/TSV list")
	}

	if q.hasClauses() {
		wrapAsSubquery(q)
	}
	if q.fromClause == "" {
		return fmt.Errorf("traverse: no edge source to translate")
	}
	var outCols []string
	if seedCols != nil {
		outCols = append(append(append(outCols, seedCols...), f.dst), f.accNames()...)
	}
	wrapAsSQLOnly(q, "traverse", func(edges string) string {
		return sqlSubquery(followCTE(seeds, edges, edgeFields, f, totals, totalTypes, seedCols))
	})
	sqlOnlyReset(q, outCols)
	// The output's kinds: SRC and DST keep the edges'; the edges' other
	// columns are gone and a start file's are unknown.
	for _, c := range edgeFields {
		if c != f.src && c != f.dst {
			delete(sqlColumnKinds, c)
		}
	}
	for _, c := range startFields {
		if c != f.src {
			delete(sqlColumnKinds, c)
		}
	}
	if k := edgeKinds[f.src]; k != "" {
		sqlColumnKinds[f.dst] = k
	}
	if f.depth != "" {
		sqlColumnKinds[f.depth] = "int"
	}
	for _, w := range totals {
		delete(sqlColumnKinds, w.Name) // an int128 total: not an int64 for the checked arithmetic
	}
	return nil
}

// followCTE is the recursive CTE text (translateTraverse): seeds is the
// starts' SELECT, edgeSrc the upstream's FROM text.
func followCTE(seeds, edgeSrc string, edgeFields []string, f followSpec, totals []ssql.FollowSum, totalTypes []string, seedCols []string) string {
	qi := quoteIdent
	src, dst := f.src, f.dst
	edges := "SELECT * FROM " + edgeSrc
	if f.both {
		swapped := make([]string, len(edgeFields))
		cols := make([]string, len(edgeFields))
		for i, c := range edgeFields {
			cols[i] = qi(c)
			switch c {
			case src:
				swapped[i] = qi(dst) + " AS " + qi(src)
			case dst:
				swapped[i] = qi(src) + " AS " + qi(dst)
			default:
				swapped[i] = qi(c)
			}
		}
		edges = "SELECT " + strings.Join(cols, ", ") + " FROM " + edgeSrc + "\n  UNION\n  SELECT " + strings.Join(swapped, ", ") + " FROM " + edgeSrc
	}
	ed := "e." + qi(dst)
	var single, appendTo, onPath string
	switch sqlDialectCur {
	case dialectPostgres:
		single, appendTo, onPath = "ARRAY["+qi(src)+"]", "w.__path || "+ed, ed+" = ANY(w.__path)"
	case dialectDataFusion:
		single, appendTo, onPath = "make_array("+qi(src)+")", "array_append(w.__path, "+ed+")", "array_has(w.__path, "+ed+")"
	default:
		single, appendTo, onPath = "["+qi(src)+"]", "list_append(w.__path, "+ed+")", "list_contains(w.__path, "+ed+")"
	}
	keyed := f.mode != ssql.FollowAllPaths

	// The anchor and the step, column by column.
	walkCols := []string{"__sid", "__node", "__depth", "__path"}
	anchor := []string{"__sid AS __sid", qi(src) + " AS __node", "0 AS __depth", single + " AS __path"}
	step := []string{"w.__sid", ed, "w.__depth + 1", appendTo}
	var newTotals []string
	for i, w := range totals {
		col := fmt.Sprintf("__t%d", i+1)
		walkCols = append(walkCols, col)
		anchor = append(anchor, fmt.Sprintf("CAST(0 AS %s) AS %s", totalTypes[i], col))
		weight := "e." + qi(w.Field)
		if f.mode == ssql.FollowLowestSum && i == len(totals)-1 {
			weight = fmt.Sprintf("CASE WHEN %s IS NULL OR %s < 0 THEN error('traverse -lowest-sum: an edge has an absent or negative %s; the search needs weights >= 0') ELSE %s END", weight, weight, w.Field, weight)
		}
		nt := fmt.Sprintf("CAST(w.%s + %s AS %s)", col, weight, totalTypes[i])
		newTotals = append(newTotals, nt)
		step = append(step, nt)
	}
	conds := []string{"NOT " + onPath}
	if f.maxDepth > 0 {
		conds = append(conds, fmt.Sprintf("w.__depth < %d", f.maxDepth))
	}

	var b strings.Builder
	b.WriteString("(WITH RECURSIVE\n")
	// The starts are numbered by their own columns: they are distinct,
	// so that order is total, and __seeds is read twice (the anchor and
	// the final join) — DataFusion evaluates a CTE per reading, and an
	// OVER () numbering of a UNION differed between the two (DFC161 §11).
	over := ""
	if seedCols != nil {
		cols := make([]string, len(seedCols))
		for i, c := range seedCols {
			cols[i] = qi(c)
		}
		over = "ORDER BY " + strings.Join(cols, ", ")
	}
	fmt.Fprintf(&b, "__seeds AS (SELECT *, row_number() OVER (%s) AS __sid FROM (\n  %s\n)),\n", over, seeds)
	fmt.Fprintf(&b, "__edges AS (%s),\n", edges)
	usingKey := ""
	if keyed {
		usingKey = " USING KEY (__sid, __node)"
	}
	fmt.Fprintf(&b, "__walk(%s)%s AS (\n", strings.Join(walkCols, ", "), usingKey)
	fmt.Fprintf(&b, "  SELECT %s FROM __seeds\n  UNION ALL\n", strings.Join(anchor, ", "))
	fmt.Fprintf(&b, "  SELECT %s\n  FROM __walk w JOIN __edges e ON e.%s = w.__node\n", strings.Join(step, ", "), qi(src))
	if keyed {
		// The tie key (DFC161 §9): fewest hops (hops, path, sums);
		// lowest sum (lowest, hops, path, sums).
		key := []string{"w.__depth + 1", appendTo}
		prev := []string{"r.__depth", "r.__path"}
		for i := range newTotals {
			key = append(key, newTotals[i])
			prev = append(prev, fmt.Sprintf("r.__t%d", i+1))
		}
		if f.mode == ssql.FollowLowestSum {
			last := len(key) - 1
			key = append([]string{key[last]}, key[:last]...)
			prev = append([]string{prev[last]}, prev[:last]...)
		}
		b.WriteString("  LEFT JOIN recurring.__walk r ON r.__sid = w.__sid AND r.__node = " + ed + "\n")
		conds = append(conds, fmt.Sprintf("(r.__node IS NULL OR (%s) < (%s))", strings.Join(key, ", "), strings.Join(prev, ", ")))
		fmt.Fprintf(&b, "  WHERE %s\n", strings.Join(conds, " AND "))
		fmt.Fprintf(&b, "  QUALIFY row_number() OVER (PARTITION BY w.__sid, %s ORDER BY %s) = 1\n", ed, strings.Join(key, ", "))
	} else {
		fmt.Fprintf(&b, "  WHERE %s\n", strings.Join(conds, " AND "))
	}
	b.WriteString(")\n")

	// The start's columns, the node reached as DST, then the results.
	var sel []string
	if seedCols != nil {
		for _, c := range seedCols {
			sel = append(sel, "s."+qi(c))
		}
	} else {
		sel = append(sel, "s.* EXCLUDE (__sid)")
	}
	sel = append(sel, "w.__node AS "+qi(dst))
	if f.depth != "" {
		sel = append(sel, "w.__depth AS "+qi(f.depth))
	}
	for i, w := range totals {
		sel = append(sel, fmt.Sprintf("w.__t%d AS %s", i+1, qi(w.Name)))
	}
	if f.path != "" {
		sel = append(sel, "w.__path AS "+qi(f.path))
	}
	fmt.Fprintf(&b, "SELECT %s\nFROM __walk w JOIN __seeds s ON s.__sid = w.__sid)", strings.Join(sel, ", "))
	return b.String()
}
