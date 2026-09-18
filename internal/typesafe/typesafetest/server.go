// Package typesafetest is a mock TypeSafe server for tests.
package typesafetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mclaren/jevpsql/internal/typesafe"
)

// Server answers every question with a fixed shape and counts requests.
func Server(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":"unauthorized"}`, 401)
			return
		}
		var req typesafe.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		rows, _ := req.State["rows"].([]any)
		answers := map[string]any{}
		for id, q := range req.Questions {
			var idx int
			fmt.Sscanf(id, "row_%d", &idx)
			row := rows[idx].(map[string]any)["row"].(map[string]any)
			name, _ := row["name"].(string)
			switch q.Type {
			case typesafe.Noul:
				p := 0.1
				if len(name) > 0 && name[0] >= 'A' && name[0] <= 'M' {
					p = 0.9 // names starting A-M "pass"
				}
				answers[id] = map[string]any{"type": "noul", "noul": p}
			case typesafe.Choice:
				opts := q.Criteria.(map[string]any)
				pick := ""
				for k := range opts {
					if pick == "" || k < pick {
						pick = k
					}
				}
				if len(name)%2 == 1 {
					for k := range opts {
						if k > pick {
							pick = k
						}
					}
				}
				answers[id] = map[string]any{"type": "choice", "choice": pick, "confidence": 0.8, "probabilities": map[string]float64{pick: 0.8}}
			case typesafe.Score:
				answers[id] = map[string]any{"type": "score", "score": 1.5, "confidence": 0.7, "legend": map[string]string{"0": "lo"}, "probabilities": map[string]float64{"1": 0.5, "2": 0.5}}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"model": "jev-test", "answers": answers,
			"usage": map[string]int{"input_tokens": 100 * len(req.Questions), "output_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}
