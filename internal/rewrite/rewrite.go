// Package rewrite turns an analysed jev SELECT into the "collect" SQL that
// Postgres runs, plus the column layout the executor needs to map the
// result back onto the user's SELECT list.
package rewrite

import (
	"context"
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"

	"github.com/mclaren/jevpsql/internal/parse"
)

// Column is one attribute of a relation.
type Column struct {
	Name string
	Type string // format_type() output, e.g. "bytea", "text[]"
}

// Catalog resolves relation columns.
type Catalog interface {
	Columns(ctx context.Context, schema, rel string) ([]Column, error)
}

// Options tune what the collect query fetches for row objects.
type Options struct {
	Columns []string // --columns: override the alias-form column set
}

// SourceField is one key of a row object and the collect column it comes from.
type SourceField struct {
	Name   string // JSON key
	Column int    // index in the collect result
}

// SourcePlan is a row-object source with its collect columns.
type SourcePlan struct {
	Key    string
	Source parse.Source
	Fields []SourceField
}

// TargetLayout says where a target's data sits in the collect result.
type TargetLayout struct {
	Start int  // first collect column (-1 when none)
	Count int  // number of columns, -1 for star (read until separator)
	Sep   bool // a separator column follows (star only)
}

// SepName is the alias of the sentinel column emitted after a `*` target.
const SepName = "__jev_sep"

// Plan is the output of Build.
type Plan struct {
	A           *parse.Analysis
	CollectSQL  string
	Sources     []SourcePlan
	SourceIndex map[string]int // Source.Key -> index in Sources
	Layout      []TargetLayout // parallel to A.Targets
	ServerOrder bool           // ORDER BY / LIMIT / OFFSET were pushed to Postgres
	Threshold   float64
}

// Build constructs the collect query.
func Build(ctx context.Context, a *parse.Analysis, cat Catalog, opts Options) (*Plan, error) {
	p := &Plan{A: a, SourceIndex: map[string]int{}}
	sel := proto.Clone(a.Select).(*pg_query.SelectStmt)
	var targets []*pg_query.Node
	col := 0

	// 1. Row-object columns come first so user columns are at a stable offset.
	for _, c := range a.Calls {
		key := c.Source.Key()
		if _, ok := p.SourceIndex[key]; ok {
			continue
		}
		sp := SourcePlan{Key: key, Source: c.Source}
		k := len(p.Sources)
		if c.Source.IsAlias {
			names, err := aliasColumns(ctx, a, c, cat, opts)
			if err != nil {
				return nil, err
			}
			for j, name := range names {
				alias := fmt.Sprintf("__jev_s%d_%d", k, j)
				ref := pg_query.MakeColumnRefNode([]*pg_query.Node{pg_query.MakeStrNode(c.Source.Alias), pg_query.MakeStrNode(name)}, -1)
				targets = append(targets, pg_query.MakeResTargetNodeWithNameAndVal(alias, ref, -1))
				sp.Fields = append(sp.Fields, SourceField{Name: name, Column: col})
				col++
			}
		} else {
			for j, expr := range c.Source.Columns {
				alias := fmt.Sprintf("__jev_s%d_%d", k, j)
				targets = append(targets, pg_query.MakeResTargetNodeWithNameAndVal(alias, proto.Clone(expr).(*pg_query.Node), -1))
				sp.Fields = append(sp.Fields, SourceField{Name: c.Source.Names[j], Column: col})
				col++
			}
		}
		p.SourceIndex[key] = k
		p.Sources = append(p.Sources, sp)
	}

	// 2. User targets (and hidden ones) in order.
	p.Layout = make([]TargetLayout, len(a.Targets))
	for i, t := range a.Targets {
		lay := TargetLayout{Start: -1}
		switch t.Kind {
		case parse.TargetPlain:
			val := proto.Clone(t.Node).(*pg_query.Node)
			if t.Star {
				targets = append(targets, pg_query.MakeResTargetNodeWithVal(val, -1))
				sep := pg_query.MakeResTargetNodeWithNameAndVal(SepName, nullInt(), -1)
				targets = append(targets, sep)
				lay = TargetLayout{Start: col, Count: -1, Sep: true}
				col++ // separator; star columns are counted at runtime
			} else {
				name := t.Name
				if name == "" && t.Hidden {
					name = fmt.Sprintf("__jev_t%d", i)
				}
				if name != "" {
					targets = append(targets, pg_query.MakeResTargetNodeWithNameAndVal(name, val, -1))
				} else {
					targets = append(targets, pg_query.MakeResTargetNodeWithVal(val, -1))
				}
				lay = TargetLayout{Start: col, Count: 1}
				col++
			}
		case parse.TargetAgg:
			if !t.Agg.Star {
				val := proto.Clone(t.Agg.Arg).(*pg_query.Node)
				targets = append(targets, pg_query.MakeResTargetNodeWithNameAndVal(fmt.Sprintf("__jev_agg%d", i), val, -1))
				lay = TargetLayout{Start: col, Count: 1}
				col++
			}
		case parse.TargetJev:
			// computed in Go
		}
		p.Layout[i] = lay
	}

	sel.TargetList = targets
	sel.WhereClause = a.Where
	sel.GroupClause = nil
	sel.GroupDistinct = false
	sel.HavingClause = nil
	sel.DistinctClause = nil
	sel.SortClause = nil
	sel.LimitCount = nil
	sel.LimitOffset = nil
	sel.LimitOption = pg_query.LimitOption_LIMIT_OPTION_DEFAULT

	// 3. ORDER BY / LIMIT can stay on the server only when the collected row
	// set is exactly the final row set: no jev filter, no grouping, no jev sort.
	if !a.Grouped && len(a.Predicates) == 0 && !a.OrderHasJev() {
		p.ServerOrder = true
		for _, k := range a.OrderKeys {
			t := a.Targets[k.Target]
			node := proto.Clone(t.Node).(*pg_query.Node)
			dir := pg_query.SortByDir_SORTBY_ASC
			if k.Desc {
				dir = pg_query.SortByDir_SORTBY_DESC
			}
			// Only spell out NULLS FIRST/LAST when it differs from the default.
			nulls := pg_query.SortByNulls_SORTBY_NULLS_DEFAULT
			if k.NullsFirst != k.Desc {
				nulls = pg_query.SortByNulls_SORTBY_NULLS_LAST
				if k.NullsFirst {
					nulls = pg_query.SortByNulls_SORTBY_NULLS_FIRST
				}
			}
			sel.SortClause = append(sel.SortClause, pg_query.MakeSortByNode(node, dir, nulls, -1))
		}
		if a.Limit != nil {
			sel.LimitCount = pg_query.MakeAConstIntNode(*a.Limit, -1)
			sel.LimitOption = pg_query.LimitOption_LIMIT_OPTION_COUNT
		}
		if a.Offset != nil {
			sel.LimitOffset = pg_query.MakeAConstIntNode(*a.Offset, -1)
			if sel.LimitCount == nil {
				sel.LimitOption = pg_query.LimitOption_LIMIT_OPTION_COUNT
			}
		}
	}

	sql, err := parse.DeparseStmt(&pg_query.Node{Node: &pg_query.Node_SelectStmt{SelectStmt: sel}})
	if err != nil {
		return nil, fmt.Errorf("deparse collect query: %w", err)
	}
	p.CollectSQL = sql
	return p, nil
}

func nullInt() *pg_query.Node {
	null := &pg_query.Node{Node: &pg_query.Node_AConst{AConst: &pg_query.A_Const{Isnull: true, Location: -1}}}
	return &pg_query.Node{Node: &pg_query.Node_TypeCast{TypeCast: &pg_query.TypeCast{
		Arg:      null,
		TypeName: &pg_query.TypeName{Names: []*pg_query.Node{pg_query.MakeStrNode("pg_catalog"), pg_query.MakeStrNode("int4")}, Typemod: -1, Location: -1},
		Location: -1,
	}}}
}

// aliasColumns decides which columns of a FROM alias go into the row object.
func aliasColumns(ctx context.Context, a *parse.Analysis, c *parse.Call, cat Catalog, opts Options) ([]string, error) {
	if len(opts.Columns) > 0 {
		return opts.Columns, nil
	}
	var tbl parse.FromTable
	found := false
	for _, t := range a.Tables {
		if t.Alias == c.Source.Alias {
			tbl, found = t, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%s: unknown FROM alias %q", c.Text, c.Source.Alias)
	}
	if cat == nil {
		return nil, fmt.Errorf("%s: no catalog available to expand alias %q", c.Text, c.Source.Alias)
	}
	cols, err := cat.Columns(ctx, tbl.Schema, tbl.Rel)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Text, err)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("%s: relation %q has no columns or does not exist", c.Text, tbl.Rel)
	}
	var names, blobs []string
	for _, col := range cols {
		if isBlob(col.Type) {
			blobs = append(blobs, col.Name+" "+col.Type)
			continue
		}
		names = append(names, col.Name)
	}
	if len(blobs) > 0 {
		return nil, fmt.Errorf("%s: %s has binary columns (%s) that would be sent to TypeSafe; use the column-list form jev((%s), ...) or --columns", c.Text, tbl.Rel, strings.Join(blobs, ", "), strings.Join(firstN(names, 3), ", "))
	}
	return names, nil
}

func isBlob(typ string) bool {
	t := strings.ToLower(typ)
	return t == "bytea" || strings.HasPrefix(t, "bytea[") || t == "bytea[]" || t == "pg_largeobject" || t == "oid" && false
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}
