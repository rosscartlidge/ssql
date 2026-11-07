package commands

import (
	"slices"
	"strings"
)

// joinClause is one clause of a join: its arguments, and the columns it brings
// in, each with the right-side column it comes from.
type joinClause struct {
	args    []string
	origins map[string]string // output column → right column
}

// joinShape is what the pushdown rule needs to know about a join stage. A plain
// join brings in every right column but its key; a lookup (-as) brings in only
// the named ones, under their new names, clause by clause.
type joinShape struct {
	clauses      []joinClause
	usingKeys    map[string]bool // -using: the key is on both sides
	rightMovable bool            // false: filtering the right side changes the answer
	leftMovable  bool
}

// outputs maps every column the join brings in to its clause.
func (s *joinShape) outputs() map[string]int {
	out := map[string]int{}
	for i, c := range s.clauses {
		for name := range c.origins {
			out[name] = i
		}
	}
	return out
}

// parseJoinShape reads a join's arguments. ok is false for a form the rule does
// not reason about: -suffix and -exclude-* change which names exist, so a
// predicate on a name they removed must stay put and fail as it does in exec.
func parseJoinShape(cmd *pipelineCmd, rightFields []string) (*joinShape, bool) {
	s := &joinShape{usingKeys: map[string]bool{}, rightMovable: true, leftMovable: true}
	cur := joinClause{origins: map[string]string{}}
	rightKeys := map[string]bool{}
	lookup := false
	args := cmd.JoinArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := 0
		switch a {
		case "-":
			s.clauses = append(s.clauses, cur)
			cur = joinClause{origins: map[string]string{}}
			continue
		case "-using", "-u":
			take = 1
			if i+1 < len(args) {
				s.usingKeys[args[i+1]] = true
				rightKeys[args[i+1]] = true
			}
		case "-on", "-o":
			take = 2
			if i+2 < len(args) {
				rightKeys[args[i+2]] = true
			}
		case "-as":
			take = 2
			if i+2 < len(args) {
				lookup = true
				cur.origins[args[i+2]] = args[i+1]
			}
		case "-type", "-t":
			take = 1
			if i+1 < len(args) {
				switch args[i+1] {
				case "inner":
				case "left":
					s.rightMovable = false
				case "right", "full":
					s.rightMovable, s.leftMovable = false, false
				default:
					return nil, false
				}
			}
		case "-asof":
			take = 1
			s.rightMovable = false // the match depends on which right rows exist
		case "-asof-on":
			take = 2
			s.rightMovable = false
		case "-tolerance":
			take = 1
		case "-after", "-strict":
		default:
			return nil, false
		}
		if i+take >= len(args) && take > 0 {
			return nil, false
		}
		cur.args = append(cur.args, args[i:i+take+1]...)
		i += take
	}
	s.clauses = append(s.clauses, cur)
	if lookup || len(s.clauses) > 1 {
		// Every clause of a split must name what it brings in, and -type
		// and the ASOF flags belong to the whole stage.
		for _, c := range s.clauses {
			if len(c.origins) == 0 {
				return nil, false
			}
		}
		if len(s.clauses) > 1 && (!s.rightMovable || !s.leftMovable) {
			return nil, false
		}
		return s, true
	}
	for _, f := range rightFields {
		if !rightKeys[f] {
			s.clauses[0].origins[f] = f
		}
	}
	return s, true
}

// sideFileColumns is the columns a join's right side has: the plain file's
// header, or what its inner pipeline leaves.
func sideFileColumns(cmd *pipelineCmd) ([]string, bool) {
	if cmd.JoinIsProcessSub {
		return columnsThrough(cmd.JoinProcSubCmds, len(cmd.JoinProcSubCmds))
	}
	names, err := readFieldNames(cmd.JoinFile, "")
	return names, err == nil
}

// columnsThrough walks a pipeline to just before stage end, asking each stage
// what it does to the columns. Not knowing is an answer: the rule then leaves
// the pipeline alone.
func columnsThrough(cmds []*pipelineCmd, end int) ([]string, bool) {
	var cols []string
	sourced := false
	for _, c := range cmds[:end] {
		if c.Removed {
			continue
		}
		switch {
		case c.Kind == "from":
			if sourced || c.IsSSH || c.IsCatalog || c.IsMultiFile || slices.Contains(c.RawArgs, "-columns") {
				return nil, false
			}
			file, format := getFromFileAndFormat(c)
			if file == "" {
				return nil, false
			}
			names, err := readFieldNames(file, format)
			if err != nil {
				return nil, false
			}
			cols, sourced = names, true
		case !sourced:
			return nil, false
		case c.IsJoin:
			right, ok := sideFileColumns(c)
			if !ok {
				return nil, false
			}
			shape, ok := parseJoinShape(c, right)
			if !ok {
				return nil, false
			}
			for name := range shape.outputs() {
				if !slices.Contains(cols, name) {
					cols = append(cols, name)
				}
			}
		default:
			out, ok := lookupSchemaOp(c.Kind)(nil, cols, c.RawArgs)
			if !ok {
				return nil, false
			}
			cols = out
		}
	}
	return cols, sourced
}

// ruleJoinPredicatePushdown moves where conditions off the far side of a join.
// A condition on a column the join brings in becomes a filter on the right side,
// under that column's name there; one on a left column moves before the join,
// where an earlier join can take it the same way. It looks past an include or
// exclude that keeps the column. A lookup whose clauses share one table is split
// into one join per clause, since a filter on the table restricts only its own
// clause. The columns come from the stages themselves, so a condition on a name
// the join does not produce stays where it is and fails as it does in exec.
func ruleJoinPredicatePushdown(cmds []*pipelineCmd) ([]*pipelineCmd, []ruleApplication) {
	var rules []ruleApplication
	for {
		next, rule, fired := pushOneJoinPredicate(cmds)
		if !fired {
			return cmds, rules
		}
		cmds = next
		rules = append(rules, rule)
	}
}

// pushOneJoinPredicate moves what it can across one join, last join first.
// Every firing moves a condition strictly toward the source or into a right
// side, so the rule terminates.
func pushOneJoinPredicate(cmds []*pipelineCmd) ([]*pipelineCmd, ruleApplication, bool) {
	for i := len(cmds) - 1; i >= 0; i-- {
		if cmds[i].Removed || !cmds[i].IsJoin {
			continue
		}
		if next, rule, fired := pushAcrossJoin(cmds, i); fired {
			return next, rule, true
		}
	}
	return cmds, ruleApplication{}, false
}

// joinHopStage reports a stage a where can move back across: it keeps or drops
// columns by name and neither adds rows nor removes them.
func joinHopStage(c *pipelineCmd) bool {
	if c.Kind != "include" && c.Kind != "exclude" {
		return false
	}
	return !slices.ContainsFunc(c.RawArgs, func(a string) bool { return strings.HasPrefix(a, "-") })
}

func pushAcrossJoin(cmds []*pipelineCmd, i int) ([]*pipelineCmd, ruleApplication, bool) {
	none := ruleApplication{}
	var hops []*pipelineCmd
	w := nextNonRemoved(cmds, i)
	for w != -1 && joinHopStage(cmds[w]) {
		hops = append(hops, cmds[w])
		w = nextNonRemoved(cmds, w)
	}
	if w == -1 || cmds[w].Kind != "where" || whereHasNegationFlags(cmds[w].RawArgs) {
		return cmds, none, false
	}
	clauses := parseWhereArgs(cmds[w].RawArgs)
	if len(clauses) != 1 || len(clauses[0].Exprs) > 0 || len(clauses[0].Conditions) == 0 {
		return cmds, none, false // an OR or an expression can span both sides
	}
	right, ok := sideFileColumns(cmds[i])
	if !ok {
		return cmds, none, false
	}
	shape, ok := parseJoinShape(cmds[i], right)
	if !ok {
		return cmds, none, false
	}
	left, ok := columnsThrough(cmds, i)
	if !ok {
		return cmds, none, false
	}

	// A condition must name a column that reaches the where. If it does not,
	// the stage's own error is the answer, and it stays to report it.
	outputs := shape.outputs()
	after := slices.Clone(left)
	for name := range outputs {
		after = append(after, name)
	}
	for _, h := range hops {
		if after, ok = lookupSchemaOp(h.Kind)(nil, after, h.RawArgs); !ok {
			return cmds, none, false
		}
	}

	rightConds := make([][]whereCondition, len(shape.clauses))
	var leftConds, stay []whereCondition
	pushed := false
	for _, cond := range clauses[0].Conditions {
		clause, fromJoin := outputs[cond.Field]
		inLeft := slices.Contains(left, cond.Field)
		switch {
		case !slices.Contains(after, cond.Field):
			stay = append(stay, cond)
		case fromJoin && !inLeft && shape.rightMovable:
			moved := cond
			moved.Field = shape.clauses[clause].origins[cond.Field]
			rightConds[clause] = append(rightConds[clause], moved)
			pushed = true
		case inLeft && !fromJoin && !shape.usingKeys[cond.Field] && shape.leftMovable:
			leftConds = append(leftConds, cond)
			pushed = true
		default:
			stay = append(stay, cond)
		}
	}
	if !pushed {
		return cmds, none, false
	}

	before := []string{renderCmd(cmds[i])}
	for _, h := range hops {
		before = append(before, renderCmd(h))
	}
	before = append(before, renderCmd(cmds[w]))

	joins, ok := filteredJoins(cmds[i], shape, rightConds)
	if !ok {
		return cmds, none, false
	}
	if len(stay) == 0 {
		cmds[w].Removed = true
	} else {
		cmds[w].RawArgs = buildWhereArgs([]whereClause{{Conditions: stay}})
	}

	var inserted []*pipelineCmd
	if len(leftConds) > 0 {
		inserted = append(inserted, &pipelineCmd{Kind: "where", RawArgs: buildWhereArgs([]whereClause{{Conditions: leftConds}})})
	}
	inserted = append(inserted, joins...)
	out := make([]*pipelineCmd, 0, len(cmds)+len(inserted))
	out = append(append(append(out, cmds[:i]...), inserted...), cmds[i+1:]...)

	var afterText []string
	for _, c := range inserted {
		afterText = append(afterText, renderCmd(c))
	}
	if len(stay) > 0 {
		afterText = append(afterText, renderCmd(cmds[w]))
	}
	return out, ruleApplication{
		Rule:   "join-predicate-pushdown",
		Before: strings.Join(before, " | "),
		After:  strings.Join(afterText, " | "),
	}, true
}

// filteredJoins renders the join with each clause's conditions on its right
// side, one join per clause when the clauses need different filters. ok is
// false when that takes an inner pipeline that cannot be read twice.
func filteredJoins(join *pipelineCmd, shape *joinShape, conds [][]whereCondition) ([]*pipelineCmd, bool) {
	filtered := 0
	for _, c := range conds {
		if len(c) > 0 {
			filtered++
		}
	}
	if filtered == 0 {
		return []*pipelineCmd{join}, true
	}
	if len(shape.clauses) == 1 {
		addRightWhere(join, conds[0])
		return []*pipelineCmd{join}, true
	}
	if join.JoinIsProcessSub && !sideIsPlainSource(join.JoinProcSubCmds) {
		return nil, false
	}
	out := make([]*pipelineCmd, len(shape.clauses))
	for i, clause := range shape.clauses {
		split := *join
		split.JoinArgs = clause.args
		split.RawArgs = append([]string{join.JoinFile}, clause.args...)
		split.JoinRightWhere = nil
		split.JoinProcSubCmds = slices.Clone(join.JoinProcSubCmds)
		if len(conds[i]) > 0 {
			addRightWhere(&split, conds[i])
		}
		out[i] = &split
	}
	return out, true
}

// sideIsPlainSource reports an inner pipeline that is one file's read, which
// gives the same rows when two joins each run it.
func sideIsPlainSource(inner []*pipelineCmd) bool {
	if len(inner) != 1 || inner[0].Kind != "from" || inner[0].IsSSH || inner[0].IsCatalog {
		return false
	}
	file, _ := getFromFileAndFormat(inner[0])
	return file != ""
}

func addRightWhere(join *pipelineCmd, conds []whereCondition) {
	args := buildWhereArgs([]whereClause{{Conditions: conds}})
	if join.JoinIsProcessSub {
		join.JoinProcSubCmds = append(join.JoinProcSubCmds, &pipelineCmd{Kind: "where", RawArgs: args})
		return
	}
	join.JoinRightWhere = append(join.JoinRightWhere, args...)
}
