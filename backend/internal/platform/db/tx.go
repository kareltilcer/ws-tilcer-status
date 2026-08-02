package db

import (
	"context"
	"database/sql"
)

// WithTx runs fn inside a single transaction, committing on success and rolling
// back on any error or panic. This is the atomicity backbone of the service:
// a mutation and any dependent write (e.g. a color recompute) commit or roll
// back together.
func WithTx(ctx context.Context, sqldb *sql.DB, fn func(*sql.Tx) error) (err error) {
	tx, err := sqldb.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback() // no-op if already committed
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}
