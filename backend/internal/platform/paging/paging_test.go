package paging

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// TestPaginateInvalidCursor verifies a malformed cursor is rejected with
// ErrInvalidCursor before any query runs (so the nil Querier is never touched).
func TestPaginateInvalidCursor(t *testing.T) {
	_, _, err := Paginate(context.Background(), nil, 10, "!!!not base64!!!", Query[int]{
		Columns:   "id",
		From:      "t",
		CursorCol: "ts",
		IDCol:     "id",
		Scan:      func(*sql.Rows) (int, error) { return 0, nil },
		Key:       func(int) (string, int64) { return "", 0 },
	})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("malformed cursor should yield ErrInvalidCursor, got %v", err)
	}
}
