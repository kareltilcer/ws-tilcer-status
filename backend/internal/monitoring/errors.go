package monitoring

import "errors"

// errInvalidCursor is returned when a pagination cursor is malformed (→ 422).
var errInvalidCursor = errors.New("monitoring: invalid cursor")
