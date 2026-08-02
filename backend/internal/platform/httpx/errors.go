package httpx

import (
	"errors"
	"net/http"
)

// Error is the shared error envelope from openapi.yaml (schema Error).
type Error struct {
	Err    string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// APIError is a domain error carrying an HTTP status and the client-facing
// envelope. Handlers return these (often via the helpers below) and a single
// place writes them.
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

// Constructors for the common statuses used across the API.

func ErrBadRequest(detail string) *APIError {
	return &APIError{http.StatusBadRequest, "bad_request", detail}
}
func ErrUnauthorized(detail string) *APIError {
	return &APIError{http.StatusUnauthorized, "unauthorized", detail}
}
func ErrForbidden(detail string) *APIError {
	return &APIError{http.StatusForbidden, "forbidden", detail}
}
func ErrNotFound(detail string) *APIError { return &APIError{http.StatusNotFound, "not_found", detail} }
func ErrConflict(detail string) *APIError { return &APIError{http.StatusConflict, "conflict", detail} }
func ErrUnprocessable(detail string) *APIError {
	return &APIError{http.StatusUnprocessableEntity, "unprocessable", detail}
}
func ErrInternal(detail string) *APIError {
	return &APIError{http.StatusInternalServerError, "internal", detail}
}

// WriteError writes err as the shared Error envelope. Non-APIError values are
// treated as 500s with a generic message (details stay server-side).
func WriteError(w http.ResponseWriter, err error) {
	var ae *APIError
	if errors.As(err, &ae) {
		JSON(w, ae.Status, Error{Err: ae.Code, Detail: ae.Detail})
		return
	}
	JSON(w, http.StatusInternalServerError, Error{Err: "internal"})
}
