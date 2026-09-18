package typesafe

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestBatchPacking(t *testing.T) {
	c := New("http://x", "k", "m", 3, 2)
	qa := Question{Kind: Noul, Text: "a"}
	qb := Question{Kind: Choice, Text: "b", Options: []string{"x", "y"}}
	var items []*Item
	for i := 0; i < 7; i++ {
		items = append(items, &Item{Q: qa, Row: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))})
	}
	for i := 0; i < 2; i++ {
		items = append(items, &Item{Q: qb, Row: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))})
	}
	b := c.Batches(items)
	if len(b) != 4 { // 3+3+1 for a, 2 for b
		t.Fatalf("batches = %d", len(b))
	}
	sizes := []int{3, 3, 1, 2}
	for i, s := range sizes {
		if len(b[i].items) != s {
			t.Errorf("batch %d size %d want %d", i, len(b[i].items), s)
		}
	}
	if b[3].q.Kind != Choice {
		t.Errorf("last batch should be the choice question")
	}
}
