// Package paging provides opaque keyset-pagination cursors shared by the
// monitoring and crash list endpoints. A cursor encodes the last row's
// (timestamp, id) pair so the next page resumes exactly after it; all timestamps
// use a fixed RFC3339 UTC layout so string comparison is a valid total order.
package paging

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const (
	// DefaultLimit is used when the client omits or under-specifies limit.
	DefaultLimit = 50
	// MaxLimit caps a page (openapi: limit 1..200).
	MaxLimit = 200
)

// ClampLimit normalizes a requested page size to [1, MaxLimit], defaulting when
// non-positive.
func ClampLimit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	if n > MaxLimit {
		return MaxLimit
	}
	return n
}

// EncodeCursor packs (ts, id) into an opaque URL-safe token.
func EncodeCursor(ts string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(ts + "\x00" + strconv.FormatInt(id, 10)))
}

// DecodeCursor reverses EncodeCursor. A malformed cursor yields an error (map to 422).
func DecodeCursor(cur string) (ts string, id int64, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return "", 0, errors.New("malformed cursor")
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 {
		return "", 0, errors.New("malformed cursor")
	}
	id, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, errors.New("malformed cursor")
	}
	return parts[0], id, nil
}

// ErrInvalidCursor is returned by Paginate when the caller's cursor is malformed.
// Callers map it to their own 422 sentinel.
var ErrInvalidCursor = errors.New("paging: invalid cursor")

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Query specifies one keyset-paginated read ordered by (CursorCol DESC, IDCol
// DESC). Filters are the non-cursor WHERE conditions (each using ?) with their
// Args; Scan maps one row to T; Key extracts the (timestamp, id) pair used to
// resume after the last row.
type Query[T any] struct {
	Columns   string   // SELECT list
	From      string   // table plus any JOINs
	Filters   []string // non-cursor WHERE conditions, each using ?
	Args      []any    // args for Filters, in order
	CursorCol string   // timestamp column the cursor resumes after
	IDCol     string   // tie-break id column
	Scan      func(*sql.Rows) (T, error)
	Key       func(T) (ts string, id int64)
}

// Paginate runs q with limit/cursor applied: it clamps the limit, appends the
// keyset cursor predicate, selects limit+1 rows to detect a further page, and
// returns at most limit items plus an optional next cursor. A malformed cursor
// yields ErrInvalidCursor.
func Paginate[T any](ctx context.Context, db Querier, limit int, cursor string, q Query[T]) ([]T, *string, error) {
	limit = ClampLimit(limit)
	conds := append([]string(nil), q.Filters...)
	args := append([]any(nil), q.Args...)
	if cursor != "" {
		ts, id, err := DecodeCursor(cursor)
		if err != nil {
			return nil, nil, ErrInvalidCursor
		}
		conds = append(conds, "("+q.CursorCol+" < ? OR ("+q.CursorCol+" = ? AND "+q.IDCol+" < ?))")
		args = append(args, ts, ts, id)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	query := "SELECT " + q.Columns + " FROM " + q.From + where +
		" ORDER BY " + q.CursorCol + " DESC, " + q.IDCol + " DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		item, err := q.Scan(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(items) > limit {
		last := items[limit-1]
		items = items[:limit]
		ts, id := q.Key(last)
		cur := EncodeCursor(ts, id)
		next = &cur
	}
	return items, next, nil
}
