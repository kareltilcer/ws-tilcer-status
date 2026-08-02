// Package idgen mints UUIDv7 identifiers. v7 ids are time-ordered, so they sort
// chronologically. Used for session ids and request ids.
package idgen

import "github.com/google/uuid"

// New returns a new UUIDv7 as a canonical string. It panics only if the system
// entropy source fails, which is not a recoverable condition.
func New() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic("idgen: cannot generate UUIDv7: " + err.Error())
	}
	return id.String()
}
