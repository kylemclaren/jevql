package exec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kylemclaren/jevql/internal/cache"
	"github.com/kylemclaren/jevql/internal/parse"
	"github.com/kylemclaren/jevql/internal/typesafe"
	"github.com/kylemclaren/jevql/internal/typesafe/typesafetest"
)

// testDB connects to $PGTEST_URL (skipping otherwise) and loads
// testdata/people.sql into a private schema.
func testDB(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("PGTEST_URL")
	if url == "" {
		t.Skip("PGTEST_URL not set; skipping database tests")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := "jevql_test"
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
			t.Fatalf("load testdata: %v\n%s", err, s)
		}
	}
	t.Cleanup(func() {
		conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		conn.Close(ctx)
	})
	return conn
}

func newExec(t *testing.T, conn *pgx.Conn, withCache bool) (*Executor, *int32) {
	t.Helper()
	srv, calls := typesafetest.Server(t)
	ts := typesafe.New(srv.URL, "test-key", "jev-test", 4, 2)
	var st *cache.Store
	if withCache {
		var err error
		st, err = cache.Open(filepath.Join(t.TempDir(), "cache.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
	}
	return &Executor{DB: conn, TS: ts, Cache: st, Opts: Options{Threshold: 0.5, BatchSize: 4, Concurrency: 2, MaxRows: 2500, Model: "jev-test"}}, calls
}

func names(t *testing.T, res *Result, col int) []string {
	t.Helper()
	var out []string
	for _, r := range res.Table.Rows {
		out = append(out, r[col].Text)
	}
	return out
}

func TestPassthrough(t *testing.T) {
	conn := testDB(t)
	ex, calls := newExec(t, conn, false)
	res, err := ex.Run(context.Background(), "SELECT 1 AS one, NULL::text AS n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Jev || len(res.Table.Columns) != 2 || res.Table.Rows[0][0].Text != "1" || !res.Table.Rows[0][1].Null {
		t.Errorf("result = %+v", res.Table)
	}
	res, err = ex.Run(context.Background(), "CREATE TEMP TABLE tmp_x (a int)")
	if err != nil || res.Table != nil || res.Tag != "CREATE TABLE" {
		t.Errorf("ddl result = %+v err=%v", res, err)
	}
	if *calls != 0 {
		t.Error("passthrough must not call TypeSafe")
	}
}

func TestWhereJev(t *testing.T) {
	conn := testDB(t)
	ex, calls := newExec(t, conn, false)
	res, err := ex.Run(context.Background(), "SELECT name FROM people WHERE jev(people, 'could work from home') ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	// The mock passes names starting with A-M.
	got := names(t, res, 0)
	for _, n := range got {
		if n[0] < 'A' || n[0] > 'M' {
			t.Errorf("unexpected row %q", n)
		}
	}
	if len(got) != 7 {
		t.Errorf("rows = %v", got)
	}
	if *calls != 3 { // 12 rows / batch 4
		t.Errorf("requests = %d", *calls)
	}
	if res.Stats.Judged != 12 || res.Stats.Requests != 3 || res.Stats.InputTokens != 1200 {
		t.Errorf("stats = %+v", res.Stats)
	}
}

func TestProbOrderLimitAndStar(t *testing.T) {
	conn := testDB(t)
	ex, _ := newExec(t, conn, false)
	res, err := ex.Run(context.Background(), "SELECT name, jev_prob(people, 'x') AS p, * FROM people WHERE country = 'PT' ORDER BY p DESC, name LIMIT 3")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Table.Columns) != 2+9 || res.Table.Columns[1] != "p" || res.Table.Columns[2] != "id" {
		t.Errorf("columns = %v", res.Table.Columns)
	}
	if len(res.Table.Rows) != 3 {
		t.Fatalf("rows = %d", len(res.Table.Rows))
	}
	for _, r := range res.Table.Rows {
		if r[1].Text != "0.9" {
			t.Errorf("expected top rows to have p=0.9, got %s (%s)", r[1].Text, r[0].Text)
		}
	}
	if res.Table.Rows[0][0].Text != "Ada Fernandes" {
		t.Errorf("first row = %s", res.Table.Rows[0][0].Text)
	}
}

func TestServerPushdownJudgesOnlyLimitedRows(t *testing.T) {
	conn := testDB(t)
	ex, _ := newExec(t, conn, false)
	res, err := ex.Run(context.Background(), "SELECT name, jev_prob(people, 'x') AS p FROM people ORDER BY name LIMIT 2 OFFSET 1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Judged != 2 || res.Stats.CollectRows != 2 {
		t.Errorf("expected 2 judged rows, stats = %+v", res.Stats)
	}
	if got := names(t, res, 0); strings.Join(got, ",") != "Carlos Ruiz,Hans Müller" {
		t.Errorf("rows = %v", got)
	}
}

func TestGroupByChoice(t *testing.T) {
	conn := testDB(t)
	ex, _ := newExec(t, conn, false)
	res, err := ex.Run(context.Background(), "SELECT jev_choice(tickets, 'team?', ARRAY['billing','technical']) AS team, count(*), sum(id), min(id) FROM tickets WHERE status = 'open' GROUP BY 1 ORDER BY 2 DESC, 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Table.Columns) != 4 || res.Table.Columns[0] != "team" || res.Table.Columns[1] != "count" {
		t.Errorf("columns = %v", res.Table.Columns)
	}
	total := 0
	for _, r := range res.Table.Rows {
		var n int
		if _, err := parseInt(r[1].Text, &n); err != nil {
			t.Fatal(err)
		}
		total += n
		if r[0].Text != "billing" && r[0].Text != "technical" {
			t.Errorf("bad team %q", r[0].Text)
		}
	}
	if total != 7 {
		t.Errorf("total count = %d, want 7 open tickets", total)
	}
	if len(res.Table.Rows) == 2 {
		var a, b int
		parseInt(res.Table.Rows[0][1].Text, &a)
		parseInt(res.Table.Rows[1][1].Text, &b)
		if a < b {
			t.Errorf("not sorted by count desc: %d %d", a, b)
		}
	}
}

func parseInt(s string, n *int) (int, error) {
	var err error
	*n, err = atoi(s)
	return *n, err
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number: " + s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func TestJoin(t *testing.T) {
	conn := testDB(t)
	ex, _ := newExec(t, conn, false)
	res, err := ex.Run(context.Background(), "SELECT p.name, c.country FROM people p JOIN cities c ON c.name = p.city WHERE jev(p, 'wfh') AND c.country = 'PT' ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	got := names(t, res, 0)
	if strings.Join(got, ",") != "Ada Fernandes,Inês Rocha,Joana Silva,Miguel Costa" {
		t.Errorf("rows = %v", got)
	}
}

func TestCacheSecondRunMakesNoHTTP(t *testing.T) {
	conn := testDB(t)
	ex, calls := newExec(t, conn, true)
	q := "SELECT name FROM people WHERE jev(people, 'wfh')"
	if _, err := ex.Run(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	first := *calls
	if first == 0 {
		t.Fatal("expected HTTP calls on first run")
	}
	res, err := ex.Run(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != first {
		t.Errorf("second run made %d extra HTTP calls", *calls-first)
	}
	if res.Stats.CacheHits != 12 || res.Stats.Requests != 0 {
		t.Errorf("stats = %+v", res.Stats)
	}
	// Same rows, different function, same question: still cached.
	res, err = ex.Run(context.Background(), "SELECT name, jev_prob(people, 'wfh') FROM people")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != first || res.Stats.CacheHits != 12 {
		t.Errorf("jev_prob should reuse cached noul answers: calls=%d stats=%+v", *calls, res.Stats)
	}
}

func TestExplainMakesNoHTTP(t *testing.T) {
	conn := testDB(t)
	ex, calls := newExec(t, conn, false)
	ex.Opts.Explain = true
	res, err := ex.Run(context.Background(), "SELECT name FROM people WHERE jev(people, 'wfh') AND country = 'PT'")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Errorf("explain made %d HTTP calls", *calls)
	}
	if res.Explain == nil || res.Explain.Rows != 6 || res.Explain.Batches != 2 || res.Explain.Tokens == 0 {
		t.Errorf("explain = %+v", res.Explain)
	}
	if !strings.Contains(res.Explain.CollectSQL, "WHERE country = 'PT'") {
		t.Errorf("collect SQL = %s", res.Explain.CollectSQL)
	}
}

func TestMaxRows(t *testing.T) {
	conn := testDB(t)
	ex, calls := newExec(t, conn, false)
	ex.Opts.MaxRows = 2
	_, err := ex.Run(context.Background(), "SELECT name FROM people WHERE jev(people, 'wfh')")
	var be *BudgetError
	if !errors.As(err, &be) {
		t.Fatalf("expected BudgetError, got %v", err)
	}
	if *calls != 0 {
		t.Error("max-rows must abort before HTTP")
	}
}

func TestByteaRefused(t *testing.T) {
	conn := testDB(t)
	ex, _ := newExec(t, conn, false)
	if _, err := conn.Exec(context.Background(), "CREATE TABLE blobs (id int, photo bytea, name text); INSERT INTO blobs VALUES (1, '\\x00', 'a')"); err != nil {
		t.Fatal(err)
	}
	_, err := ex.Run(context.Background(), "SELECT name FROM blobs WHERE jev(blobs, 'x')")
	if err == nil || !strings.Contains(err.Error(), "bytea") {
		t.Errorf("expected bytea refusal, got %v", err)
	}
	ex.Opts.Columns = []string{"name"}
	if _, err := ex.Run(context.Background(), "SELECT name FROM blobs WHERE jev(blobs, 'x')"); err != nil {
		t.Errorf("--columns should bypass the bytea check: %v", err)
	}
	ex.Opts.Columns = nil
	if _, err := ex.Run(context.Background(), "SELECT name FROM blobs WHERE jev((name, id), 'x')"); err != nil {
		t.Errorf("column-list form should bypass the bytea check: %v", err)
	}
}

func TestAllFunctions(t *testing.T) {
	conn := testDB(t)
	ex, _ := newExec(t, conn, false)
	res, err := ex.Run(context.Background(), `SELECT name,
  jev(people, 'q', 0.95) AS b, jev_prob(people, 'q') AS p,
  jev_choice(people, 'c', ARRAY['x','y']) AS ch,
  jev_score(people, 's', ARRAY['lo','mid','hi']) AS sc, jev_score_norm(people, 's', ARRAY['lo','mid','hi']) AS sn,
  jev_confidence(people, 'q') AS cf, jev_confidence(people, 'c', 'choice', ARRAY['x','y']) AS cc,
  jev_eval(people, 'q') AS raw
FROM people WHERE name = 'Ada Fernandes'`)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Table.Rows[0]
	want := []string{"Ada Fernandes", "f", "0.9", "", "1.5", "0.75", "0.9", "0.8", ""}
	var raw map[string]any
	if err := json.Unmarshal([]byte(r[8].Text), &raw); err != nil || raw["type"] != "noul" || raw["noul"] != 0.9 {
		t.Errorf("raw = %q (%v)", r[8].Text, err)
	}
	for i, w := range want {
		if i == 8 {
			continue
		}
		if i == 3 {
			if r[i].Text != "x" && r[i].Text != "y" {
				t.Errorf("choice = %q", r[i].Text)
			}
			continue
		}
		if r[i].Text != w {
			t.Errorf("col %s = %q want %q", res.Table.Columns[i], r[i].Text, w)
		}
	}
	if res.Stats.Judged != 3 { // noul q, choice c, score s: one row each
		t.Errorf("judged = %d", res.Stats.Judged)
	}
}
