package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// translateFollowJoin renders `join … -follow` (DFC161) as a recursive
// CTE in a derived table: the seeds numbered (__sid keeps several seeds
// apart), the edges (with their reverses under -both, deduplicated by
// UNION), and __walk — the anchor is every seed at depth 0, each step
// joins the routes the last step found to the edges again, never to a
// node already on the path. That is -all-paths, in every dialect. The
// keyed modes keep one route per (seed, node): DuckDB's USING KEY with
// the recurring table, and an in-round QUALIFY that carries the whole
// tie key — without it two candidates for one node in one round leave
// an arbitrary one (the 565-minute Darwin of DFC161 §4). Postgres and
// DataFusion have no keyed recursion and refuse them, by name.
func translateFollowJoin(q *sqlQuery, args []string, funcFrags []*lib.CodeFragment) error {
	filePath := args[0]
	var node, src string
	var f followSpec
	lowest := false
	for i := 1; i < len(args); i++ {
		need := func(n int) error {
			if i+n >= len(args) {
				return fmt.Errorf("join %s needs %d argument(s)", args[i], n)
			}
			return nil
		}
		switch args[i] {
		case "-on", "-o":
			if err := need(2); err != nil {
				return err
			}
			node, src = args[i+1], args[i+2]
			i += 2
		case "-using", "-u":
			if err := need(1); err != nil {
				return err
			}
			node, src = args[i+1], args[i+1]
			i++
		case "-follow", "-depth", "-path", "-max-depth":
			if err := need(1); err != nil {
				return err
			}
			switch args[i] {
			case "-follow":
				f.dst = args[i+1]
			case "-depth":
				f.depth = args[i+1]
			case "-path":
				f.path = args[i+1]
			case "-max-depth":
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 0 {
					return fmt.Errorf("join -max-depth %s: not a positive number", args[i+1])
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
		case "-type", "-t":
			if i+1 < len(args) && args[i+1] != "inner" {
				return fmt.Errorf("join -follow: -type %s has no traversal meaning", args[i+1])
			}
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("join -follow: %s has no SQL translation", args[i])
			}
		}
	}
	if lowest {
		f.mode = ssql.FollowLowestSum
	}
	if node == "" || f.dst == "" {
		return fmt.Errorf("join -follow needs -on NODE SRC and -follow FIELD")
	}

	// What the dialect cannot do, refused by name.
	if sqlDialectCur != dialectDuckDB {
		switch f.mode {
		case ssql.FollowFewestHops:
			return dialectRefuse("join -follow -fewest-hops (the default mode)", "no keyed recursion (USING KEY) to visit each node once; use -all-paths and pick with window -partition NODE -order … -row-number rn | where -if rn eq 1")
		case ssql.FollowLowestSum:
			return dialectRefuse("join -follow -lowest-sum", "no keyed recursion (USING KEY) to keep each node's cheapest route; use -all-paths -sum and pick with window -partition NODE -order TOTAL -row-number rn | where -if rn eq 1")
		}
		if f.path != "" {
			return dialectRefuse("join -follow -path", "the path is a list, a nested value this dialect does not output")
		}
	}

	// The edges: their source text, columns and kinds.
	var edgeSrc string
	var edgeFields []string
	edgeType := noType
	if strings.HasPrefix(filePath, "/dev/fd/") && len(funcFrags) > 0 {
		side := buildJoinQuery(funcFrags[len(funcFrags)-1])
		if side == nil {
			return fmt.Errorf("join -follow: the edge pipeline has no SQL translation")
		}
		edgeSrc = sqlSubquery("(" + renderSelect(side) + ")")
		body := funcFrags[len(funcFrags)-1].FuncBody
		edgeFields = fragmentFields(body)
		var adv map[string]string
		if len(body) > 0 {
			adv = body[len(body)-1].AdvisoryTypes
		}
		// The side's translation just recorded the integer columns it
		// knows (a computed column included); the fragment's advisory
		// types cover the rest.
		sideInts := map[string]bool{}
		for fl, k := range sqlColumnKinds {
			sideInts[fl] = k == "int"
		}
		edgeType = func(fl string) string {
			if t := advisoryToSchemaType(adv[fl]); t != "" {
				return t
			}
			if sideInts[fl] {
				return lib.TypeInt
			}
			return ""
		}
	} else {
		s, err := dialectSource(filePath)
		if err != nil {
			return err
		}
		edgeSrc = s
		if _, schema, err := readAuxInput(filePath); err == nil && schema != nil {
			edgeFields, edgeType = schema.Fields, schema.TypeOf
		}
	}
	clauses := []ssql.LookupClause{{LeftField: node, RightField: src}}
	if _, err := followCheck(q.columns, edgeFields, edgeType, clauses, f, "", joinOptions{}, asofSpec{}); err != nil {
		return err
	}
	totals := f.weights()
	totalTypes := make([]string, len(totals))
	for i, w := range totals {
		switch base, scale := lib.ParseWireType(edgeType(w.Field)); base {
		case lib.TypeInt, lib.TypeInt128:
			totalTypes[i] = sqlInt128Type()
		case lib.TypeFloat:
			totalTypes[i] = sqlFloatType()
		case lib.TypeDecimal:
			totalTypes[i] = sqlDecimalType(scale)
		default:
			return fmt.Errorf("join -follow: the type of edge field %q is unknown, so its total has no SQL type — read the edges from a CSV file, or use generate go", w.Field)
		}
	}
	if f.both && edgeFields == nil {
		return fmt.Errorf("join -follow -both: the edges' columns are unknown, so their reverses cannot be written — read the edges from a CSV file, or use generate go")
	}

	if q.hasClauses() {
		wrapAsSubquery(q)
	}
	if q.fromClause == "" {
		return fmt.Errorf("join -follow: no seed source to translate")
	}
	seedCols := q.columns
	if seedCols == nil && sqlDialectCur != dialectDuckDB {
		return dialectRefuse("join -follow", "it rewrites the seed's node column and the translator does not know the seed's columns — name them with `include`, or use a CSV/TSV source")
	}
	var outCols []string
	if seedCols != nil {
		outCols = append(append(outCols, seedCols...), f.accNames()...)
	}
	wrapAsSQLOnly(q, "join -follow", func(seeds string) string {
		return sqlSubquery(followCTE(seeds, edgeSrc, edgeFields, node, src, f, totals, totalTypes, seedCols))
	})
	sqlOnlyReset(q, outCols)
	if f.depth != "" {
		sqlColumnKinds[f.depth] = "int"
	}
	for _, w := range totals {
		delete(sqlColumnKinds, w.Name) // an int128 total: not an int64 for the checked arithmetic
	}
	return nil
}

// followCTE is the recursive CTE text (translateFollowJoin).
func followCTE(seeds, edgeSrc string, edgeFields []string, node, src string, f followSpec, totals []ssql.FollowSum, totalTypes []string, seedCols []string) string {
	qi := quoteIdent
	dst := f.dst
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
		single, appendTo, onPath = "ARRAY["+qi(node)+"]", "w.__path || "+ed, ed+" = ANY(w.__path)"
	case dialectDataFusion:
		single, appendTo, onPath = "make_array("+qi(node)+")", "array_append(w.__path, "+ed+")", "array_has(w.__path, "+ed+")"
	default:
		single, appendTo, onPath = "["+qi(node)+"]", "list_append(w.__path, "+ed+")", "list_contains(w.__path, "+ed+")"
	}
	keyed := f.mode != ssql.FollowAllPaths

	// The anchor and the step, column by column.
	walkCols := []string{"__sid", "__node", "__depth", "__path"}
	anchor := []string{"__sid AS __sid", qi(node) + " AS __node", "0 AS __depth", single + " AS __path"}
	step := []string{"w.__sid", ed, "w.__depth + 1", appendTo}
	var newTotals []string
	for i, w := range totals {
		col := fmt.Sprintf("__t%d", i+1)
		walkCols = append(walkCols, col)
		anchor = append(anchor, fmt.Sprintf("CAST(0 AS %s) AS %s", totalTypes[i], col))
		weight := "e." + qi(w.Field)
		if f.mode == ssql.FollowLowestSum && i == len(totals)-1 {
			weight = fmt.Sprintf("CASE WHEN %s IS NULL OR %s < 0 THEN error('join -follow -lowest-sum: an edge has an absent or negative %s; the search needs weights >= 0') ELSE %s END", weight, weight, w.Field, weight)
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
	fmt.Fprintf(&b, "__seeds AS (SELECT *, row_number() OVER () AS __sid FROM %s),\n", seeds)
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

	// The seed's columns with the node replaced, then the results.
	var sel []string
	if seedCols != nil {
		for _, c := range seedCols {
			if c == node {
				sel = append(sel, "w.__node AS "+qi(c))
			} else {
				sel = append(sel, "s."+qi(c))
			}
		}
	} else {
		sel = append(sel, "s.* EXCLUDE (__sid) REPLACE (w.__node AS "+qi(node)+")")
	}
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
