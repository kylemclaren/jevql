// Package mcp exposes the jevql engine to agents over the Model Context
// Protocol: semantic SQL queries, cost estimates, row judgements and the
// catalog, over stdio or streamable HTTP.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/parse"
	"github.com/kylemclaren/jevql/internal/wire"
)

// Options configure the server.
type Options struct {
	Version string
	// AllowWrites lifts the default read-only rule (only SELECT statements).
	AllowWrites bool
	// MaxRows caps the collected row set per query when > 0 (overrides the executor default).
	MaxRows int
}

// ReadOnly reports whether non-SELECT statements are rejected.
func (o Options) ReadOnly() bool { return !o.AllowWrites }

type server struct {
	ex   *exec.Executor
	opts Options
	mu   sync.Mutex // the executor's DB may be a single pgx connection
}

// New builds an MCP server over the executor.
func New(ex *exec.Executor, o Options) *mcp.Server {
	if o.Version == "" {
		o.Version = "dev"
	}
	s := &server{ex: ex, opts: o}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:        "jevql",
		Title:       "jevql",
		Version:     o.Version,
		Description: "Semantic SQL for vanilla Postgres: run SELECTs that use jev() judgements, estimate their cost, judge rows directly, and browse the catalog.",
		WebsiteURL:  "https://jevql.fly.dev",
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "query",
		Title: "Run a jevql query",
		Description: `Run a SQL SELECT against the connected Postgres. Plain SQL works unchanged. jevql adds functions that judge each row with the Jev model:
- jev(alias, 'condition' [, threshold]) → boolean; use it as an AND term in WHERE (NOT jev(...) also works)
- jev_prob(alias, 'condition') → probability 0..1
- jev_choice(alias, 'question', ARRAY['a','b']) → the chosen option (works with GROUP BY and count(*))
- jev_score(alias, 'question', ARRAY['low','mid','high']) → weighted level index
The first argument is a FROM alias (all its columns are sent) or a column list: jev((name, bio), 'condition').
Example: SELECT name, jev_prob(people, 'could work from home') AS p FROM people WHERE jev(people, 'could work from home') AND country = 'PT' ORDER BY p DESC LIMIT 10
Cost: every row that survives the ordinary SQL filters is sent to TypeSafe and judged (cached afterwards), so put cheap predicates in SQL first and call the explain tool before a large query. Returns columns, rows and a stats object with tokens and USD.`,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: o.ReadOnly(), OpenWorldHint: ptr(true)},
	}, s.query)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "explain",
		Title:       "Estimate a query before running it",
		Description: `Plan a jevql query without calling TypeSafe: returns the SQL Postgres will actually run (jev terms stripped), how many rows survive the SQL filters, the number of batches, and an estimate of input tokens and USD. Call this before any query that might touch many rows, then narrow the WHERE clause or add a LIMIT if the estimate is high.`,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.explain)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "judge",
		Title:       "Judge rows you already have",
		Description: `Ask one question about each object in rows, with no database involved. kind is noul (yes/no, default), choice (pick one of options) or score (weighted position across ordered options). Answers come back in input order with a probability, a pass flag against the threshold, or the chosen option, plus a stats object. Identical rows are judged once and answers are cached.`,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, s.judge)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_tables",
		Title:       "List tables and views",
		Description: "List the tables, views and materialized views visible on the connected database (schema, name, kind). Use it to discover what you can query.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, s.listTables)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "describe_table",
		Title:       "Describe a table",
		Description: "Columns (name, type, nullable, default) and indexes of one table or view, like psql's \\d. Pass the name, optionally schema-qualified.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, s.describeTable)

	srv.AddResource(&mcp.Resource{
		URI:         "jevql://sql-surface",
		Name:        "jevql SQL surface",
		Title:       "jevql SQL surface",
		Description: "Reference for the jev functions, where they may appear in a statement, and what is rejected.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: sqlSurface}}}, nil
	})

	srv.AddPrompt(&mcp.Prompt{
		Name:        "semantic-query",
		Title:       "Write a jevql query",
		Description: "Turn a natural-language request into a jevql SELECT that judges rows with jev(), keeping cheap filters in SQL.",
		Arguments: []*mcp.PromptArgument{
			{Name: "request", Description: "What the user wants to find, in plain language", Required: true},
			{Name: "table", Description: "Table or tables to query, if known"},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		ask := req.Params.Arguments["request"]
		table := req.Params.Arguments["table"]
		if table == "" {
			table = "(call list_tables and describe_table first to pick one)"
		}
		text := fmt.Sprintf(`Write a single jevql SELECT for this request:

%s

Table(s): %s

Rules:
1. Put every ordinary condition (dates, status, country, ids) in SQL first; they run on Postgres for free.
2. Use jev(alias, 'condition') as an AND term in WHERE for yes/no filtering; jev_prob(...) in the SELECT list when a score is useful; jev_choice(...) with ARRAY[...] for classification; jev_score(...) for ordered levels.
3. Phrase the condition as a statement about one row, e.g. 'the customer is asking for a refund'.
4. Send fewer columns when the table is wide: jev((subject, body), '...').
5. Call the explain tool with the query and report the estimated rows and cost before running it if the estimate exceeds a few hundred rows.
Return only the SQL and one sentence on what it judges.`, ask, table)
		return &mcp.GetPromptResult{
			Description: "Draft a jevql query",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})

	return srv
}

// RunStdio serves one session over stdin/stdout until ctx ends.
func RunStdio(ctx context.Context, srv *mcp.Server) error {
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// Handler serves the streamable HTTP transport (mount at /mcp).
func Handler(srv *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true})
}

func ptr[T any](v T) *T { return &v }

// ---- tools -----------------------------------------------------------------

// QueryInput is the argument of the query tool.
type QueryInput struct {
	SQL       string   `json:"sql" jsonschema:"the SELECT to run; may use jev(), jev_prob(), jev_choice(), jev_score()"`
	Threshold *float64 `json:"threshold,omitempty" jsonschema:"probability cutoff for jev() when the call gives none (default 0.5)"`
	MaxRows   *int     `json:"max_rows,omitempty" jsonschema:"abort before any TypeSafe call if more rows than this survive the SQL filters"`
}

// ExplainInput is the argument of the explain tool.
type ExplainInput struct {
	SQL string `json:"sql" jsonschema:"the jevql SELECT to estimate"`
}

// JudgeInput is the argument of the judge tool.
type JudgeInput struct {
	Question  string           `json:"question" jsonschema:"the condition or question, phrased about one row, e.g. 'is asking for a refund'"`
	Kind      string           `json:"kind,omitempty" jsonschema:"noul (yes/no, default), choice or score"`
	Options   []string         `json:"options,omitempty" jsonschema:"choice options or ordered score levels"`
	Threshold *float64         `json:"threshold,omitempty" jsonschema:"noul pass cutoff, default 0.5"`
	Rows      []map[string]any `json:"rows" jsonschema:"the objects to judge; every field is sent to TypeSafe"`
	Raw       bool             `json:"raw,omitempty" jsonschema:"include the raw TypeSafe answer per row"`
}

// DescribeInput is the argument of describe_table.
type DescribeInput struct {
	Name string `json:"name" jsonschema:"table or view name, optionally schema-qualified"`
}

// TableInfo is one row of list_tables.
type TableInfo struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

// TableList is the output of list_tables.
type TableList struct {
	Tables []TableInfo `json:"tables"`
}

// ColumnInfo is one column of describe_table.
type ColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
}

// TableDescription is the output of describe_table.
type TableDescription struct {
	Name    string       `json:"name"`
	Kind    string       `json:"kind"`
	Columns []ColumnInfo `json:"columns"`
	Indexes []string     `json:"indexes"`
}

func toolErr(err error) (*mcp.CallToolResult, error) {
	eb, _ := wire.ErrorFor(err)
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: eb.Code + ": " + eb.Error}},
	}, nil
}

func (s *server) checkReadOnly(sql string) error {
	if !s.opts.ReadOnly() {
		return nil
	}
	st, err := parse.ParseOne(sql)
	if err != nil {
		return err
	}
	if st.Node.GetSelectStmt() == nil {
		return fmt.Errorf("only SELECT statements are allowed (server is read-only; start with --allow-writes to change that)")
	}
	return nil
}

// executor returns a per-call shallow copy with option overrides applied.
func (s *server) executor(explain bool, thr *float64, maxRows *int) *exec.Executor {
	ex := *s.ex
	ex.Opts.Explain = explain
	if s.opts.MaxRows > 0 {
		ex.Opts.MaxRows = s.opts.MaxRows
	}
	if thr != nil {
		ex.Opts.Threshold = *thr
	}
	if maxRows != nil {
		ex.Opts.MaxRows = *maxRows
	}
	return &ex
}

func (s *server) query(ctx context.Context, _ *mcp.CallToolRequest, in QueryInput) (*mcp.CallToolResult, *wire.QueryResult, error) {
	if strings.TrimSpace(in.SQL) == "" {
		return toolErrOut[*wire.QueryResult](fmt.Errorf("sql is required"))
	}
	if err := s.checkReadOnly(in.SQL); err != nil {
		return toolErrOut[*wire.QueryResult](err)
	}
	s.mu.Lock()
	res, err := s.executor(false, in.Threshold, in.MaxRows).Run(ctx, in.SQL)
	s.mu.Unlock()
	if err != nil {
		return toolErrOut[*wire.QueryResult](err)
	}
	out := wire.FromResult(res)
	return nil, out, nil
}

func (s *server) explain(ctx context.Context, _ *mcp.CallToolRequest, in ExplainInput) (*mcp.CallToolResult, *wire.Explain, error) {
	if strings.TrimSpace(in.SQL) == "" {
		return toolErrOut[*wire.Explain](fmt.Errorf("sql is required"))
	}
	if err := s.checkReadOnly(in.SQL); err != nil {
		return toolErrOut[*wire.Explain](err)
	}
	s.mu.Lock()
	res, err := s.executor(true, nil, nil).Run(ctx, in.SQL)
	s.mu.Unlock()
	if err != nil {
		return toolErrOut[*wire.Explain](err)
	}
	doc := wire.FromResult(res)
	if doc.Explain == nil {
		return toolErrOut[*wire.Explain](fmt.Errorf("the statement has no jev_* calls; it runs entirely on Postgres and costs nothing to judge"))
	}
	return nil, doc.Explain, nil
}

func (s *server) judge(ctx context.Context, _ *mcp.CallToolRequest, in JudgeInput) (*mcp.CallToolResult, *exec.JudgeResult, error) {
	s.mu.Lock()
	res, err := s.executor(false, nil, nil).JudgeRows(ctx, exec.JudgeRequest{
		Question: in.Question, Kind: in.Kind, Options: in.Options, Threshold: in.Threshold, Rows: in.Rows, Raw: in.Raw,
	})
	s.mu.Unlock()
	if err != nil {
		return toolErrOut[*exec.JudgeResult](err)
	}
	return nil, res, nil
}

const listTablesSQL = `SELECT n.nspname, c.relname,
  CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'partitioned table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' WHEN 'f' THEN 'foreign table' END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','p','v','m','f') AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
ORDER BY 1, 2`

func (s *server) listTables(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, *TableList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.ex.DB.Query(ctx, listTablesSQL)
	if err != nil {
		return toolErrOut[*TableList](err)
	}
	defer rows.Close()
	out := &TableList{Tables: []TableInfo{}}
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Schema, &t.Name, &t.Kind); err != nil {
			return toolErrOut[*TableList](err)
		}
		out.Tables = append(out.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return toolErrOut[*TableList](err)
	}
	return nil, out, nil
}

func sqlLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (s *server) describeTable(ctx context.Context, _ *mcp.CallToolRequest, in DescribeInput) (*mcp.CallToolResult, *TableDescription, error) {
	if strings.TrimSpace(in.Name) == "" {
		return toolErrOut[*TableDescription](fmt.Errorf("name is required"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lit := sqlLiteral(in.Name)
	rows, err := s.ex.DB.Query(ctx, `SELECT c.oid::text, n.nspname || '.' || c.relname,
  CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'partitioned table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' WHEN 'f' THEN 'foreign table' ELSE c.relkind::text END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = to_regclass(`+lit+`)`)
	if err != nil {
		return toolErrOut[*TableDescription](err)
	}
	var oid, rel, kind string
	found := false
	for rows.Next() {
		if err := rows.Scan(&oid, &rel, &kind); err != nil {
			rows.Close()
			return toolErrOut[*TableDescription](err)
		}
		found = true
	}
	rows.Close()
	if !found {
		return toolErrOut[*TableDescription](fmt.Errorf("relation %q does not exist", in.Name))
	}
	out := &TableDescription{Name: rel, Kind: kind, Columns: []ColumnInfo{}, Indexes: []string{}}
	rows, err = s.ex.DB.Query(ctx, `SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
  COALESCE(pg_get_expr(d.adbin, d.adrelid), '')
FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attrelid = `+oid+` AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`)
	if err != nil {
		return toolErrOut[*TableDescription](err)
	}
	for rows.Next() {
		var c ColumnInfo
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default); err != nil {
			rows.Close()
			return toolErrOut[*TableDescription](err)
		}
		out.Columns = append(out.Columns, c)
	}
	rows.Close()
	rows, err = s.ex.DB.Query(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname || '.' || tablename = `+sqlLiteral(rel)+` ORDER BY indexname`)
	if err != nil {
		return toolErrOut[*TableDescription](err)
	}
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			rows.Close()
			return toolErrOut[*TableDescription](err)
		}
		out.Indexes = append(out.Indexes, def)
	}
	rows.Close()
	return nil, out, nil
}

// toolErrOut adapts toolErr to the typed handler signature.
func toolErrOut[Out any](err error) (*mcp.CallToolResult, Out, error) {
	var zero Out
	r, _ := toolErr(err)
	return r, zero, nil
}

var _ = json.Marshal

const sqlSurface = `# jevql SQL surface

All functions take a **row source** first and a **question** second.

Row sources: ` + "`jev(people, ...)`" + ` (every column of the FROM alias), ` + "`jev(p, ...)`" + ` for ` + "`FROM people p`" + `, or a column list ` + "`jev((name, bio), ...)`" + ` to send less. NULLs are sent as null; timestamps as RFC 3339.

| Call | Returns |
|---|---|
| jev(src, 'condition') | boolean, p >= threshold (default 0.5) |
| jev(src, 'condition', 0.7) | boolean with an explicit threshold |
| jev_prob(src, 'condition') | float8 0..1 |
| jev_choice(src, 'question', ARRAY['a','b']) | text, the chosen option |
| jev_score(src, 'question', ARRAY['lo','mid','hi']) | float8, weighted level index |
| jev_score_norm(src, 'question', ARRAY[...]) | float8 0..1 |
| jev_confidence(src, 'q' [, 'noul'|'choice'|'score', ARRAY[...]]) | float8 |
| jev_eval(src, 'q' [, kind, ARRAY[...]]) | raw answer JSON |

Where a call may appear:
- WHERE: jev() as an AND term, including NOT jev().
- SELECT list: any function as the whole expression, with an alias.
- ORDER BY: by alias, position, or the expression.
- GROUP BY: with count(*), count(x), sum, avg, min, max (computed client-side; sum/avg need numeric columns).
- Joins: judge one or several aliases.

Rejected on purpose: jev_* in INSERT/UPDATE/DELETE/DDL, CTEs or subqueries, HAVING, window functions, OR jev(...), comparisons like jev_prob(...) > 0.7 (use jev(src, 'condition', 0.7)), wrapping like round(jev_prob(...)), SELECT DISTINCT, UNION, tables with bytea columns unless columns are narrowed.

Cost: every row that survives the SQL filters is sent to TypeSafe and judged; identical rows are judged once and answers are cached. ORDER BY/LIMIT are pushed to Postgres only when no jev predicate, grouping or jev sort key is present. Use the explain tool to see rows, batches and USD before running.
`
