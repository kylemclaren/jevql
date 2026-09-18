package rewrite

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kylemclaren/jevql/internal/parse"
)

var update = flag.Bool("update", false, "rewrite golden files")

type fakeCatalog map[string][]Column

func (f fakeCatalog) Columns(_ context.Context, schema, rel string) ([]Column, error) {
	if c, ok := f[rel]; ok {
		return c, nil
	}
	return nil, os.ErrNotExist
}

var cat = fakeCatalog{
	"people":  {{"id", "integer"}, {"name", "text"}, {"city", "text"}, {"country", "text"}, {"job_title", "text"}, {"bio", "text"}},
	"cities":  {{"name", "text"}, {"country", "text"}},
	"tickets": {{"id", "integer"}, {"subject", "text"}, {"body", "text"}, {"status", "text"}},
	"blobs":   {{"id", "integer"}, {"photo", "bytea"}, {"name", "text"}},
}

func build(t *testing.T, sql string, opts Options) *Plan {
	t.Helper()
	st, err := parse.ParseOne(sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a, err := parse.Analyze(st)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	p, err := Build(context.Background(), a, cat, opts)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return p
}

func TestGolden(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "queries")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files in %s", dir)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".sql")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			p := build(t, strings.TrimSpace(string(src)), Options{})
			golden := filepath.Join("..", "..", "testdata", "golden", name+".collect.sql")
			got := p.CollectSQL + "\n"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file %s (run with -update)", golden)
			}
			if string(want) != got {
				t.Errorf("collect SQL mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestLayout(t *testing.T) {
	p := build(t, "SELECT name, jev_prob(people, 'x') AS p, *, count_me FROM people WHERE jev(people, 'y') ORDER BY p", Options{})
	if len(p.Sources) != 1 || len(p.Sources[0].Fields) != 6 {
		t.Fatalf("sources = %+v", p.Sources)
	}
	// 6 hidden source columns, then name, (jev: none), *, sep, count_me
	want := []TargetLayout{{Start: 6, Count: 1}, {Start: -1}, {Start: 7, Count: -1, Sep: true}, {Start: 8, Count: 1}}
	for i, w := range want {
		if p.Layout[i] != w {
			t.Errorf("layout[%d] = %+v want %+v", i, p.Layout[i], w)
		}
	}
	if p.ServerOrder {
		t.Error("ORDER BY on a jev column must not be pushed to the server")
	}
	if !strings.Contains(p.CollectSQL, "NULL::int AS __jev_sep") {
		t.Errorf("expected separator after *, got %s", p.CollectSQL)
	}
}

func TestServerOrderPushdown(t *testing.T) {
	p := build(t, "SELECT name, jev_prob(people, 'x') AS p FROM people WHERE country = 'PT' ORDER BY name DESC LIMIT 5 OFFSET 2", Options{})
	if !p.ServerOrder {
		t.Fatal("expected ORDER BY/LIMIT pushdown")
	}
	if !strings.HasSuffix(p.CollectSQL, "ORDER BY name DESC LIMIT 5 OFFSET 2") {
		t.Errorf("collect SQL = %s", p.CollectSQL)
	}
	// A jev predicate disables pushdown.
	p = build(t, "SELECT name FROM people WHERE jev(people, 'x') ORDER BY name LIMIT 5", Options{})
	if p.ServerOrder || strings.Contains(p.CollectSQL, "LIMIT") {
		t.Errorf("unexpected pushdown: %s", p.CollectSQL)
	}
}

func TestColumnList(t *testing.T) {
	p := build(t, "SELECT name FROM people WHERE jev((name, job_title), 'x')", Options{})
	if got := p.CollectSQL; got != "SELECT name AS __jev_s0_0, job_title AS __jev_s0_1, name FROM people" {
		t.Errorf("collect = %s", got)
	}
	if p.Sources[0].Fields[1].Name != "job_title" {
		t.Errorf("fields = %+v", p.Sources[0].Fields)
	}
}

func TestColumnsOverride(t *testing.T) {
	p := build(t, "SELECT name FROM blobs WHERE jev(blobs, 'x')", Options{Columns: []string{"name"}})
	if !strings.HasPrefix(p.CollectSQL, "SELECT blobs.name AS __jev_s0_0, name FROM blobs") {
		t.Errorf("collect = %s", p.CollectSQL)
	}
}

func TestByteaRefused(t *testing.T) {
	st, _ := parse.ParseOne("SELECT name FROM blobs WHERE jev(blobs, 'x')")
	a, err := parse.Analyze(st)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(context.Background(), a, cat, Options{})
	if err == nil || !strings.Contains(err.Error(), "bytea") {
		t.Errorf("expected bytea error, got %v", err)
	}
}

func TestGroupedCollect(t *testing.T) {
	p := build(t, "SELECT jev_choice(tickets, 'q', ARRAY['a','b']) AS k, count(*), sum(id) FROM tickets WHERE status = 'open' GROUP BY 1 ORDER BY 2 DESC", Options{})
	want := "SELECT tickets.id AS __jev_s0_0, tickets.subject AS __jev_s0_1, tickets.body AS __jev_s0_2, tickets.status AS __jev_s0_3, id AS __jev_agg2 FROM tickets WHERE status = 'open'"
	if p.CollectSQL != want {
		t.Errorf("collect = %s\nwant    = %s", p.CollectSQL, want)
	}
}
