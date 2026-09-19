package app

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestPGDBReconnects drops the connection underneath the client and expects
// the next query to succeed on a fresh connection.
func TestPGDBReconnects(t *testing.T) {
	url := os.Getenv("PGTEST_URL")
	if url == "" {
		t.Skip("PGTEST_URL not set")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	var warned int
	db, err := newPGDB(ctx, cfg, func(string, ...any) { warned++ })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	db.idleCheck = 0 // every call counts as "idle": always ping first

	var pid int
	if err := db.QueryRow(ctx, "select pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// drop the socket underneath pgx, as an idle timeout or a pooler restart would;
	// pgx only finds out on its next read
	if err := db.conn.PgConn().Conn().Close(); err != nil {
		t.Fatal(err)
	}
	// the old connection is dead; Query must come back on a new one
	rows, err := db.Query(ctx, "select pg_backend_pid()")
	if err != nil {
		t.Fatalf("query after kill: %v", err)
	}
	var pid2 int
	for rows.Next() {
		if err := rows.Scan(&pid2); err != nil {
			t.Fatal(err)
		}
	}
	rows.Close()
	if pid2 == 0 || pid2 == pid {
		t.Fatalf("expected a new backend, got %d (old %d)", pid2, pid)
	}
	if warned != 1 {
		t.Fatalf("expected one reconnect notice, got %d", warned)
	}
}
