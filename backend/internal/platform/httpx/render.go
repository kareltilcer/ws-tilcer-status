// Package httpx holds the HTTP layer shared across modules: JSON rendering,
// the shared Error shape, middleware (request id, logging, recovery, auth, role
// gates), and the health probes.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxBodyBytes caps admin request bodies to a sane size for this API. The public
// crash-ingest path enforces its own (larger, configurable) cap before decode.
const maxBodyBytes = 1 << 20 // 1 MiB

// JSON writes v as a JSON response with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// DecodeJSON strictly decodes a JSON request body into dst (unknown fields are
// rejected). Returns a client-facing error suitable for a 422.
func DecodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Reject trailing content after the first JSON value.
	if dec.More() {
		return errors.New("unexpected trailing content in request body")
	}
	return nil
}
