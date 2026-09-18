package cache

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cache.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("cache file mode = %v (err %v), want 0600", fi.Mode().Perm(), err)
	}
	ctx := context.Background()
	k1 := Key("m", "noul", "q", nil, []byte(`{"a":1}`))
	k2 := Key("m", "noul", "q", nil, []byte(`{"a":2}`))
	if k1 == k2 || len(k1) != 64 {
		t.Errorf("keys: %s %s", k1, k2)
	}
	if k1 == Key("m2", "noul", "q", nil, []byte(`{"a":1}`)) {
		t.Error("model must be part of the key")
	}
	if k1 == Key("m", "noul", "q", []string{"x"}, []byte(`{"a":1}`)) {
		t.Error("options must be part of the key")
	}
	if err := s.PutMany(ctx, map[string]json.RawMessage{k1: json.RawMessage(`{"type":"noul","noul":0.5}`)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMany(ctx, []string{k1, k2})
	if err != nil || len(got) != 1 || string(got[k1]) != `{"type":"noul","noul":0.5}` {
		t.Errorf("GetMany = %v %v", got, err)
	}
	v, ok, err := s.Get(ctx, k2)
	if err != nil || ok || v != nil {
		t.Errorf("Get miss = %v %v %v", v, ok, err)
	}
	n, err := s.Count(ctx)
	if err != nil || n != 1 {
		t.Errorf("Count = %d %v", n, err)
	}
	if n, err := s.Clear(ctx); err != nil || n != 1 {
		t.Errorf("Clear = %d %v", n, err)
	}
	if n, _ := s.Count(ctx); n != 0 {
		t.Errorf("Count after clear = %d", n)
	}
	// A nil store is a no-op.
	var nilStore *Store
	if _, err := nilStore.GetMany(ctx, []string{k1}); err != nil {
		t.Error(err)
	}
	if err := nilStore.PutMany(ctx, map[string]json.RawMessage{k1: nil}); err != nil {
		t.Error(err)
	}
}
