package serve

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/wire"
)

//go:embed openapi.json
var openAPI []byte

// Routes lists every route the handler serves; the OpenAPI test checks
// each one is documented.
var Routes = []struct{ Method, Path string }{
	{"GET", "/v1/health"},
	{"POST", "/v1/query"},
	{"POST", "/v1/explain"},
	{"POST", "/v1/judge"},
	{"GET", "/v1/cache"},
	{"DELETE", "/v1/cache"},
	{"GET", "/v1/schema/tables"},
	{"GET", "/v1/schema/tables/{name}"},
	{"GET", "/openapi.json"},
}

// TableInfo is one entry of GET /v1/schema/tables.
type TableInfo struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

// ColumnInfo is one column of a described table.
type ColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default"`
}

// IndexInfo is one index of a described table.
type IndexInfo struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// TableDetail is the response of GET /v1/schema/tables/{name}.
type TableDetail struct {
	Schema  string       `json:"schema"`
	Name    string       `json:"name"`
	Kind    string       `json:"kind"`
	Columns []ColumnInfo `json:"columns"`
	Indexes []IndexInfo  `json:"indexes"`
}

// CacheInfo is the response of GET /v1/cache.
type CacheInfo struct {
	Entries int64  `json:"entries"`
	Path    string `json:"path,omitempty"`
}

// CacheCleared is the response of DELETE /v1/cache.
type CacheCleared struct {
	Deleted int64 `json:"deleted"`
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, wire.ErrorBody{Error: "missing or invalid bearer token", Code: "auth"})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: err.Error(), Code: "sql"})
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: "invalid JSON body: " + err.Error(), Code: "sql"})
		return false
	}
	return true
}

func (s *Server) writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	eb, status := wire.ErrorFor(err)
	writeJSON(w, status, eb)
}

// executor returns a per-request copy with option overrides applied.
func (s *Server) executor(threshold *float64, maxRows *int, explain bool) *exec.Executor {
	ex := *s.Exec
	if threshold != nil {
		ex.Opts.Threshold = *threshold
	}
	if maxRows != nil {
		ex.Opts.MaxRows = *maxRows
	}
	ex.Opts.Explain = explain
	return &ex
}

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "GET only", Code: "internal"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(openAPI)
}

func (s *Server) explain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "POST only", Code: "internal"})
		return
	}
	if !s.authorized(r) {
		s.unauthorized(w)
		return
	}
	var req wire.QueryRequest
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: "sql is required", Code: "sql"})
		return
	}
	s.mu.Lock()
	res, err := s.executor(req.Threshold, req.MaxRows, true).Run(r.Context(), req.SQL)
	s.mu.Unlock()
	if err != nil {
		s.writeErr(w, err)
		return
	}
	doc := wire.FromResult(res)
	if doc.Explain == nil {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: "statement has no jev_* calls, nothing to explain", Code: "sql"})
		return
	}
	writeJSON(w, http.StatusOK, doc.Explain)
}

func (s *Server) judge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "POST only", Code: "internal"})
		return
	}
	if !s.authorized(r) {
		s.unauthorized(w)
		return
	}
	var req wire.JudgeRequest
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: "question is required", Code: "sql"})
		return
	}
	s.mu.Lock()
	res, err := s.executor(nil, nil, false).JudgeRows(r.Context(), req)
	s.mu.Unlock()
	if s.Log != nil {
		s.Log("judge %q kind=%s rows=%d err=%v", req.Question, req.Kind, len(req.Rows), err)
	}
	if err != nil {
		var be *exec.BudgetError
		if errors.As(err, &be) {
			writeJSON(w, http.StatusPaymentRequired, wire.ErrorBody{Error: err.Error(), Code: "budget"})
			return
		}
		eb, status := wire.ErrorFor(err)
		if status == http.StatusBadRequest {
			eb.Code = "sql"
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		writeJSON(w, status, eb)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cache(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		s.unauthorized(w)
		return
	}
	if !s.CacheAdmin {
		writeJSON(w, http.StatusForbidden, wire.ErrorBody{Error: "cache admin is disabled on this server", Code: "auth"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		n, err := s.Exec.Cache.Count(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, wire.ErrorBody{Error: err.Error(), Code: "internal"})
			return
		}
		info := CacheInfo{Entries: n}
		if s.Exec.Cache != nil {
			info.Path = s.Exec.Cache.Path
		}
		writeJSON(w, http.StatusOK, info)
	case http.MethodDelete:
		n, err := s.Exec.Cache.Clear(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, wire.ErrorBody{Error: err.Error(), Code: "internal"})
			return
		}
		writeJSON(w, http.StatusOK, CacheCleared{Deleted: n})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "GET or DELETE only", Code: "internal"})
	}
}

const tablesSQL = `SELECT n.nspname, c.relname,
  CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'partitioned table' WHEN 'v' THEN 'view'
                 WHEN 'm' THEN 'materialized view' WHEN 'f' THEN 'foreign table' END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','p','v','m','f') AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
ORDER BY 1, 2`

func (s *Server) schemaTables(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "GET only", Code: "internal"})
		return
	}
	if !s.authorized(r) {
		s.unauthorized(w)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.Exec.DB.Query(r.Context(), tablesSQL)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	defer rows.Close()
	out := []TableInfo{}
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Schema, &t.Name, &t.Kind); err != nil {
			s.writeErr(w, err)
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) schemaTable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "GET only", Code: "internal"})
		return
	}
	if !s.authorized(r) {
		s.unauthorized(w)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/v1/schema/tables/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: "table name is required", Code: "sql"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := describeTable(r.Context(), s.Exec.DB, name)
	if err != nil {
		if errors.Is(err, errNoTable) {
			writeJSON(w, http.StatusNotFound, wire.ErrorBody{Error: fmt.Sprintf("relation %q does not exist", name), Code: "sql"})
			return
		}
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

var errNoTable = errors.New("no such table")

func describeTable(ctx context.Context, db exec.Querier, name string) (*TableDetail, error) {
	rows, err := db.Query(ctx, `SELECT c.oid, n.nspname, c.relname,
  CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'partitioned table' WHEN 'v' THEN 'view'
                 WHEN 'm' THEN 'materialized view' WHEN 'f' THEN 'foreign table' WHEN 'S' THEN 'sequence' ELSE c.relkind::text END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = to_regclass($1)`, name)
	if err != nil {
		return nil, err
	}
	d := &TableDetail{Columns: []ColumnInfo{}, Indexes: []IndexInfo{}}
	var oid uint32
	found := false
	for rows.Next() {
		if err := rows.Scan(&oid, &d.Schema, &d.Name, &d.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		found = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, errNoTable
	}
	cols, err := db.Query(ctx, `SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
  COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '')
FROM pg_attribute a LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, oid)
	if err != nil {
		return nil, err
	}
	for cols.Next() {
		var c ColumnInfo
		if err := cols.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default); err != nil {
			cols.Close()
			return nil, err
		}
		d.Columns = append(d.Columns, c)
	}
	cols.Close()
	if err := cols.Err(); err != nil {
		return nil, err
	}
	idx, err := db.Query(ctx, `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2 ORDER BY indexname`, d.Schema, d.Name)
	if err != nil {
		return nil, err
	}
	for idx.Next() {
		var i IndexInfo
		if err := idx.Scan(&i.Name, &i.Definition); err != nil {
			idx.Close()
			return nil, err
		}
		d.Indexes = append(d.Indexes, i)
	}
	idx.Close()
	return d, idx.Err()
}

var _ = pgx.ErrNoRows
