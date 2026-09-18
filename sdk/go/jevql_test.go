package jevql

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kylemclaren/jevql/internal/parse"
	"github.com/kylemclaren/jevql/internal/typesafe/typesafetest"
)

func testClient(t *testing.T) (*Client, *int32) {
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
	schema := "jevql_sdk_test"
	if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE; CREATE SCHEMA "+schema+"; SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "people.sql"))
	stmts, _ := parse.Split(string(data))
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	srv, calls := typesafetest.Server(t)
	c, err := New(ctx, Options{Conn: conn, APIKey: "test-key", APIURL: srv.URL, Model: "jev-test",
		CachePath: filepath.Join(t.TempDir(), "c.db"), BatchSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close(ctx)
		conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		conn.Close(ctx)
	})
	return c, calls
}

func TestClient(t *testing.T) {
	c, calls := testClient(t)
	ctx := context.Background()

	r, err := c.Query(ctx, "SELECT 1 AS one")
	if err != nil || r.Jev || r.Rows[0][0] != int32(1) && r.Rows[0][0] != int64(1) && r.Rows[0][0] != 1 {
		t.Errorf("select 1: %+v %v", r, err)
	}

	rows, err := c.QueryMaps(ctx, "SELECT name, jev_prob(people, 'wfh') AS p FROM people WHERE jev(people, 'wfh') ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 || rows[0]["name"] != "Ada Fernandes" || rows[0]["p"] != 0.9 {
		t.Errorf("rows = %v", rows)
	}
	first := *calls
	r, err = c.Query(ctx, "SELECT name FROM people WHERE jev(people, 'wfh')")
	if err != nil || r.Stats == nil || r.Stats.CacheHits != 12 || *calls != first {
		t.Errorf("cache: stats=%+v calls=%d err=%v", r.Stats, *calls-first, err)
	}

	thr := 0.95
	r, err = c.Query(ctx, "SELECT name FROM people WHERE jev(people, 'wfh')", QueryOptions{Threshold: &thr})
	if err != nil || r.RowCount != 0 {
		t.Errorf("threshold override: %+v %v", r, err)
	}

	x, err := c.Explain(ctx, "SELECT name FROM people WHERE jev(people, 'new') AND country = 'PT'")
	if err != nil || x.Rows != 6 || x.Batches != 2 {
		t.Errorf("explain: %+v %v", x, err)
	}
	if _, err := c.Explain(ctx, "SELECT 1"); err == nil {
		t.Error("explain of plain SQL should error")
	}

	_, err = c.Query(ctx, "SELECT * FROM nope")
	var je *Error
	if !errors.As(err, &je) || je.Code != "sql" {
		t.Errorf("bad table error = %v", err)
	}
	mr := 2
	_, err = c.Query(ctx, "SELECT name FROM people WHERE jev(people, 'x')", QueryOptions{MaxRows: &mr})
	if !errors.As(err, &je) || je.Code != "budget" {
		t.Errorf("budget error = %v", err)
	}
	if _, err := c.Query(ctx, "   "); err == nil {
		t.Error("empty statement should error")
	}
}

func TestNewDefaults(t *testing.T) {
	url := os.Getenv("PGTEST_URL")
	if url == "" {
		t.Skip("PGTEST_URL not set")
	}
	ctx := context.Background()
	c, err := New(ctx, Options{DatabaseURL: url, NoCache: true, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if c.ex.Opts.MaxRows != 2500 || c.ex.Opts.Threshold != 0.5 || c.ex.TS.Model != "jev-latest" || c.cache != nil {
		t.Errorf("defaults: %+v", c.ex.Opts)
	}
	if _, err := c.Query(ctx, "SELECT 2"); err != nil {
		t.Error(err)
	}
}
