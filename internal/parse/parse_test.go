package parse

import (
	"strings"
	"testing"
)

func mustAnalyze(t *testing.T, sql string) *Analysis {
	t.Helper()
	st, err := ParseOne(sql)
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	a, err := Analyze(st)
	if err != nil {
		t.Fatalf("analyze %q: %v", sql, err)
	}
	return a
}

func TestHasJev(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1": false,
		"SELECT * FROM people WHERE jev(people, 'x')":  true,
		"SELECT jev_prob(p, 'x') FROM people p":        true,
		"SELECT * FROM people WHERE jevx(people, 'x')": false,
		"INSERT INTO t VALUES (1)":                     false,
		"SELECT JEV(people, 'x') FROM people":          true,
	}
	for sql, want := range cases {
		st, err := ParseOne(sql)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		if st.HasJev != want {
			t.Errorf("%s: HasJev=%v want %v", sql, st.HasJev, want)
		}
	}
}

func TestSplit(t *testing.T) {
	got, _ := Split("SELECT 1; SELEC 2;\n-- c\nSELECT ';' AS s; $$a;b$$; ")
	want := []string{"SELECT 1", "SELEC 2", "-- c\nSELECT ';' AS s", "$$a;b$$"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Split = %q, want %q", got, want)
	}
}

func TestAnalyzeMustWork(t *testing.T) {
	tests := []struct {
		sql      string
		targets  int
		preds    int
		grouped  bool
		orderJev bool
		calls    int
		sources  int
		limit    int64
	}{
		{"SELECT * FROM people WHERE jev(people, 'could work from home')", 1, 1, false, false, 1, 1, 0},
		{"SELECT name, jev_prob(people, 'the name is European') AS p FROM people WHERE country = 'PT' ORDER BY p DESC LIMIT 10", 2, 0, false, true, 1, 1, 10},
		{"SELECT jev_choice(tickets, 'which team?', ARRAY['billing','technical','sales']) AS team, count(*) FROM tickets WHERE status = 'open' GROUP BY 1", 2, 0, true, false, 1, 1, 0},
		{"SELECT * FROM people p JOIN cities c ON c.name = p.city WHERE jev(p, 'could work from home') AND c.country = 'PT'", 1, 1, false, false, 1, 1, 0},
		{"SELECT name FROM people WHERE NOT jev(people, 'x') AND jev(people, 'y', 0.8)", 1, 2, false, false, 2, 1, 0},
		{"SELECT name, jev((name, bio), 'x') AS a, jev_prob(people, 'x') AS b FROM people", 3, 0, false, false, 2, 2, 0},
		// hidden targets for ORDER BY / GROUP BY expressions not in the select list
		{"SELECT name FROM people WHERE jev(people, 'x') ORDER BY age DESC", 2, 1, false, false, 1, 1, 0},
		{"SELECT count(*) FROM people WHERE jev(people, 'x') GROUP BY country", 2, 1, true, false, 1, 1, 0},
		{"SELECT name FROM people ORDER BY jev_prob(people, 'x')", 2, 0, false, true, 1, 1, 0},
	}
	for _, tc := range tests {
		a := mustAnalyze(t, tc.sql)
		if len(a.Targets) != tc.targets {
			t.Errorf("%s: targets=%d want %d", tc.sql, len(a.Targets), tc.targets)
		}
		if len(a.Predicates) != tc.preds {
			t.Errorf("%s: predicates=%d want %d", tc.sql, len(a.Predicates), tc.preds)
		}
		if a.Grouped != tc.grouped {
			t.Errorf("%s: grouped=%v want %v", tc.sql, a.Grouped, tc.grouped)
		}
		if a.OrderHasJev() != tc.orderJev {
			t.Errorf("%s: orderHasJev=%v want %v", tc.sql, a.OrderHasJev(), tc.orderJev)
		}
		if len(a.Calls) != tc.calls {
			t.Errorf("%s: calls=%d want %d", tc.sql, len(a.Calls), tc.calls)
		}
		if len(a.Sources) != tc.sources {
			t.Errorf("%s: sources=%d want %d", tc.sql, len(a.Sources), tc.sources)
		}
		var lim int64
		if a.Limit != nil {
			lim = *a.Limit
		}
		if lim != tc.limit {
			t.Errorf("%s: limit=%d want %d", tc.sql, lim, tc.limit)
		}
	}
}

func TestAnalyzeDetails(t *testing.T) {
	a := mustAnalyze(t, "SELECT name FROM people p WHERE NOT jev(p, 'x', 0.7) AND country = 'PT' AND age > 3")
	if a.Where == nil || DeparseExpr(a.Where) != "country = 'PT' AND age > 3" {
		t.Errorf("remaining WHERE = %q", DeparseExpr(a.Where))
	}
	p := a.Predicates[0]
	if !p.Negate || p.Call.Threshold == nil || *p.Call.Threshold != 0.7 || p.Call.Source.Alias != "p" {
		t.Errorf("predicate = %+v call=%+v", p, p.Call)
	}
	if len(a.Tables) != 1 || a.Tables[0].Alias != "p" || a.Tables[0].Rel != "people" {
		t.Errorf("tables = %+v", a.Tables)
	}

	a = mustAnalyze(t, "SELECT jev_score(t, 'q', ARRAY['lo','mid','hi']) AS s, jev_confidence(t, 'q', 'choice', ARRAY['a','b']) AS c, jev_eval(t, 'q') AS e FROM tickets t ORDER BY s DESC NULLS LAST, 1")
	if a.Targets[0].Call.Kind != KindScore || len(a.Targets[0].Call.Options) != 3 {
		t.Errorf("score call = %+v", a.Targets[0].Call)
	}
	if a.Targets[1].Call.Kind != KindChoice || a.Targets[1].Call.Fn != "jev_confidence" {
		t.Errorf("confidence call = %+v", a.Targets[1].Call)
	}
	if a.Targets[2].Call.Kind != KindNoul {
		t.Errorf("eval call = %+v", a.Targets[2].Call)
	}
	if len(a.OrderKeys) != 2 || a.OrderKeys[0].Target != 0 || !a.OrderKeys[0].Desc || a.OrderKeys[0].NullsFirst || a.OrderKeys[1].Target != 0 {
		t.Errorf("order keys = %+v", a.OrderKeys)
	}

	a = mustAnalyze(t, "SELECT jev_choice(t, 'q', ARRAY['a','b']) AS k, count(*), sum(id), avg(id) FROM tickets t GROUP BY k ORDER BY count(*) DESC")
	if len(a.GroupKeys) != 1 || a.GroupKeys[0] != 0 {
		t.Errorf("group keys = %v", a.GroupKeys)
	}
	if a.Targets[1].Kind != TargetAgg || !a.Targets[1].Agg.Star || a.Targets[2].Agg.Fn != "sum" {
		t.Errorf("agg targets = %+v", a.Targets[1:])
	}
	if a.OrderKeys[0].Target != 1 {
		t.Errorf("order by count(*) should resolve to target 1, got %d", a.OrderKeys[0].Target)
	}
}

func TestAnalyzeMustError(t *testing.T) {
	tests := []struct{ sql, want string }{
		{"INSERT INTO t SELECT * FROM people WHERE jev(people, 'x')", "only be used in a SELECT"},
		{"UPDATE people SET a = 1 WHERE jev(people, 'x')", "UPDATE"},
		{"DELETE FROM people WHERE jev(people, 'x')", "DELETE"},
		{"CREATE TABLE t AS SELECT * FROM people WHERE jev(people, 'x')", "DDL"},
		{"WITH t AS (SELECT * FROM people WHERE jev(people, 'x')) SELECT * FROM t", "CTE"},
		{"SELECT * FROM (SELECT * FROM people WHERE jev(people, 'x')) s", "subquery"},
		{"SELECT * FROM people WHERE id IN (SELECT id FROM people WHERE jev(people, 'x'))", "subquery"},
		{"SELECT country, count(*) FROM people GROUP BY 1 HAVING jev(people, 'x')", "HAVING"},
		{"SELECT country, count(*) FROM people WHERE jev(people, 'x') GROUP BY 1 HAVING count(*) > 1", "HAVING"},
		{"SELECT row_number() OVER (ORDER BY jev_prob(people, 'x')) FROM people", "window"},
		{"SELECT * FROM people WHERE jev(people, 'x') OR country = 'PT'", "OR jev"},
		{"SELECT * FROM people WHERE NOT (jev(people, 'x') AND a = 1)", "NOT"},
		{"SELECT * FROM people WHERE jev_prob(people, 'x') > 0.5", "not supported in v1"},
		{"SELECT * FROM people WHERE jev(nope, 'x')", `unknown FROM alias "nope"`},
		{"SELECT * FROM people p WHERE jev(people, 'x')", `unknown FROM alias "people"`},
		{"SELECT round(jev_prob(people, 'x'), 2) FROM people", "whole SELECT expression"},
		{"SELECT string_agg(name, ',') FROM people WHERE jev(people, 'x')", "COUNT and SUM/AVG"},
		{"SELECT jev(people) FROM people", "at least"},
		{"SELECT jev(people, name) FROM people", "string literal"},
		{"SELECT jev_choice(people, 'q') FROM people", "ARRAY"},
		{"SELECT jev_score(people, 'q', ARRAY['one']) FROM people", "at least two"},
		{"SELECT * FROM people p JOIN cities c ON jev(p, 'x')", "JOIN"},
		{"SELECT DISTINCT jev_choice(people, 'q', ARRAY['a']) FROM people", "DISTINCT"},
		{"SELECT * FROM people WHERE jev(people, 'x') UNION SELECT * FROM people", "UNION"},
		{"SELECT * FROM people WHERE jev(people, 'x') LIMIT (SELECT 1)", "LIMIT"},
		{"SELECT name FROM people WHERE jev(people, 'x') GROUP BY country", "GROUP BY"},
		{"SELECT max(jev_prob(people, 'x')) FROM people", "aggregating over jev_*"},
	}
	for _, tc := range tests {
		st, err := ParseOne(tc.sql)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.sql, err)
		}
		_, err = Analyze(st)
		if err == nil {
			t.Errorf("%s: expected error containing %q, got nil", tc.sql, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not contain %q", tc.sql, err.Error(), tc.want)
		}
	}
}
