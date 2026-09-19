package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgdb is a single Postgres connection that comes back after the server, a
// pooler or an idle timeout drops it. A shared node can sit idle for hours; the
// first request after that used to fail with "conn closed" until a restart.
type pgdb struct {
	cfg       *pgx.ConnConfig
	mu        sync.Mutex
	conn      *pgx.Conn
	lastUse   time.Time
	idleCheck time.Duration                    // ping before reuse after this much idle time
	warn      func(format string, args ...any) // may be nil
}

// idleCheck is short enough that a request after a long idle stretch never
// hits a dead socket, and long enough that a busy node never pays for pings.
const defaultIdleCheck = 5 * time.Second

func newPGDB(ctx context.Context, cfg *pgx.ConnConfig, warn func(string, ...any)) (*pgdb, error) {
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pgdb{cfg: cfg, conn: conn, lastUse: time.Now(), idleCheck: defaultIdleCheck, warn: warn}, nil
}

// get returns a live connection, dialing a new one if the old one is gone.
func (d *pgdb) get(ctx context.Context) (*pgx.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil && !d.conn.IsClosed() {
		if time.Since(d.lastUse) < d.idleCheck {
			d.lastUse = time.Now()
			return d.conn, nil
		}
		// idle for a while: a pooler or the server may have dropped the socket
		// without pgx noticing; find out with one round trip instead of a failed query
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := d.conn.Ping(pctx)
		cancel()
		if err == nil {
			d.lastUse = time.Now()
			return d.conn, nil
		}
		_ = d.conn.Close(context.Background())
	}
	d.conn = nil
	conn, err := pgx.ConnectConfig(ctx, d.cfg)
	if err != nil {
		return nil, err
	}
	if d.warn != nil {
		d.warn("reconnected to %s", d.cfg.Database)
	}
	d.conn = conn
	d.lastUse = time.Now()
	return conn, nil
}

// drop forgets the current connection so the next call dials again.
func (d *pgdb) drop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		_ = d.conn.Close(context.Background())
		d.conn = nil
	}
}

// dead reports errors that mean the connection itself is gone, as opposed to
// the statement being wrong.
func dead(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "57P01" // admin_shutdown: terminated by the server
	}
	if errors.Is(err, net.ErrClosed) || pgconn.SafeToRetry(err) {
		return true
	}
	msg := err.Error()
	for _, s := range []string{"conn closed", "connection reset", "broken pipe", "unexpected EOF", "conn busy"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Query runs one statement, reconnecting once if the connection is dead.
func (d *pgdb) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	conn, err := d.get(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil && dead(err) {
		d.drop()
		if conn, err = d.get(ctx); err != nil {
			return nil, err
		}
		rows, err = conn.Query(ctx, sql, args...)
	}
	if err != nil {
		return nil, err
	}
	return &watchRows{Rows: rows, db: d}, nil
}

// watchRows forgets the connection if iteration ends with a dead-link error,
// so the next call dials again instead of failing the same way.
type watchRows struct {
	pgx.Rows
	db *pgdb
}

func (w *watchRows) Err() error {
	err := w.Rows.Err()
	if dead(err) {
		w.db.drop()
	}
	return err
}

// QueryRow is Query for a single row; a dead connection is replaced first.
func (d *pgdb) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	conn, err := d.get(ctx)
	if err != nil {
		return errRow{err}
	}
	return conn.QueryRow(ctx, sql, args...)
}

// Close closes the current connection for good.
func (d *pgdb) Close(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil
	}
	err := d.conn.Close(ctx)
	d.conn = nil
	return err
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }
