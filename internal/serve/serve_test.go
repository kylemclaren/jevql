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
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kylemclaren/jevql/internal/cache"
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

func TestListenRandomPortReady(t *testing.T) {
	s := &Server{Version: "test", Model: "m"}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx, "127.0.0.1:0", func(b string) { ready <- b }) }()
	var addr string
	select {
	case addr = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("no ready callback")
	}
	if addr == "127.0.0.1:0" || addr == "" {
		t.Fatalf("bound address = %q", addr)
	}
	res, err := http.Get("http://" + addr + "/v1/health")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("health: %v %v", err, res)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func do(t *testing.T, h http.Handler, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

func TestJudgeEndpoint(t *testing.T) {
	ex, calls := testExec(t)
	st, err := cache.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ex.Cache = st
	s := &Server{Exec: ex, Version: "test", Model: "jev-test"}
	h := s.Handler()
	rows := []map[string]any{{"name": "Ada"}, {"name": "Zed"}, {"name": "Ada"}}
	code, body := do(t, h, "POST", "/v1/judge", "", wire.JudgeRequest{Question: "wfh", Rows: rows})
	if code != 200 {
		t.Fatalf("judge: %d %s", code, body)
	}
	var res wire.JudgeResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Answers) != 3 || !*res.Answers[0].Pass || *res.Answers[1].Pass || res.Stats.Judged != 2 || *calls != 1 {
		t.Errorf("answers=%+v stats=%+v calls=%d", res.Answers, res.Stats, *calls)
	}
	// second call: all cache hits, no HTTP
	code, body = do(t, h, "POST", "/v1/judge", "", wire.JudgeRequest{Question: "wfh", Rows: rows[:2]})
	_ = json.Unmarshal(body, &res)
	if code != 200 || res.Stats.CacheHits != 2 || *calls != 1 {
		t.Errorf("cache: %d %+v calls=%d", code, res.Stats, *calls)
	}
	// score with norm
	code, body = do(t, h, "POST", "/v1/judge", "", wire.JudgeRequest{Question: "how?", Kind: "score", Options: []string{"lo", "mid", "hi"}, Rows: rows[:1]})
	_ = json.Unmarshal(body, &res)
	if code != 200 || res.Answers[0].Score == nil || *res.Answers[0].Norm != 0.75 {
		t.Errorf("score: %d %+v", code, res.Answers)
	}
	// validation
	for _, bad := range []wire.JudgeRequest{{Question: "", Rows: rows}, {Question: "x", Kind: "bogus", Rows: rows}, {Question: "x", Kind: "choice", Rows: rows}} {
		code, body = do(t, h, "POST", "/v1/judge", "", bad)
		var eb wire.ErrorBody
		_ = json.Unmarshal(body, &eb)
		if code != 400 || eb.Code != "sql" {
			t.Errorf("validation %+v: %d %s", bad, code, body)
		}
	}
	// budget
	ex.Opts.MaxRows = 2
	code, body = do(t, h, "POST", "/v1/judge", "", wire.JudgeRequest{Question: "new q", Rows: rows})
	if code != 402 {
		t.Errorf("budget: %d %s", code, body)
	}
	ex.Opts.MaxRows = 2500
	// auth
	s.Token = "t"
	code, _ = do(t, h, "POST", "/v1/judge", "", wire.JudgeRequest{Question: "x", Rows: rows})
	if code != 401 {
		t.Errorf("auth: %d", code)
	}
}

func TestExplainEndpoint(t *testing.T) {
	ex, calls := testExec(t)
	s := &Server{Exec: ex, Version: "test", Model: "jev-test"}
	h := s.Handler()
	code, body := do(t, h, "POST", "/v1/explain", "", wire.QueryRequest{SQL: "SELECT name FROM people WHERE jev(people, 'wfh') AND country = 'PT'"})
	var x wire.Explain
	_ = json.Unmarshal(body, &x)
	if code != 200 || x.Rows != 6 || x.CollectSQL == "" || *calls != 0 {
		t.Errorf("explain: %d %s calls=%d", code, body, *calls)
	}
	code, body = do(t, h, "POST", "/v1/explain", "", wire.QueryRequest{SQL: "SELECT 1"})
	if code != 400 {
		t.Errorf("plain sql explain: %d %s", code, body)
	}
}

func TestCacheEndpoints(t *testing.T) {
	ex, _ := testExec(t)
	st, err := cache.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ex.Cache = st
	s := &Server{Exec: ex, Version: "test", Model: "jev-test"}
	h := s.Handler()
	code, body := do(t, h, "GET", "/v1/cache", "", nil)
	if code != 403 {
		t.Errorf("disabled: %d %s", code, body)
	}
	s.CacheAdmin = true
	do(t, h, "POST", "/v1/judge", "", wire.JudgeRequest{Question: "wfh", Rows: []map[string]any{{"name": "Ada"}}})
	code, body = do(t, h, "GET", "/v1/cache", "", nil)
	var info CacheInfo
	_ = json.Unmarshal(body, &info)
	if code != 200 || info.Entries != 1 || info.Path == "" {
		t.Errorf("info: %d %s", code, body)
	}
	code, body = do(t, h, "DELETE", "/v1/cache", "", nil)
	var cl CacheCleared
	_ = json.Unmarshal(body, &cl)
	if code != 200 || cl.Deleted != 1 {
		t.Errorf("clear: %d %s", code, body)
	}
}

func TestSchemaEndpoints(t *testing.T) {
	ex, _ := testExec(t)
	s := &Server{Exec: ex, Version: "test", Model: "jev-test"}
	h := s.Handler()
	code, body := do(t, h, "GET", "/v1/schema/tables", "", nil)
	var list []TableInfo
	_ = json.Unmarshal(body, &list)
	names := map[string]bool{}
	for _, tb := range list {
		if tb.Schema == "jevql_serve_test" {
			names[tb.Name] = true
		}
	}
	if code != 200 || !names["people"] || !names["tickets"] || !names["cities"] {
		t.Errorf("tables: %d %v", code, names)
	}
	code, body = do(t, h, "GET", "/v1/schema/tables/people", "", nil)
	var d TableDetail
	_ = json.Unmarshal(body, &d)
	if code != 200 || d.Name != "people" || d.Kind != "table" || len(d.Columns) != 9 || d.Columns[0].Name != "id" || d.Columns[0].Nullable || len(d.Indexes) != 1 {
		t.Errorf("describe: %d %s", code, body)
	}
	code, body = do(t, h, "GET", "/v1/schema/tables/jevql_serve_test.people", "", nil)
	if code != 200 {
		t.Errorf("qualified: %d %s", code, body)
	}
	code, body = do(t, h, "GET", "/v1/schema/tables/nope", "", nil)
	var eb wire.ErrorBody
	_ = json.Unmarshal(body, &eb)
	if code != 404 || eb.Code != "sql" {
		t.Errorf("missing: %d %s", code, body)
	}
}

func TestCORS(t *testing.T) {
	s := &Server{Token: "x", Version: "t", Model: "m", CORSOrigins: []string{"https://jevql.fly.dev"}}
	h := s.Handler()
	req := httptest.NewRequest(http.MethodOptions, "/v1/query", nil)
	req.Header.Set("Origin", "https://jevql.fly.dev")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || rr.Header().Get("Access-Control-Allow-Origin") != "https://jevql.fly.dev" {
		t.Errorf("preflight: %d %v", rr.Code, rr.Header())
	}
	req = httptest.NewRequest(http.MethodOptions, "/v1/query", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unknown origin must not be allowed")
	}
}

func TestRateLimit(t *testing.T) {
	s := &Server{Version: "t", Model: "m", RatePerMinute: 3}
	h := s.Handler()
	codes := []int{}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		codes = append(codes, rr.Code)
	}
	if codes[0] != 200 || codes[2] != 200 || codes[3] != 429 || codes[4] != 429 {
		t.Errorf("codes = %v", codes)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "10.0.0.2:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("other client should be allowed, got %d", rr.Code)
	}
}
