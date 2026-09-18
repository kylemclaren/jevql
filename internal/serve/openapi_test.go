package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/wire"
)

func jsonFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

func TestOpenAPIDocument(t *testing.T) {
	var doc struct {
		OpenAPI    string                    `json:"openapi"`
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]struct{ Properties map[string]any }
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPI, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi version = %q", doc.OpenAPI)
	}
	for _, r := range Routes {
		ops, ok := doc.Paths[r.Path]
		if !ok {
			t.Errorf("route %s %s missing from paths", r.Method, r.Path)
			continue
		}
		if _, ok := ops[strings.ToLower(r.Method)]; !ok {
			t.Errorf("route %s %s: method not documented", r.Method, r.Path)
		}
	}
	// Every JSON field of the Go types must be a documented property, and vice versa.
	types := map[string]reflect.Type{
		"QueryRequest": reflect.TypeOf(wire.QueryRequest{}),
		"QueryResult":  reflect.TypeOf(wire.QueryResult{}),
		"Stats":        reflect.TypeOf(wire.Stats{}),
		"Explain":      reflect.TypeOf(wire.Explain{}),
		"ErrorBody":    reflect.TypeOf(wire.ErrorBody{}),
		"JudgeRequest": reflect.TypeOf(exec.JudgeRequest{}),
		"JudgeAnswer":  reflect.TypeOf(exec.JudgeAnswer{}),
		"JudgeResult":  reflect.TypeOf(exec.JudgeResult{}),
		"TableInfo":    reflect.TypeOf(TableInfo{}),
		"ColumnInfo":   reflect.TypeOf(ColumnInfo{}),
		"IndexInfo":    reflect.TypeOf(IndexInfo{}),
		"TableDetail":  reflect.TypeOf(TableDetail{}),
		"CacheInfo":    reflect.TypeOf(CacheInfo{}),
		"CacheCleared": reflect.TypeOf(CacheCleared{}),
	}
	for name, typ := range types {
		schema, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("component %s missing", name)
			continue
		}
		want := jsonFields(typ)
		for f := range want {
			if _, ok := schema.Properties[f]; !ok {
				t.Errorf("component %s: Go field %q not documented", name, f)
			}
		}
		for f := range schema.Properties {
			if !want[f] {
				t.Errorf("component %s: documented property %q has no Go field", name, f)
			}
		}
	}
}

func TestOpenAPIServedWithoutAuth(t *testing.T) {
	s := &Server{Token: "secret"}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("openapi: %d %s", rr.Code, rr.Header().Get("Content-Type"))
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil || doc["openapi"] == nil {
		t.Fatalf("bad document: %v", err)
	}
}
