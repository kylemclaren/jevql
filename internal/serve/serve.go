// Package serve is the local HTTP transport used by the SDKs: a tiny JSON
// API over the same executor the CLI uses. It is meant for localhost.
package serve

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/wire"
)

// Runner executes one statement. *exec.Executor satisfies it.
type Runner interface {
	Run(ctx context.Context, sql string) (*exec.Result, error)
}

// Server is the HTTP handler.
type Server struct {
	Exec    *exec.Executor
	Token   string
	Version string
	Model   string
	Log     func(format string, args ...any)
	// CacheAdmin enables GET/DELETE /v1/cache. Off by default when the
	// server is reachable beyond loopback.
	CacheAdmin  bool
	MCP         http.Handler // optional: mounted at /mcp behind the bearer check
	CORSOrigins []string     // browser origins allowed to call the API ("*" for any)

	mu sync.Mutex // pgx connections are not safe for concurrent use
}

// Handler returns the routed http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if len(s.CORSOrigins) > 0 {
		return s.cors(s.routes(mux))
	}
	return s.routes(mux)
}

// cors answers preflight requests and stamps allowed origins on responses.
func (s *Server) cors(h http.Handler) http.Handler {
	allowed := func(origin string) string {
		for _, o := range s.CORSOrigins {
			if o == "*" || strings.EqualFold(o, origin) {
				return origin
			}
		}
		return ""
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if ok := allowed(origin); ok != "" {
				w.Header().Set("Access-Control-Allow-Origin", ok)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) routes(mux *http.ServeMux) http.Handler {
	mux.HandleFunc("/v1/health", s.health)
	mux.HandleFunc("/v1/query", s.query)
	mux.HandleFunc("/v1/explain", s.explain)
	mux.HandleFunc("/v1/judge", s.judge)
	mux.HandleFunc("/v1/cache", s.cache)
	mux.HandleFunc("/v1/schema/tables", s.schemaTables)
	mux.HandleFunc("/v1/schema/tables/", s.schemaTable)
	mux.HandleFunc("/openapi.json", s.openapi)
	if s.MCP != nil {
		mux.Handle("/mcp", s.requireAuth(s.MCP))
		mux.Handle("/mcp/", s.requireAuth(s.MCP))
	}
	return mux
}

// requireAuth wraps a handler with the bearer check.
func (s *Server) requireAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, wire.ErrorBody{Error: "missing or invalid bearer token", Code: "auth"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(s.Token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "GET only", Code: "internal"})
		return
	}
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, wire.ErrorBody{Error: "missing or invalid bearer token", Code: "auth"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.Version, "model": s.Model})
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, wire.ErrorBody{Error: "POST only", Code: "internal"})
		return
	}
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, wire.ErrorBody{Error: "missing or invalid bearer token", Code: "auth"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: err.Error(), Code: "sql"})
		return
	}
	var req wire.QueryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: "invalid JSON body: " + err.Error(), Code: "sql"})
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		writeJSON(w, http.StatusBadRequest, wire.ErrorBody{Error: "sql is required", Code: "sql"})
		return
	}
	start := time.Now()
	res, err := s.run(r.Context(), req)
	if s.Log != nil {
		s.Log("query %s in %s err=%v", strings.SplitN(strings.TrimSpace(req.SQL), "\n", 2)[0], time.Since(start).Round(time.Millisecond), err)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		eb, status := wire.ErrorFor(err)
		writeJSON(w, status, eb)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// run executes on a per-request executor copy, serialised on the connection.
func (s *Server) run(ctx context.Context, req wire.QueryRequest) (*wire.QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.executor(req.Threshold, req.MaxRows, req.Explain).Run(ctx, req.SQL)
	if err != nil {
		return nil, err
	}
	return wire.FromResult(res), nil
}

// ListenAndServe runs until ctx is cancelled. addr may end in ":0" to pick
// a free port; onReady (optional) receives the bound address before serving.
func (s *Server) ListenAndServe(ctx context.Context, addr string, onReady func(bound string)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if onReady != nil {
		onReady(ln.Addr().String())
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// WatchParent cancels the returned context when the process pid disappears.
// SDKs pass their own pid so an orphaned engine does not outlive them.
func WatchParent(ctx context.Context, pid int) context.Context {
	if pid <= 0 {
		return ctx
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !processAlive(pid) {
					cancel()
					return
				}
			}
		}
	}()
	return ctx
}
