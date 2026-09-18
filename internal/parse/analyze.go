package parse

import (
	"errors"
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"
)

// Statement is one parsed SQL statement.
type Statement struct {
	SQL    string
	Tree   *pg_query.ParseResult
	Node   *pg_query.Node // RawStmt.Stmt
	HasJev bool
}

// Split breaks a script into individual statements on top-level
// semicolons, using the Postgres lexer so strings, dollar quotes and
// comments are respected. Unlike pg_query's splitter it keeps statements
// that do not parse, so the server (or ParseOne) can report the error.
func Split(sql string) ([]string, error) {
	res, err := pg_query.Scan(sql)
	if err != nil {
		return []string{sql}, nil
	}
	var out []string
	start := 0
	for _, t := range res.Tokens {
		if t.Token == pg_query.Token_ASCII_59 { // ';'
			if s := strings.TrimSpace(sql[start:t.Start]); s != "" && !onlyComments(s) {
				out = append(out, s)
			}
			start = int(t.End)
		}
	}
	if s := strings.TrimSpace(sql[start:]); s != "" && !onlyComments(s) {
		out = append(out, s)
	}
	return out, nil
}

// onlyComments reports whether s has no SQL tokens besides comments.
func onlyComments(s string) bool {
	res, err := pg_query.Scan(s)
	if err != nil {
		return false
	}
	for _, t := range res.Tokens {
		if t.Token != pg_query.Token_SQL_COMMENT && t.Token != pg_query.Token_C_COMMENT {
			return false
		}
	}
	return true
}

// ParseOne parses exactly one statement.
func ParseOne(sql string) (*Statement, error) {
	tree, err := pg_query.Parse(sql)
	if err != nil {
		return nil, err
	}
	if len(tree.Stmts) == 0 {
		return nil, errors.New("empty statement")
	}
	if len(tree.Stmts) > 1 {
		return nil, errors.New("expected a single statement")
	}
	st := &Statement{SQL: sql, Tree: tree, Node: tree.Stmts[0].Stmt}
	st.HasJev = ContainsJev(st.Node)
	return st, nil
}

// TargetKind classifies a SELECT-list entry.
type TargetKind int

const (
	TargetPlain TargetKind = iota // sent to Postgres as-is
	TargetJev                     // computed in Go from a TypeSafe answer
	TargetAgg                     // aggregate computed in Go (grouped mode)
)

// Aggregate is COUNT/SUM/AVG/MIN/MAX over a plain expression.
type Aggregate struct {
	Fn       string
	Star     bool
	Distinct bool
	Arg      *pg_query.Node // nil when Star
}

// Target is one output column of the user's SELECT.
type Target struct {
	Kind   TargetKind
	Name   string // explicit alias ("" = let Postgres / the default name decide)
	Node   *pg_query.Node
	Text   string // deparsed expression
	Star   bool   // plain: * or alias.*
	Hidden bool   // added for GROUP BY / ORDER BY, not printed
	Call   *Call
	Agg    *Aggregate
}

// OutputName is the column header for this target.
func (t Target) OutputName() string {
	if t.Name != "" {
		return t.Name
	}
	switch t.Kind {
	case TargetJev:
		return t.Call.Fn
	case TargetAgg:
		return t.Agg.Fn
	}
	return ""
}

// Predicate is a boolean jev() term from WHERE.
type Predicate struct {
	Call   *Call
	Negate bool
}

// OrderKey sorts by a target (possibly hidden).
type OrderKey struct {
	Target     int
	Desc       bool
	NullsFirst bool
}

// FromTable is a base relation in FROM with the alias it is visible as.
type FromTable struct {
	Alias  string
	Schema string
	Rel    string
}

// Analysis is everything the rewriter and executor need from a jev SELECT.
type Analysis struct {
	Stmt       *Statement
	Select     *pg_query.SelectStmt
	Targets    []Target
	Predicates []Predicate
	Where      *pg_query.Node // WHERE with jev terms removed (nil if none left)
	Grouped    bool
	GroupKeys  []int // target indexes
	OrderKeys  []OrderKey
	Limit      *int64
	Offset     *int64
	Tables     []FromTable
	Sources    map[string]Source // by Source.Key()
	Calls      []*Call
}

// OrderHasJev reports whether any ORDER BY key depends on a jev target.
func (a *Analysis) OrderHasJev() bool {
	for _, k := range a.OrderKeys {
		if a.Targets[k.Target].Kind != TargetPlain {
			return true
		}
	}
	return false
}

// UserTargetCount is the number of visible output columns.
func (a *Analysis) UserTargetCount() int {
	n := 0
	for _, t := range a.Targets {
		if !t.Hidden {
			n++
		}
	}
	return n
}

// Analyze validates a statement containing jev_* calls and extracts the plan inputs.
func Analyze(st *Statement) (*Analysis, error) {
	if !st.HasJev {
		return nil, errors.New("statement has no jev_* calls")
	}
	sel := st.Node.GetSelectStmt()
	if sel == nil {
		return nil, fmt.Errorf("jev_* can only be used in a SELECT; found it in %s", stmtName(st.Node))
	}
	a := &Analysis{Stmt: st, Select: sel, Sources: map[string]Source{}}

	// Position checks with useful messages.
	if sel.WithClause != nil && ContainsJev(sel.WithClause) {
		return nil, errors.New("jev_* inside a CTE (WITH ...) is not supported; move it to the outer SELECT")
	}
	if sel.Op != pg_query.SetOperation_SETOP_NONE {
		return nil, errors.New("jev_* with UNION/INTERSECT/EXCEPT is not supported in v1")
	}
	if len(sel.ValuesLists) > 0 {
		return nil, errors.New("jev_* in VALUES is not supported")
	}
	if len(sel.DistinctClause) > 0 {
		return nil, errors.New("SELECT DISTINCT with jev_* is not supported in v1; use GROUP BY")
	}
	for _, f := range sel.FromClause {
		if ContainsJev(f) {
			if f.GetRangeSubselect() != nil {
				return nil, errors.New("jev_* inside a subquery in FROM is not supported; use it in the outer SELECT")
			}
			return nil, errors.New("jev_* inside FROM/JOIN ... ON is not supported; move it to WHERE")
		}
	}
	if err := checkSubLinks(st.Node); err != nil {
		return nil, err
	}
	if sel.HavingClause != nil {
		if ContainsJev(sel.HavingClause) {
			return nil, errors.New("HAVING jev_*(...) is not supported in v1")
		}
		return nil, errors.New("HAVING is not supported together with jev_* in v1")
	}
	if err := checkWindows(sel); err != nil {
		return nil, err
	}
	if len(sel.LockingClause) > 0 {
		return nil, errors.New("FOR UPDATE/SHARE with jev_* is not supported")
	}

	// FROM tables.
	for _, f := range sel.FromClause {
		if err := a.collectTables(f); err != nil {
			return nil, err
		}
	}

	// Targets.
	for _, tn := range sel.TargetList {
		rt := tn.GetResTarget()
		if rt == nil {
			return nil, fmt.Errorf("unexpected SELECT list entry %s", DeparseExpr(tn))
		}
		t, err := a.classifyTarget(rt.Val)
		if err != nil {
			return nil, err
		}
		t.Name = rt.Name
		a.Targets = append(a.Targets, t)
	}
	a.Grouped = len(sel.GroupClause) > 0
	for _, t := range a.Targets {
		if t.Kind == TargetAgg {
			a.Grouped = true
		}
	}
	if a.Grouped {
		for _, t := range a.Targets {
			if t.Kind == TargetPlain && t.Star {
				return nil, errors.New("SELECT * is not allowed with GROUP BY / aggregates")
			}
		}
	}

	// WHERE.
	rest, preds, err := a.splitWhere(sel.WhereClause)
	if err != nil {
		return nil, err
	}
	a.Where, a.Predicates = rest, preds

	// GROUP BY.
	for _, g := range sel.GroupClause {
		if g.GetGroupingSet() != nil {
			return nil, errors.New("GROUPING SETS / ROLLUP / CUBE with jev_* are not supported")
		}
		idx, err := a.resolveRef(g, true)
		if err != nil {
			return nil, fmt.Errorf("GROUP BY: %w", err)
		}
		if a.Targets[idx].Kind == TargetAgg {
			return nil, errors.New("GROUP BY cannot reference an aggregate")
		}
		a.GroupKeys = append(a.GroupKeys, idx)
	}
	if a.Grouped {
		for i, t := range a.Targets {
			if t.Kind == TargetPlain && !t.Hidden && !containsInt(a.GroupKeys, i) {
				return nil, fmt.Errorf("column %s must appear in the GROUP BY clause or be used in an aggregate function", t.Text)
			}
		}
	}

	// ORDER BY.
	for _, sn := range sel.SortClause {
		sb := sn.GetSortBy()
		if sb == nil {
			continue
		}
		idx, err := a.resolveRef(sb.Node, false)
		if err != nil {
			return nil, fmt.Errorf("ORDER BY: %w", err)
		}
		desc := sb.SortbyDir == pg_query.SortByDir_SORTBY_DESC
		nullsFirst := desc // Postgres default: NULLS LAST for ASC, NULLS FIRST for DESC
		switch sb.SortbyNulls {
		case pg_query.SortByNulls_SORTBY_NULLS_FIRST:
			nullsFirst = true
		case pg_query.SortByNulls_SORTBY_NULLS_LAST:
			nullsFirst = false
		}
		a.OrderKeys = append(a.OrderKeys, OrderKey{Target: idx, Desc: desc, NullsFirst: nullsFirst})
	}

	// LIMIT / OFFSET.
	if sel.LimitOption == pg_query.LimitOption_LIMIT_OPTION_WITH_TIES {
		return nil, errors.New("FETCH FIRST ... WITH TIES is not supported with jev_*")
	}
	if a.Limit, err = constInt64(sel.LimitCount, "LIMIT"); err != nil {
		return nil, err
	}
	if a.Offset, err = constInt64(sel.LimitOffset, "OFFSET"); err != nil {
		return nil, err
	}

	// Every jev call must have been accounted for.
	all := JevCalls(st.Node)
	if len(all) != len(a.Calls) {
		for _, fc := range all {
			txt := DeparseExpr(&pg_query.Node{Node: &pg_query.Node_FuncCall{FuncCall: fc}})
			seen := false
			for _, c := range a.Calls {
				if c.Text == txt {
					seen = true
					break
				}
			}
			if !seen {
				return nil, fmt.Errorf("%s: jev_* must be a top-level SELECT expression, an AND term in WHERE, or a GROUP BY / ORDER BY key", txt)
			}
		}
		return nil, errors.New("jev_* call in an unsupported position")
	}

	// Sources must resolve to known aliases, and be judgeable.
	for _, c := range a.Calls {
		if c.Source.IsAlias {
			if _, ok := a.table(c.Source.Alias); !ok {
				hint := ""
				if len(a.Tables) > 0 {
					names := make([]string, len(a.Tables))
					for i, t := range a.Tables {
						names[i] = t.Alias
					}
					hint = " (FROM aliases: " + strings.Join(names, ", ") + ")"
				}
				return nil, fmt.Errorf("%s: unknown FROM alias %q%s", c.Text, c.Source.Alias, hint)
			}
		}
	}
	return a, nil
}

func (a *Analysis) table(alias string) (FromTable, bool) {
	for _, t := range a.Tables {
		if t.Alias == alias {
			return t, true
		}
	}
	return FromTable{}, false
}

func (a *Analysis) collectTables(n *pg_query.Node) error {
	switch v := n.Node.(type) {
	case *pg_query.Node_RangeVar:
		rv := v.RangeVar
		alias := rv.Relname
		if rv.Alias != nil && rv.Alias.Aliasname != "" {
			alias = rv.Alias.Aliasname
		}
		a.Tables = append(a.Tables, FromTable{Alias: alias, Schema: rv.Schemaname, Rel: rv.Relname})
	case *pg_query.Node_JoinExpr:
		if err := a.collectTables(v.JoinExpr.Larg); err != nil {
			return err
		}
		return a.collectTables(v.JoinExpr.Rarg)
	case *pg_query.Node_RangeSubselect, *pg_query.Node_RangeFunction:
		// Not judgeable, but harmless as long as no jev call names them.
	default:
		return fmt.Errorf("unsupported FROM item %s", DeparseExpr(n))
	}
	return nil
}

func (a *Analysis) addCall(c *Call) {
	a.Calls = append(a.Calls, c)
	if _, ok := a.Sources[c.Source.Key()]; !ok {
		a.Sources[c.Source.Key()] = c.Source
	}
}

func (a *Analysis) classifyTarget(val *pg_query.Node) (Target, error) {
	t := Target{Node: val, Text: DeparseExpr(val)}
	if fc := val.GetFuncCall(); fc != nil {
		name := FuncName(fc)
		if IsJevFunc(name) {
			c, err := ParseCall(fc)
			if err != nil {
				return t, err
			}
			a.addCall(c)
			t.Kind, t.Call = TargetJev, c
			return t, nil
		}
		if isAggName(name) && fc.Over == nil {
			if ContainsJev(fc) {
				return t, fmt.Errorf("%s: aggregating over jev_* is not supported in v1", t.Text)
			}
			if fc.AggFilter != nil || len(fc.AggOrder) > 0 {
				return t, fmt.Errorf("%s: FILTER / ORDER BY inside aggregates is not supported with jev_*", t.Text)
			}
			agg := &Aggregate{Fn: name, Star: fc.AggStar, Distinct: fc.AggDistinct}
			if !fc.AggStar {
				if len(fc.Args) != 1 {
					return t, fmt.Errorf("%s: expected one argument", t.Text)
				}
				agg.Arg = fc.Args[0]
			}
			t.Kind, t.Agg = TargetAgg, agg
			return t, nil
		}
		if isOtherAgg(name) && fc.Over == nil {
			return t, fmt.Errorf("%s: GROUP BY with jev_* only supports COUNT and SUM/AVG/MIN/MAX of numeric columns in v1", t.Text)
		}
	}
	if ContainsJev(val) {
		return t, fmt.Errorf("%s: jev_* must be the whole SELECT expression (wrap or compare it in a CTE-less outer query is not supported in v1)", t.Text)
	}
	if cr := val.GetColumnRef(); cr != nil && len(cr.Fields) > 0 {
		if cr.Fields[len(cr.Fields)-1].GetAStar() != nil {
			t.Star = true
		}
	}
	return t, nil
}

// splitWhere removes jev() AND-terms from the WHERE tree.
func (a *Analysis) splitWhere(n *pg_query.Node) (*pg_query.Node, []Predicate, error) {
	if n == nil {
		return nil, nil, nil
	}
	if !ContainsJev(n) {
		return n, nil, nil
	}
	if fc := n.GetFuncCall(); fc != nil && IsJevFunc(FuncName(fc)) {
		p, err := a.predicate(fc, false)
		if err != nil {
			return nil, nil, err
		}
		return nil, []Predicate{p}, nil
	}
	if be := n.GetBoolExpr(); be != nil {
		switch be.Boolop {
		case pg_query.BoolExprType_AND_EXPR:
			var rest []*pg_query.Node
			var preds []Predicate
			for _, arg := range be.Args {
				r, p, err := a.splitWhere(arg)
				if err != nil {
					return nil, nil, err
				}
				if r != nil {
					rest = append(rest, r)
				}
				preds = append(preds, p...)
			}
			switch len(rest) {
			case 0:
				return nil, preds, nil
			case 1:
				return rest[0], preds, nil
			}
			return pg_query.MakeBoolExprNode(pg_query.BoolExprType_AND_EXPR, rest, be.Location), preds, nil
		case pg_query.BoolExprType_NOT_EXPR:
			if fc := be.Args[0].GetFuncCall(); fc != nil && IsJevFunc(FuncName(fc)) {
				p, err := a.predicate(fc, true)
				if err != nil {
					return nil, nil, err
				}
				return nil, []Predicate{p}, nil
			}
			return nil, nil, fmt.Errorf("%s: NOT over an expression containing jev_* is only supported as NOT jev(...)", DeparseExpr(n))
		case pg_query.BoolExprType_OR_EXPR:
			return nil, nil, fmt.Errorf("%s: OR jev(...) is not supported in v1 (every row would need judging); split into two queries or use AND", DeparseExpr(n))
		}
	}
	return nil, nil, fmt.Errorf("%s: jev_* in WHERE must be a bare AND term like jev(alias, 'condition'); comparisons such as jev_prob(...) > 0.7 are not supported in v1, use jev(alias, 'condition', 0.7)", DeparseExpr(n))
}

func (a *Analysis) predicate(fc *pg_query.FuncCall, negate bool) (Predicate, error) {
	c, err := ParseCall(fc)
	if err != nil {
		return Predicate{}, err
	}
	if c.Fn != "jev" {
		return Predicate{}, fmt.Errorf("%s: only jev(alias, 'condition' [, threshold]) is boolean; %s cannot be used directly in WHERE", c.Text, c.Fn)
	}
	a.addCall(c)
	return Predicate{Call: c, Negate: negate}, nil
}

// resolveRef maps a GROUP BY / ORDER BY expression to a target index,
// adding a hidden target when nothing in the SELECT list matches.
func (a *Analysis) resolveRef(n *pg_query.Node, grouping bool) (int, error) {
	visible := func() []int {
		var idx []int
		for i, t := range a.Targets {
			if !t.Hidden {
				idx = append(idx, i)
			}
		}
		return idx
	}
	if ac := n.GetAConst(); ac != nil {
		if iv := ac.GetIval(); iv != nil {
			vis := visible()
			pos := int(iv.Ival)
			if pos < 1 || pos > len(vis) {
				return 0, fmt.Errorf("position %d is not in select list", pos)
			}
			return vis[pos-1], nil
		}
		return 0, fmt.Errorf("non-integer constant %s", DeparseExpr(n))
	}
	text := DeparseExpr(n)
	if cr := n.GetColumnRef(); cr != nil && len(cr.Fields) == 1 {
		if s := cr.Fields[0].GetString_(); s != nil {
			for i, t := range a.Targets {
				if !t.Hidden && t.Name == s.Sval {
					return i, nil
				}
			}
		}
	}
	for i, t := range a.Targets {
		if t.Text == text {
			return i, nil
		}
	}
	// No match: add a hidden target.
	t, err := a.classifyTarget(n)
	if err != nil {
		return 0, err
	}
	if t.Kind == TargetAgg && grouping {
		return 0, fmt.Errorf("aggregate %s cannot be a GROUP BY key", text)
	}
	if a.Grouped && t.Kind == TargetPlain && !grouping {
		return 0, fmt.Errorf("%s must appear in the SELECT list or GROUP BY when jev_* is used with GROUP BY", text)
	}
	if t.Star {
		return 0, fmt.Errorf("cannot use * here")
	}
	t.Hidden = true
	a.Targets = append(a.Targets, t)
	return len(a.Targets) - 1, nil
}

func checkSubLinks(n proto.Message) error {
	var err error
	Walk(n, func(m proto.Message) bool {
		if err != nil {
			return false
		}
		if sl, ok := m.(*pg_query.SubLink); ok && ContainsJev(sl) {
			err = errors.New("jev_* inside a subquery is not supported in v1; run the inner query with jevql instead")
			return false
		}
		return true
	})
	return err
}

func checkWindows(sel *pg_query.SelectStmt) error {
	var err error
	Walk(sel, func(m proto.Message) bool {
		if err != nil {
			return false
		}
		if fc, ok := m.(*pg_query.FuncCall); ok && fc.Over != nil && ContainsJev(fc) {
			err = fmt.Errorf("%s: window functions over jev_* are not supported", DeparseExpr(&pg_query.Node{Node: &pg_query.Node_FuncCall{FuncCall: fc}}))
			return false
		}
		return true
	})
	return err
}

func constInt64(n *pg_query.Node, what string) (*int64, error) {
	if n == nil {
		return nil, nil
	}
	ac := n.GetAConst()
	if ac == nil {
		return nil, fmt.Errorf("%s must be an integer literal when using jev_*", what)
	}
	if ac.Isnull { // LIMIT ALL
		return nil, nil
	}
	if iv := ac.GetIval(); iv != nil {
		v := int64(iv.Ival)
		return &v, nil
	}
	return nil, fmt.Errorf("%s must be an integer literal when using jev_*", what)
}

func isAggName(n string) bool {
	switch n {
	case "count", "sum", "avg", "min", "max":
		return true
	}
	return false
}

func isOtherAgg(n string) bool {
	switch n {
	case "string_agg", "array_agg", "json_agg", "jsonb_agg", "bool_and", "bool_or", "every",
		"stddev", "variance", "percentile_cont", "percentile_disc", "mode", "json_object_agg", "jsonb_object_agg",
		"stddev_pop", "stddev_samp", "var_pop", "var_samp", "bit_and", "bit_or", "xmlagg":
		return true
	}
	return false
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func stmtName(n *pg_query.Node) string {
	switch n.Node.(type) {
	case *pg_query.Node_InsertStmt:
		return "INSERT"
	case *pg_query.Node_UpdateStmt:
		return "UPDATE"
	case *pg_query.Node_DeleteStmt:
		return "DELETE"
	case *pg_query.Node_MergeStmt:
		return "MERGE"
	case *pg_query.Node_CreateStmt, *pg_query.Node_CreateTableAsStmt, *pg_query.Node_ViewStmt, *pg_query.Node_IndexStmt, *pg_query.Node_AlterTableStmt:
		return "DDL"
	case *pg_query.Node_ExplainStmt:
		return "EXPLAIN (use --explain instead)"
	case *pg_query.Node_CopyStmt:
		return "COPY"
	}
	t := fmt.Sprintf("%T", n.Node)
	return strings.TrimPrefix(strings.TrimPrefix(t, "*pg_query.Node_"), "pg_query.Node_")
}
