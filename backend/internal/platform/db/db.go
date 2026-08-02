// Package db opens the embedded SQLite database, runs migrations, and provides
// the transaction backbone every mutation flows through (WithTx). One DB file,
// WAL journaling, single writer.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (registers "sqlite")
)

// Open opens the SQLite database at path with the service's standard pragmas and
// verifies connectivity. Pragmas are set via the DSN so they apply to every
// pooled connection; the pool is capped at a single connection to keep SQLite's
// single-writer model simple and free of lock contention at this scale.
func Open(path string) (*sql.DB, error) {
	// _txlock=immediate makes every BeginTx issue BEGIN IMMEDIATE, taking the write
	// lock up front. Every WithTx transaction is a mutation, and several do a
	// read-then-write (e.g. crash group upsert); acquiring the write lock at BEGIN
	// serializes those atomically. Belt-and-suspenders alongside the single-connection
	// pool below — it keeps the invariant if MaxOpenConns ever grows.
	dsn := "file:" + url.PathEscape(path) +
		"?_txlock=immediate" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	sqldb.SetMaxOpenConns(1)
	if err := sqldb.PingContext(context.Background()); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	return sqldb, nil
}

// Ping checks database connectivity (used by the readiness probe).
func Ping(ctx context.Context, sqldb *sql.DB) error { return sqldb.PingContext(ctx) }
