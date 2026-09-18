package typesafe_test

import (
	"context"

	"encoding/json"
	"fmt"
	. "github.com/kylemclaren/jevpsql/internal/typesafe"
	"github.com/kylemclaren/jevpsql/internal/typesafe/typesafetest"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBuildRequest(t *testing.T) {
	c := New("http://x", "k", "jev-latest", 40, 6)
	rows := []json.RawMessage{json.RawMessage(`{"name":"Ada"}`), json.RawMessage(`{"name":"Bo"}`)}
	req := c.BuildRequest(Question{Kind: Noul, Text: "wfh"}, rows)
	b, _ := json.Marshal(req)
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["model"] != "jev-latest" {
		t.Errorf("model = %v", m["model"])
	}
	state := m["state"].(map[string]any)
	if state["condition"] != "wfh" || len(state["rows"].([]any)) != 2 {
		t.Errorf("state = %v", state)
	}
	qs := m["questions"].(map[string]any)
	if len(qs) != 2 || qs["row_1"].(map[string]any)["type"] != "noul" {
		t.Errorf("questions = %v", qs)
	}
	req = c.BuildRequest(Question{Kind: Choice, Text: "team?", Options: []string{"a", "b"}}, rows[:1])
	crit := req.Questions["row_0"].Criteria.(map[string]string)
	if crit["a"] != "a" || crit["b"] != "b" {
		t.Errorf("choice criteria = %v", crit)
	}
	req = c.BuildRequest(Question{Kind: Score, Text: "how?", Options: []string{"lo", "hi"}}, rows[:1])
	if lv, ok := req.Questions["row_0"].Criteria.([]string); !ok || len(lv) != 2 {
		t.Errorf("score criteria = %v", req.Questions["row_0"].Criteria)
	}
}

func TestJudgeAgainstMock(t *testing.T) {
	srv, calls := typesafetest.Server(t)
	c := New(srv.URL, "test-key", "jev-test", 4, 3)
	var items []*Item
	for _, n := range []string{"Ada", "Ravi", "Bea", "Zed", "Cal", "Yan", "Dee", "Xi", "Eve"} {
		items = append(items, &Item{Q: Question{Kind: Noul, Text: "x"}, Row: json.RawMessage(fmt.Sprintf(`{"name":%q}`, n))})
	}
	items = append(items, &Item{Q: Question{Kind: Choice, Text: "q", Options: []string{"a", "b"}}, Row: json.RawMessage(`{"name":"Ada"}`)})
	usage, err := c.Judge(context.Background(), items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 4 || usage.Requests != 4 { // 9 noul in batches of 4 = 3, plus 1 choice
		t.Errorf("calls = %d usage = %+v", *calls, usage)
	}
	if usage.InputTokens != 1000 {
		t.Errorf("input tokens = %d", usage.InputTokens)
	}
	for _, it := range items {
		if it.Answer == nil {
			t.Fatalf("missing answer for %s", it.Row)
		}
	}
	if items[0].Answer.P() != 0.9 || items[1].Answer.P() != 0.1 {
		t.Errorf("answers = %v %v", items[0].Answer.P(), items[1].Answer.P())
	}
	if items[9].Answer.Choice == "" || items[9].Answer.Raw == nil {
		t.Errorf("choice answer = %+v", items[9].Answer)
	}
	if usage.USD() <= 0 {
		t.Error("expected non-zero cost")
	}
}

func TestJudgeAuthError(t *testing.T) {
	srv, _ := typesafetest.Server(t)
	c := New(srv.URL, "wrong", "jev-test", 4, 1)
	c.MaxRetries = 0
	_, err := c.Judge(context.Background(), []*Item{{Q: Question{Kind: Noul, Text: "x"}, Row: json.RawMessage(`{"name":"A"}`)}}, nil)
	var ae *APIError
	if err == nil || !asAPIError(err, &ae) || ae.Status != 401 {
		t.Errorf("expected 401 APIError, got %v", err)
	}
}

func asAPIError(err error, target **APIError) bool {
	if e, ok := err.(*APIError); ok {
		*target = e
		return true
	}
	return false
}

func TestRetryOn429(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			http.Error(w, "slow down", 429)
			return
		}
		fmt.Fprint(w, `{"model":"m","answers":{"row_0":{"type":"noul","noul":0.5}},"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "k", "m", 1, 1)
	items := []*Item{{Q: Question{Kind: Noul, Text: "x"}, Row: json.RawMessage(`{}`)}}
	if _, err := c.Judge(context.Background(), items, nil); err != nil {
		t.Fatal(err)
	}
	if n != 3 || items[0].Answer.P() != 0.5 {
		t.Errorf("n=%d answer=%+v", n, items[0].Answer)
	}
}
