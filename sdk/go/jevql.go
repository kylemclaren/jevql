// Package jevql is the Go SDK: run jev-enabled SQL against a vanilla
// Postgres from your own program, in process, using the same parser,
// rewriter, cache and TypeSafe client as the jevql CLI.
//
//	client, err := jevql.New(ctx, jevql.Options{DatabaseURL: os.Getenv("DATABASE_URL")})
//	res, err := client.Query(ctx, "SELECT name FROM people WHERE jev(people, 'could work from home')")
//	for _, row := range res.Rows { fmt.Println(row[0]) }
package jevql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/kylemclaren/jevql/internal/cache"
	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/typesafe"
	"github.com/kylemclaren/jevql/internal/wire"
)

// Options configure a Client. Zero values fall back to the environment
// (DATABASE_URL, TYPESAFE_API_KEY, TYPESAFE_API_URL, JEV_THRESHOLD) and then
// to the CLI defaults.
type Options struct {
	DatabaseURL string    // postgres:// URL; or pass Conn
	Conn        *pgx.Conn // an existing connection (Close will not close it)
	APIKey      string    //
	APIURL      string    // default https://api.typesafe.ai/v1/systemone
	Model       string    // default jev-latest
	Threshold   float64   // default 0.5
	BatchSize   int       // default 40
	Concurrency int       // default 6
	MaxRows     int       // default 2500; 0 disables the guard
	MaxRowsSet  bool      // set true to force MaxRows == 0 (no guard)
	MaxChars    int       //
	Columns     []string  // --columns equivalent
	CachePath   string    // default ~/.cache/jevql/cache.db
	NoCache     bool      //
	Logf        func(string, ...any)
}

// Result is one statement's outcome. Rows hold JSON-friendly values
// (string, float64/int64, bool, nil, time.Time formatted as RFC 3339, ...).
type Result = wire.QueryResult

// Stats are the jev metrics for a statement.
type Stats = wire.Stats

// Explain is the --explain report.
type Explain = wire.Explain

// Error is returned for SQL, budget and API failures with a protocol code.
type Error struct {
	Code string // sql | budget | api
	Err  error
}

func (e *Error) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Client runs statements.
type Client struct {
	mu      sync.Mutex
	conn    *pgx.Conn
	ownConn bool
	cache   *cache.Store
	ex      *exec.Executor
}

// New connects and prepares a client.
func New(ctx context.Context, o Options) (*Client, error) {
	c := &Client{}
	if o.Conn != nil {
		c.conn = o.Conn
	} else {
		url := o.DatabaseURL
		if url == "" {
			url = os.Getenv("DATABASE_URL")
		}
		cfg, err := pgx.ParseConfig(url)
		if err != nil {
			return nil, fmt.Errorf("jevql: %w", err)
		}
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		cfg.RuntimeParams["application_name"] = "jevql-sdk"
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("jevql: connect: %w", err)
		}
		c.conn, c.ownConn = conn, true
	}
	apiKey := o.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("TYPESAFE_API_KEY")
	}
	apiURL := o.APIURL
	if apiURL == "" {
		apiURL = os.Getenv("TYPESAFE_API_URL")
	}
	if apiURL == "" {
		apiURL = "https://api.typesafe.ai/v1/systemone"
	}
	model := o.Model
	if model == "" {
		model = "jev-latest"
	}
	thr := o.Threshold
	if thr == 0 {
		thr = 0.5
		if v := os.Getenv("JEV_THRESHOLD"); v != "" {
			fmt.Sscanf(v, "%g", &thr)
		}
	}
	maxRows := o.MaxRows
	if maxRows == 0 && !o.MaxRowsSet {
		maxRows = 2500
	}
	if !o.NoCache {
		path := o.CachePath
		if path == "" {
			path = cache.DefaultPath()
		}
		st, err := cache.Open(path)
		if err != nil {
			if o.Logf != nil {
				o.Logf("jevql: cache disabled: %v", err)
			}
		} else {
			c.cache = st
		}
	}
	c.ex = &exec.Executor{
		DB:    c.conn,
		TS:    typesafe.New(apiURL, apiKey, model, o.BatchSize, o.Concurrency),
		Cache: c.cache,
		Log:   o.Logf,
		Opts: exec.Options{Threshold: thr, BatchSize: o.BatchSize, Concurrency: o.Concurrency,
			MaxRows: maxRows, MaxChars: o.MaxChars, Columns: o.Columns, Model: model},
	}
	return c, nil
}

// QueryOptions override client settings for one call.
type QueryOptions struct {
	Threshold *float64
	MaxRows   *int
}

// Query runs one statement. Plain SQL passes straight to Postgres.
func (c *Client) Query(ctx context.Context, sql string, opts ...QueryOptions) (*Result, error) {
	return c.run(ctx, sql, false, opts...)
}

// Explain reports the collect SQL and cost estimate without calling TypeSafe.
func (c *Client) Explain(ctx context.Context, sql string) (*Explain, error) {
	r, err := c.run(ctx, sql, true)
	if err != nil {
		return nil, err
	}
	if r.Explain == nil {
		return nil, errors.New("jevql: statement has no jev_* calls, nothing to explain")
	}
	return r.Explain, nil
}

// QueryMaps returns rows as column-name keyed maps.
func (c *Client) QueryMaps(ctx context.Context, sql string, opts ...QueryOptions) ([]map[string]any, error) {
	r, err := c.Query(ctx, sql, opts...)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(r.Rows))
	for i, row := range r.Rows {
		m := make(map[string]any, len(r.Columns))
		for j, col := range r.Columns {
			m[col] = row[j]
		}
		out[i] = m
	}
	return out, nil
}

func (c *Client) run(ctx context.Context, sql string, explain bool, opts ...QueryOptions) (*Result, error) {
	if strings.TrimSpace(sql) == "" {
		return nil, &Error{Code: "sql", Err: errors.New("empty statement")}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	saved := c.ex.Opts
	defer func() { c.ex.Opts = saved }()
	c.ex.Opts.Explain = explain
	for _, o := range opts {
		if o.Threshold != nil {
			c.ex.Opts.Threshold = *o.Threshold
		}
		if o.MaxRows != nil {
			c.ex.Opts.MaxRows = *o.MaxRows
		}
	}
	res, err := c.ex.Run(ctx, sql)
	if err != nil {
		code, _ := wire.Classify(err)
		return nil, &Error{Code: code, Err: err}
	}
	return wire.FromResult(res), nil
}

// CacheClear drops every cached answer.
func (c *Client) CacheClear(ctx context.Context) (int64, error) {
	return c.cache.Clear(ctx)
}

// Close releases the cache and the connection (if the client opened it).
func (c *Client) Close(ctx context.Context) error {
	var err error
	if c.cache != nil {
		err = c.cache.Close()
	}
	if c.ownConn && c.conn != nil {
		if cerr := c.conn.Close(ctx); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

// JudgeRequest judges rows you already hold; see exec.JudgeRequest.
type JudgeRequest = exec.JudgeRequest

// JudgeResult is the answer set, in input order.
type JudgeResult = exec.JudgeResult

// JudgeAnswer is one row's answer.
type JudgeAnswer = exec.JudgeAnswer

// Judge asks one question about each row without touching the database.
// It shares batching, dedup and the cache with Query.
func (c *Client) Judge(ctx context.Context, req JudgeRequest) (*JudgeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, err := c.ex.JudgeRows(ctx, req)
	if err != nil {
		code, _ := wire.Classify(err)
		return nil, &Error{Code: code, Err: err}
	}
	return res, nil
}
