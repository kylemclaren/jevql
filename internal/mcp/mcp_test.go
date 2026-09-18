package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/parse"
	"github.com/kylemclaren/jevql/internal/typesafe"
	"github.com/kylemclaren/jevql/internal/typesafe/typesafetest"
)

func testExec(t *testing.T) (*exec.Executor, *int32) {
	t.Helper()
	url := os.Getenv("PGTEST_URL")
	if url == "" {
		t.Skip("PGTEST_URL not set")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	schema := "jevql_mcp_test"
	if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE; CREATE SCHEMA "+schema+"; SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "people.sql"))
	if err != nil {
		t.Fatal(err)
	}
	stmts, _ := parse.Split(string(data))
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); conn.Close(ctx) })
	srv, calls := typesafetest.Server(t)
	return &exec.Executor{DB: conn, TS: typesafe.New(srv.URL, "test-key", "jev-test", 4, 2),
		Opts: exec.Options{Threshold: 0.5, BatchSize: 4, Concurrency: 2, MaxRows: 2500, Model: "jev-test"}}, calls
}

// session connects an in-memory client to a server over the executor.
func session(t *testing.T, ex *exec.Executor, o Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := New(ex, o)
	st, ct := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return res, out
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestListToolsAndResource(t *testing.T) {
	// No database needed: only the catalog of tools, the resource and the prompt.
	cs := session(t, &exec.Executor{Opts: exec.Options{}}, Options{Version: "test"})
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"query": false, "explain": false, "judge": false, "list_tables": false, "describe_table": false}
	for _, tl := range tools.Tools {
		if _, ok := want[tl.Name]; ok {
			want[tl.Name] = true
		}
		if tl.Name == "query" && !strings.Contains(tl.Description, "jev_choice") {
			t.Errorf("query description should teach the jev functions")
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %s missing", name)
		}
	}
	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "jevql://sql-surface"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Contents) != 1 || !strings.Contains(rr.Contents[0].Text, "jev_score_norm") || rr.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("resource = %+v", rr.Contents)
	}
	pr, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "semantic-query", Arguments: map[string]string{"request": "refund requests", "table": "tickets"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Messages) != 1 || !strings.Contains(pr.Messages[0].Content.(*mcp.TextContent).Text, "refund requests") {
		t.Errorf("prompt = %+v", pr)
	}
}

func TestReadOnlyRejectsWrites(t *testing.T) {
	cs := session(t, &exec.Executor{Opts: exec.Options{}}, Options{})
	res, _ := call(t, cs, "query", map[string]any{"sql": "DELETE FROM people"})
	if !res.IsError || !strings.Contains(text(res), "read-only") {
		t.Errorf("expected read-only rejection, got %+v %q", res.IsError, text(res))
	}
	res, _ = call(t, cs, "query", map[string]any{"sql": "   "})
	if !res.IsError {
		t.Error("empty sql should be a tool error")
	}
}

func TestTools(t *testing.T) {
	ex, calls := testExec(t)
	cs := session(t, ex, Options{Version: "test"})

	res, out := call(t, cs, "query", map[string]any{"sql": "SELECT 1 AS one, 'x' AS t"})
	if res.IsError {
		t.Fatalf("select 1: %s", text(res))
	}
	if out["jev"] != false || out["row_count"] != float64(1) || out["columns"].([]any)[0] != "one" {
		t.Errorf("doc = %v", out)
	}

	res, out = call(t, cs, "query", map[string]any{"sql": "SELECT name FROM people WHERE jev(people, 'wfh') ORDER BY name"})
	if res.IsError {
		t.Fatalf("jev query: %s", text(res))
	}
	if out["jev"] != true || out["row_count"] != float64(7) {
		t.Errorf("jev doc = %v", out)
	}
	if st := out["stats"].(map[string]any); st["judged"] != float64(12) {
		t.Errorf("stats = %v", st)
	}

	// threshold override: the mock answers 0.9 or 0.1
	res, out = call(t, cs, "query", map[string]any{"sql": "SELECT name FROM people WHERE jev(people, 'wfh')", "threshold": 0.95})
	if res.IsError || out["row_count"] != float64(0) {
		t.Errorf("threshold: %v %v", res.IsError, out["row_count"])
	}

	before := *calls
	res, out = call(t, cs, "explain", map[string]any{"sql": "SELECT name FROM people WHERE jev(people, 'new q') AND country = 'PT'"})
	if res.IsError {
		t.Fatalf("explain: %s", text(res))
	}
	if out["rows"] != float64(6) || out["batches"] != float64(2) || *calls != before {
		t.Errorf("explain = %v calls=%d", out, *calls-before)
	}
	res, _ = call(t, cs, "explain", map[string]any{"sql": "SELECT 1"})
	if !res.IsError {
		t.Error("explain of plain SQL should be a tool error")
	}

	res, out = call(t, cs, "judge", map[string]any{
		"question": "wfh",
		"rows":     []map[string]any{{"name": "Ada"}, {"name": "Ravi"}, {"name": "Ada"}},
	})
	if res.IsError {
		t.Fatalf("judge: %s", text(res))
	}
	ans := out["answers"].([]any)
	if len(ans) != 3 || ans[0].(map[string]any)["pass"] != true || ans[1].(map[string]any)["pass"] != false {
		t.Errorf("answers = %v", ans)
	}
	if st := out["stats"].(map[string]any); st["judged"] != float64(2) {
		t.Errorf("judge stats = %v (duplicate row should be judged once)", st)
	}
	res, out = call(t, cs, "judge", map[string]any{
		"question": "team?", "kind": "choice", "options": []string{"billing", "technical"},
		"rows": []map[string]any{{"name": "Ada", "subject": "invoice"}},
	})
	if res.IsError || out["answers"].([]any)[0].(map[string]any)["choice"] == "" {
		t.Errorf("choice judge = %v %s", out, text(res))
	}

	res, out = call(t, cs, "list_tables", map[string]any{})
	if res.IsError {
		t.Fatalf("list_tables: %s", text(res))
	}
	names := map[string]bool{}
	for _, tb := range out["tables"].([]any) {
		m := tb.(map[string]any)
		if m["schema"] == "jevql_mcp_test" {
			names[m["name"].(string)] = true
		}
	}
	if !names["people"] || !names["tickets"] || !names["cities"] {
		t.Errorf("tables = %v", names)
	}

	res, out = call(t, cs, "describe_table", map[string]any{"name": "people"})
	if res.IsError {
		t.Fatalf("describe_table: %s", text(res))
	}
	cols := out["columns"].([]any)
	if out["kind"] != "table" || len(cols) != 9 || cols[1].(map[string]any)["name"] != "name" || cols[1].(map[string]any)["nullable"] != false {
		t.Errorf("describe = %v", out)
	}
	if idx := out["indexes"].([]any); len(idx) != 1 || !strings.Contains(idx[0].(string), "people_pkey") {
		t.Errorf("indexes = %v", out["indexes"])
	}
	res, _ = call(t, cs, "describe_table", map[string]any{"name": "nope"})
	if !res.IsError || !strings.Contains(text(res), "does not exist") {
		t.Errorf("missing table: %v %q", res.IsError, text(res))
	}

	res, _ = call(t, cs, "query", map[string]any{"sql": "SELECT * FROM nope"})
	if !res.IsError || !strings.HasPrefix(text(res), "sql:") {
		t.Errorf("bad table: %v %q", res.IsError, text(res))
	}
	res, _ = call(t, cs, "query", map[string]any{"sql": "SELECT name FROM people WHERE jev(people, 'x')", "max_rows": 2})
	if !res.IsError || !strings.HasPrefix(text(res), "budget:") {
		t.Errorf("budget: %v %q", res.IsError, text(res))
	}
}

func TestAllowWrites(t *testing.T) {
	ex, _ := testExec(t)
	cs := session(t, ex, Options{AllowWrites: true})
	res, out := call(t, cs, "query", map[string]any{"sql": "CREATE TEMP TABLE t_mcp (a int)"})
	if res.IsError || out["tag"] != "CREATE TABLE" {
		t.Errorf("ddl with writes allowed: %v %v", text(res), out)
	}
}

func TestHTTPHandler(t *testing.T) {
	if Handler(New(&exec.Executor{}, Options{})) == nil {
		t.Fatal("nil handler")
	}
}
