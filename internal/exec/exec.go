// Package exec runs statements: plain ones go straight to Postgres, jev
// ones take the two-pass route (collect in SQL, judge over HTTP, project
// in Go).
package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kylemclaren/jevpsql/internal/cache"
	"github.com/kylemclaren/jevpsql/internal/canon"
	"github.com/kylemclaren/jevpsql/internal/parse"
	"github.com/kylemclaren/jevpsql/internal/psqlout"
	"github.com/kylemclaren/jevpsql/internal/rewrite"
	"github.com/kylemclaren/jevpsql/internal/stats"
	"github.com/kylemclaren/jevpsql/internal/typesafe"
)

// Options tune a jev execution.
type Options struct {
	Threshold   float64
	BatchSize   int
	Concurrency int
	MaxRows     int
	MaxChars    int
	Columns     []string
	Explain     bool
	Model       string
}

// BudgetError means a guard (--max-rows / --max-chars) stopped the query
// before any HTTP call. It maps to exit code 2.
type BudgetError struct{ Msg string }

func (e *BudgetError) Error() string { return e.Msg }

// Querier is the subset of pgx.Conn we need.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Executor runs statements.
type Executor struct {
	DB       Querier
	TS       *typesafe.Client
	Cache    *cache.Store // nil disables caching
	Opts     Options
	Progress func(typesafe.Progress)
	Log      func(format string, args ...any) // verbose notices; may be nil
}

// Explain is the --explain report.
type Explain struct {
	CollectSQL   string
	Rows         int64
	Sources      int
	Questions    int
	Judgements   int
	Batches      int
	Tokens       int
	USD          float64
	ServerOrder  bool
	AvgRowChars  float64
	QuestionList []string
}

// Result of one statement.
type Result struct {
	Table   *psqlout.Table // nil when the statement returned no rows
	Tag     string         // command tag for row-less statements
	Stats   *stats.Stats   // set for jev statements
	Explain *Explain       // set in --explain mode
	Jev     bool
}

func (e *Executor) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log(format, args...)
	}
}

// Run executes one statement.
func (e *Executor) Run(ctx context.Context, sql string) (*Result, error) {
	st, err := parse.ParseOne(sql)
	if err != nil {
		return nil, err
	}
	if !st.HasJev {
		return e.passthrough(ctx, sql)
	}
	a, err := parse.Analyze(st)
	if err != nil {
		return nil, err
	}
	plan, err := rewrite.Build(ctx, a, &pgCatalog{e.DB}, rewrite.Options{Columns: e.Opts.Columns})
	if err != nil {
		return nil, err
	}
	if e.Opts.Explain {
		return e.explain(ctx, plan)
	}
	return e.runJev(ctx, plan)
}

func (e *Executor) passthrough(ctx context.Context, sql string) (*Result, error) {
	rows, err := e.DB.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fds := rows.FieldDescriptions()
	var t *psqlout.Table
	if len(fds) > 0 {
		t = &psqlout.Table{}
		for _, fd := range fds {
			t.Columns = append(t.Columns, fd.Name)
		}
	}
	for rows.Next() {
		raw := rows.RawValues()
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		cells := make([]psqlout.Cell, len(fds))
		for i, fd := range fds {
			cells[i] = cellFromRaw(fd, raw[i], vals[i])
		}
		t.Rows = append(t.Rows, cells)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res := &Result{Table: t}
	if t == nil {
		res.Tag = rows.CommandTag().String()
	}
	return res, nil
}

// crow is one collected row.
type crow struct {
	cells []psqlout.Cell
	vals  []any
	objs  [][]byte // canonical row object per source
}

type collected struct {
	fds  []pgconn.FieldDescription
	rows []*crow
}

func (e *Executor) collect(ctx context.Context, plan *rewrite.Plan) (*collected, error) {
	rows, err := e.DB.Query(ctx, plan.CollectSQL)
	if err != nil {
		return nil, fmt.Errorf("collect query failed: %w\n  SQL: %s", err, plan.CollectSQL)
	}
	defer rows.Close()
	c := &collected{fds: rows.FieldDescriptions()}
	max := e.Opts.MaxRows
	for rows.Next() {
		if max > 0 && len(c.rows) >= max {
			return nil, &BudgetError{Msg: fmt.Sprintf("collect returned more than --max-rows (%d) rows; add SQL filters or raise --max-rows", max)}
		}
		raw := rows.RawValues()
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		r := &crow{cells: make([]psqlout.Cell, len(c.fds)), vals: vals}
		for i, fd := range c.fds {
			r.cells[i] = cellFromRaw(fd, raw[i], vals[i])
		}
		c.rows = append(c.rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Build row objects.
	totalChars := 0
	for _, r := range c.rows {
		r.objs = make([][]byte, len(plan.Sources))
		for si, sp := range plan.Sources {
			obj := map[string]any{}
			for _, f := range sp.Fields {
				obj[f.Name] = canon.Value(r.vals[f.Column])
			}
			b, err := canon.Marshal(obj)
			if err != nil {
				return nil, fmt.Errorf("row object: %w", err)
			}
			r.objs[si] = b
			totalChars += len(b)
		}
	}
	if e.Opts.MaxChars > 0 && totalChars > e.Opts.MaxChars {
		return nil, &BudgetError{Msg: fmt.Sprintf("row objects total %d chars, more than --max-chars (%d); narrow the columns with jev((col, ...)) or --columns", totalChars, e.Opts.MaxChars)}
	}
	return c, nil
}

func question(c *parse.Call) typesafe.Question {
	return typesafe.Question{Kind: typesafe.Kind(c.Kind), Text: c.Question, Options: c.Options}
}

// judge resolves every (row, call) pair to an answer via cache or HTTP.
func (e *Executor) judge(ctx context.Context, plan *rewrite.Plan, c *collected, st *stats.Stats) (map[*crow]map[*parse.Call]*typesafe.Answer, error) {
	a := plan.A
	items := map[string]*typesafe.Item{}
	var order []*typesafe.Item
	lookup := make(map[*crow]map[*parse.Call]*typesafe.Item, len(c.rows))
	for _, r := range c.rows {
		lookup[r] = map[*parse.Call]*typesafe.Item{}
		for _, call := range a.Calls {
			src := plan.SourceIndex[call.Source.Key()]
			q := question(call)
			k := q.Key() + "\x00" + string(r.objs[src])
			it, ok := items[k]
			if !ok {
				it = &typesafe.Item{Q: q, Row: r.objs[src]}
				items[k] = it
				order = append(order, it)
			}
			lookup[r][call] = it
		}
	}
	st.Judged = len(order)

	// Cache lookup.
	keys := make([]string, len(order))
	for i, it := range order {
		keys[i] = cache.Key(e.TS.Model, string(it.Q.Kind), it.Q.Text, it.Q.Options, it.Row)
	}
	hits, err := e.Cache.GetMany(ctx, keys)
	if err != nil {
		e.logf("cache read failed: %v", err)
		hits = nil
	}
	var pending []*typesafe.Item
	for i, it := range order {
		if raw, ok := hits[keys[i]]; ok {
			ans, err := typesafe.ParseAnswer(raw)
			if err == nil && ans.Type == string(it.Q.Kind) {
				it.Answer = ans
				st.CacheHits++
				continue
			}
		}
		pending = append(pending, it)
	}
	e.logf("judge: %d distinct judgements, %d cache hits, %d to send in %d requests", len(order), st.CacheHits, len(pending), e.TS.BatchCount(pending))
	if len(pending) > 0 {
		usage, err := e.TS.Judge(ctx, pending, e.Progress)
		st.Requests += usage.Requests
		st.InputTokens += usage.InputTokens
		st.OutputTokens += usage.OutputTokens
		st.USD += usage.USD()
		if err != nil {
			return nil, err
		}
		put := map[string]json.RawMessage{}
		for i, it := range order {
			if it.Answer != nil && it.Answer.Raw != nil {
				if _, hit := hits[keys[i]]; !hit {
					put[keys[i]] = it.Answer.Raw
				}
			}
		}
		if err := e.Cache.PutMany(ctx, put); err != nil {
			e.logf("cache write failed: %v", err)
		}
	}
	out := make(map[*crow]map[*parse.Call]*typesafe.Answer, len(c.rows))
	for r, m := range lookup {
		out[r] = map[*parse.Call]*typesafe.Answer{}
		for call, it := range m {
			if it.Answer == nil {
				return nil, errors.New("typesafe: missing answer after judge")
			}
			out[r][call] = it.Answer
		}
	}
	return out, nil
}

func (e *Executor) threshold(c *parse.Call) float64 {
	if c.Threshold != nil {
		return *c.Threshold
	}
	return e.Opts.Threshold
}

// jevValue computes the output of a jev_* call from its answer.
func (e *Executor) jevValue(c *parse.Call, ans *typesafe.Answer) psqlout.Cell {
	switch c.Fn {
	case "jev":
		return boolCell(ans.P() >= e.threshold(c))
	case "jev_prob":
		return floatCell(ans.P())
	case "jev_choice":
		return textCell(ans.Choice)
	case "jev_score":
		return floatCell(ans.Score)
	case "jev_score_norm":
		n := float64(len(c.Options) - 1)
		if n <= 0 {
			return floatCell(0)
		}
		return floatCell(ans.Score / n)
	case "jev_confidence":
		return floatCell(ans.ConfidenceValue())
	case "jev_eval":
		return jsonCell(ans.Raw)
	}
	return psqlout.Cell{Null: true}
}

func (e *Executor) runJev(ctx context.Context, plan *rewrite.Plan) (*Result, error) {
	start := time.Now()
	st := &stats.Stats{}
	e.logf("collect: %s", plan.CollectSQL)
	c, err := e.collect(ctx, plan)
	if err != nil {
		return nil, err
	}
	st.CollectRows = len(c.rows)
	e.logf("collect: %d rows, %d source(s), %d call(s)", len(c.rows), len(plan.Sources), len(plan.A.Calls))
	answers, err := e.judge(ctx, plan, c, st)
	if err != nil {
		st.Elapsed = time.Since(start)
		return &Result{Stats: st, Jev: true}, err
	}
	table, err := e.project(plan, c, answers)
	if err != nil {
		return nil, err
	}
	st.Elapsed = time.Since(start)
	return &Result{Table: table, Stats: st, Jev: true}, nil
}

// span returns the collect columns a target occupies in this result.
func span(plan *rewrite.Plan, fds []pgconn.FieldDescription, ti int) (int, int) {
	lay := plan.Layout[ti]
	if lay.Start < 0 {
		return -1, 0
	}
	if lay.Count >= 0 {
		return lay.Start, lay.Count
	}
	// star: read until the separator column
	n := 0
	for i := lay.Start; i < len(fds); i++ {
		if fds[i].Name == rewrite.SepName {
			break
		}
		n++
	}
	return lay.Start, n
}

// outRow is a row after judging, before sorting.
type outRow struct {
	cells []psqlout.Cell
	keys  []any // sort keys, parallel to OrderKeys (nil = NULL)
}

func (e *Executor) project(plan *rewrite.Plan, c *collected, answers map[*crow]map[*parse.Call]*typesafe.Answer) (*psqlout.Table, error) {
	a := plan.A
	// Output column names.
	t := &psqlout.Table{}
	for ti, tg := range a.Targets {
		if tg.Hidden {
			continue
		}
		switch tg.Kind {
		case parse.TargetPlain:
			s, n := span(plan, c.fds, ti)
			for i := 0; i < n; i++ {
				t.Columns = append(t.Columns, c.fds[s+i].Name)
			}
		default:
			t.Columns = append(t.Columns, tg.OutputName())
		}
	}

	// Filter.
	var kept []*crow
	for _, r := range c.rows {
		ok := true
		for _, p := range a.Predicates {
			ans := answers[r][p.Call]
			pass := ans.P() >= e.threshold(p.Call)
			if p.Negate {
				pass = !pass
			}
			if !pass {
				ok = false
				break
			}
		}
		if ok {
			kept = append(kept, r)
		}
	}

	var rows []outRow
	if a.Grouped {
		var err error
		rows, err = e.aggregate(plan, c, kept, answers)
		if err != nil {
			return nil, err
		}
	} else {
		for _, r := range kept {
			o := outRow{}
			// values per target for sort keys
			tvals := make([]any, len(a.Targets))
			for ti, tg := range a.Targets {
				var cells []psqlout.Cell
				switch tg.Kind {
				case parse.TargetPlain:
					s, n := span(plan, c.fds, ti)
					cells = r.cells[s : s+n]
					if n == 1 {
						tvals[ti] = r.vals[s]
					}
				case parse.TargetJev:
					cell := e.jevValue(tg.Call, answers[r][tg.Call])
					cells = []psqlout.Cell{cell}
					tvals[ti] = cell.Value
				}
				if !tg.Hidden {
					o.cells = append(o.cells, cells...)
				}
			}
			for _, k := range a.OrderKeys {
				o.keys = append(o.keys, tvals[k.Target])
			}
			rows = append(rows, o)
		}
	}

	if !plan.ServerOrder {
		sortRows(rows, a.OrderKeys)
		if a.Offset != nil {
			off := int(*a.Offset)
			if off > len(rows) {
				off = len(rows)
			}
			rows = rows[off:]
		}
		if a.Limit != nil && int(*a.Limit) < len(rows) {
			rows = rows[:*a.Limit]
		}
	}
	for _, r := range rows {
		t.Rows = append(t.Rows, r.cells)
	}
	return t, nil
}

func sortRows(rows []outRow, keys []parse.OrderKey) {
	if len(keys) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for ki, k := range keys {
			x, y := rows[i].keys[ki], rows[j].keys[ki]
			if x == nil && y == nil {
				continue
			}
			if x == nil {
				return k.NullsFirst
			}
			if y == nil {
				return !k.NullsFirst
			}
			cmp := compare(x, y)
			if cmp == 0 {
				continue
			}
			if k.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

// aggState accumulates one aggregate.
type aggState struct {
	count    int64
	sumF     float64
	sumI     int64
	allInt   bool
	minV     any
	minC     psqlout.Cell
	maxV     any
	maxC     psqlout.Cell
	distinct map[string]struct{}
}

type group struct {
	cells []psqlout.Cell // per non-hidden target position (filled at end)
	tcell map[int]psqlout.Cell
	tval  map[int]any
	aggs  map[int]*aggState
}

func (e *Executor) aggregate(plan *rewrite.Plan, c *collected, rows []*crow, answers map[*crow]map[*parse.Call]*typesafe.Answer) ([]outRow, error) {
	a := plan.A
	groups := map[string]*group{}
	var order []string
	for _, r := range rows {
		// Key values per target.
		tcell := map[int]psqlout.Cell{}
		tval := map[int]any{}
		for ti, tg := range a.Targets {
			switch tg.Kind {
			case parse.TargetPlain:
				s, _ := span(plan, c.fds, ti)
				tcell[ti] = r.cells[s]
				tval[ti] = r.vals[s]
			case parse.TargetJev:
				cell := e.jevValue(tg.Call, answers[r][tg.Call])
				tcell[ti] = cell
				tval[ti] = cell.Value
			}
		}
		keyParts := make([]any, len(a.GroupKeys))
		for i, gk := range a.GroupKeys {
			keyParts[i] = canon.Value(tval[gk])
		}
		kb, _ := canon.Marshal(map[string]any{"k": keyParts})
		key := string(kb)
		g, ok := groups[key]
		if !ok {
			g = &group{tcell: tcell, tval: tval, aggs: map[int]*aggState{}}
			for ti, tg := range a.Targets {
				if tg.Kind == parse.TargetAgg {
					g.aggs[ti] = &aggState{allInt: true}
					if tg.Agg.Distinct {
						g.aggs[ti].distinct = map[string]struct{}{}
					}
				}
			}
			groups[key] = g
			order = append(order, key)
		}
		for ti, tg := range a.Targets {
			if tg.Kind != parse.TargetAgg {
				continue
			}
			st := g.aggs[ti]
			if tg.Agg.Star {
				st.count++
				continue
			}
			col := plan.Layout[ti].Start
			v := r.vals[col]
			if v == nil {
				continue
			}
			if st.distinct != nil {
				vb, _ := canon.Marshal(map[string]any{"v": canon.Value(v)})
				if _, seen := st.distinct[string(vb)]; seen {
					continue
				}
				st.distinct[string(vb)] = struct{}{}
			}
			st.count++
			switch tg.Agg.Fn {
			case "sum", "avg":
				f, ok := toFloat(v)
				if !ok {
					return nil, fmt.Errorf("%s: GROUP BY with jev_* only supports COUNT and SUM/AVG of numeric columns in v1", tg.Text)
				}
				st.sumF += f
				if i, ok := toInt(v); ok && st.allInt {
					st.sumI += i
				} else {
					st.allInt = false
				}
			case "min", "max":
				if st.minV == nil || compare(v, st.minV) < 0 {
					st.minV, st.minC = v, r.cells[col]
				}
				if st.maxV == nil || compare(v, st.maxV) > 0 {
					st.maxV, st.maxC = v, r.cells[col]
				}
			}
		}
	}
	var out []outRow
	for _, key := range order {
		g := groups[key]
		o := outRow{}
		tvals := make([]any, len(a.Targets))
		for ti, tg := range a.Targets {
			var cell psqlout.Cell
			switch tg.Kind {
			case parse.TargetPlain, parse.TargetJev:
				cell = g.tcell[ti]
				tvals[ti] = g.tval[ti]
			case parse.TargetAgg:
				st := g.aggs[ti]
				switch tg.Agg.Fn {
				case "count":
					cell = intCell(st.count)
				case "sum":
					if st.count == 0 {
						cell = psqlout.Cell{Null: true}
					} else if st.allInt {
						cell = intCell(st.sumI)
					} else {
						cell = floatCell(st.sumF)
					}
				case "avg":
					if st.count == 0 {
						cell = psqlout.Cell{Null: true}
					} else {
						cell = floatCell(st.sumF / float64(st.count))
					}
				case "min":
					if st.minV == nil {
						cell = psqlout.Cell{Null: true}
					} else {
						cell = st.minC
					}
				case "max":
					if st.maxV == nil {
						cell = psqlout.Cell{Null: true}
					} else {
						cell = st.maxC
					}
				}
				tvals[ti] = cell.Value
			}
			if !tg.Hidden {
				o.cells = append(o.cells, cell)
			}
		}
		for _, k := range a.OrderKeys {
			o.keys = append(o.keys, tvals[k.Target])
		}
		out = append(out, o)
	}
	return out, nil
}

func (e *Executor) explain(ctx context.Context, plan *rewrite.Plan) (*Result, error) {
	a := plan.A
	ex := &Explain{CollectSQL: plan.CollectSQL, Sources: len(plan.Sources), ServerOrder: plan.ServerOrder}
	countSQL := "SELECT count(*) FROM (" + plan.CollectSQL + ") AS __jev_collect"
	rows, err := e.DB.Query(ctx, countSQL)
	if err != nil {
		return nil, fmt.Errorf("collect query failed: %w\n  SQL: %s", err, plan.CollectSQL)
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			rows.Close()
			return nil, err
		}
		if n, ok := toInt(vals[0]); ok {
			ex.Rows = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Sample a few rows to size the row objects.
	sample := &Executor{DB: e.DB, Opts: e.Opts}
	sample.Opts.MaxRows = 0
	sp := *plan
	sp.CollectSQL = "SELECT * FROM (" + plan.CollectSQL + ") AS __jev_collect LIMIT 25"
	sc, err := sample.collect(ctx, &sp)
	if err != nil {
		return nil, err
	}
	total, n := 0, 0
	for _, r := range sc.rows {
		for _, o := range r.objs {
			total += len(o)
			n++
		}
	}
	if n > 0 {
		ex.AvgRowChars = float64(total) / float64(n)
	}
	seen := map[string]bool{}
	for _, c := range a.Calls {
		k := c.QuestionKey()
		if !seen[k] {
			seen[k] = true
			ex.QuestionList = append(ex.QuestionList, fmt.Sprintf("%s %q on %s", c.Kind, c.Question, c.Source.String()))
		}
	}
	ex.Questions = len(ex.QuestionList)
	ex.Judgements = int(ex.Rows) * ex.Questions
	bs := e.Opts.BatchSize
	if bs <= 0 {
		bs = 40
	}
	ex.Batches = ex.Questions * int((ex.Rows+int64(bs)-1)/int64(bs))
	ex.Tokens = typesafe.EstimateTokens(ex.Judgements, ex.AvgRowChars, bs)
	ex.USD = float64(ex.Tokens) * 0.042 / 1e6
	return &Result{Explain: ex, Jev: true}, nil
}

// Render prints the explain report (without colour; the app may highlight SQL).
func (x *Explain) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "collect SQL:\n  %s\n\n", x.CollectSQL)
	fmt.Fprintf(&b, "rows after SQL filters: %d\n", x.Rows)
	fmt.Fprintf(&b, "row sources:            %d\n", x.Sources)
	fmt.Fprintf(&b, "questions:              %d\n", x.Questions)
	for _, q := range x.QuestionList {
		fmt.Fprintf(&b, "  - %s\n", q)
	}
	fmt.Fprintf(&b, "judgements (max):       %d\n", x.Judgements)
	fmt.Fprintf(&b, "batches:                %d\n", x.Batches)
	fmt.Fprintf(&b, "avg row object:         %.0f chars\n", x.AvgRowChars)
	fmt.Fprintf(&b, "est. input tokens:      %s\n", stats.Compact(x.Tokens))
	fmt.Fprintf(&b, "est. cost:              $%.4f\n", x.USD)
	if x.ServerOrder {
		b.WriteString("ORDER BY / LIMIT:       pushed to Postgres\n")
	} else {
		b.WriteString("ORDER BY / LIMIT:       applied in jevpsql after judging\n")
	}
	b.WriteString("(no TypeSafe requests were made; cache hits are not counted here)\n")
	return b.String()
}

// pgCatalog answers column lookups from pg_attribute.
type pgCatalog struct{ db Querier }

func (c *pgCatalog) Columns(ctx context.Context, schema, rel string) ([]rewrite.Column, error) {
	name := pgx.Identifier{rel}.Sanitize()
	if schema != "" {
		name = pgx.Identifier{schema, rel}.Sanitize()
	}
	rows, err := c.db.Query(ctx, `SELECT a.attname, format_type(a.atttypid, a.atttypmod)
FROM pg_attribute a WHERE a.attrelid = to_regclass($1) AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rewrite.Column
	for rows.Next() {
		var n, t string
		if err := rows.Scan(&n, &t); err != nil {
			return nil, err
		}
		out = append(out, rewrite.Column{Name: n, Type: t})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("relation %s does not exist", name)
	}
	return out, nil
}
