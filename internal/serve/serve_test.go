package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/parse"
	"github.com/kylemclaren/jevql/internal/typesafe"
	"github.com/kylemclaren/jevql/internal/typesafe/typesafetest"
	"github.com/kylemclaren/jevql/internal/wire"
)

func post(t *testing.T, h http.Handler, token string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func TestAuthAndValidation(t *testing.T) {
	s := &Server{Token: "secret", Version: "test", Model: "m"}
	h := s.Handler()
	code, out := post(t, h, "", wire.QueryRequest{SQL: "SELECT 1"})
	if code != 401 || out["code"] != "auth" {
		t.Errorf("no token: %d %v", code, out)
	}
	code, out = post(t, h, "wrong", wire.QueryRequest{SQL: "SELECT 1"})
	if code != 401 || out["code"] != "auth" {
		t.Errorf("bad token: %d %v", code, out)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Errorf("bad json: %d %s", rr.Code, rr.Body.String())
	}
	code, out = post(t, h, "secret", wire.QueryRequest{SQL: "  "})
	if code != 400 || out["code"] != "sql" {
		t.Errorf("empty sql: %d %v", code, out)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var hb map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &hb)
	if rr.Code != 200 || hb["ok"] != true || hb["version"] != "test" {
		t.Errorf("health: %d %s", rr.Code, rr.Body.String())
	}
}

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
	schema := "jevql_serve_test"
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

func TestQueries(t *testing.T) {
	ex, calls := testExec(t)
	s := &Server{Exec: ex, Version: "test", Model: "jev-test"}
	h := s.Handler()

	code, out := post(t, h, "", wire.QueryRequest{SQL: "SELECT 1 AS one, 'x' AS t, NULL::int AS n"})
	if code != 200 {
		t.Fatalf("select 1: %d %v", code, out)
	}
	if out["jev"] != false || out["tag"] != "SELECT 1" || out["stats"] != nil {
		t.Errorf("passthrough doc = %v", out)
	}
	row := out["rows"].([]any)[0].([]any)
	if row[0] != float64(1) || row[1] != "x" || row[2] != nil {
		t.Errorf("row = %v", row)
	}

	code, out = post(t, h, "", wire.QueryRequest{SQL: "SELECT name FROM people WHERE jev(people, 'wfh') ORDER BY name"})
	if code != 200 || out["jev"] != true || out["row_count"] != float64(7) {
		t.Errorf("jev query: %d %v", code, out)
	}
	if st, _ := out["stats"].(map[string]any); st == nil || st["judged"] != float64(12) {
		t.Errorf("stats = %v", out["stats"])
	}

	// threshold override: mock returns 0.9 or 0.1, so 0.95 passes nobody
	thr := 0.95
	code, out = post(t, h, "", wire.QueryRequest{SQL: "SELECT name FROM people WHERE jev(people, 'wfh')", Threshold: &thr})
	if code != 200 || out["row_count"] != float64(0) {
		t.Errorf("threshold override: %d %v", code, out)
	}

	before := *calls
	code, out = post(t, h, "", wire.QueryRequest{SQL: "SELECT name FROM people WHERE jev(people, 'other')", Explain: true})
	if code != 200 || out["explain"] == nil || *calls != before {
		t.Errorf("explain: %d %v calls=%d", code, out["explain"], *calls-before)
	}

	code, out = post(t, h, "", wire.QueryRequest{SQL: "SELECT * FROM nope"})
	if code != 400 || out["code"] != "sql" {
		t.Errorf("bad table: %d %v", code, out)
	}
	code, out = post(t, h, "", wire.QueryRequest{SQL: "SELECT name FROM people WHERE jev(people, 'x') OR true"})
	if code != 400 || out["code"] != "sql" {
		t.Errorf("or jev: %d %v", code, out)
	}
	mr := 2
	code, out = post(t, h, "", wire.QueryRequest{SQL: "SELECT name FROM people WHERE jev(people, 'x')", MaxRows: &mr})
	if code != 402 || out["code"] != "budget" {
		t.Errorf("budget: %d %v", code, out)
	}
	ex.TS.APIKey = "wrong"
	ex.TS.MaxRetries = 0
	code, out = post(t, h, "", wire.QueryRequest{SQL: "SELECT name FROM people WHERE jev(people, 'brand new question')"})
	if code != 502 || out["code"] != "api" {
		t.Errorf("api error: %d %v", code, out)
	}
	code, _ = post(t, h, "", wire.QueryRequest{SQL: "CREATE TEMP TABLE t_serve (a int)"})
	if code != 200 {
		t.Errorf("ddl: %d", code)
	}
}
