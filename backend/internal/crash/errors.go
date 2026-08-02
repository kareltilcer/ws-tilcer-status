package crash

import "errors"

// errInvalidCursor is returned by the store when a pagination cursor is malformed
// (mapped to 422 by handlers).
var errInvalidCursor = errors.New("crash: invalid cursor")
