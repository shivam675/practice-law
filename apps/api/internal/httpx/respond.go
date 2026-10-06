// Package httpx holds transport-level helpers: JSON responses, a single error
// shape, request identity and the middleware stack.
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Error is the single error shape the API returns. Handlers return it; the
// writer decides the status code. Clients switch on Code, never on Message.
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func Err(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func (e *Error) WithFields(f map[string]any) *Error {
	e.Fields = f
	return e
}

// Common errors. Messages stay deliberately vague where leaking detail would
// help an attacker enumerate accounts or resources.
var (
	ErrUnauthorized = func() *Error { return Err(http.StatusUnauthorized, "unauthorized", "Authentication required.") }
	ErrForbidden    = func() *Error { return Err(http.StatusForbidden, "forbidden", "You do not have permission to do that.") }
	ErrNotFound     = func() *Error { return Err(http.StatusNotFound, "not_found", "Not found.") }
	ErrConflict     = func(m string) *Error { return Err(http.StatusConflict, "conflict", m) }
	ErrBadRequest   = func(m string) *Error { return Err(http.StatusBadRequest, "bad_request", m) }
	ErrRateLimited  = func() *Error { return Err(http.StatusTooManyRequests, "rate_limited", "Too many requests.") }
	ErrInternal     = func() *Error { return Err(http.StatusInternalServerError, "internal", "Something went wrong.") }
)

// JSON writes v with the given status. A failed encode is logged, never
// swallowed: a truncated body with a 200 is worse than a loud error.
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil || status == http.StatusNoContent {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		LoggerFrom(r.Context()).Error("write json response", "error", err)
	}
}

// Fail writes err as the standard error shape. Non-Error values become a
// generic 500 and the detail goes to the log, never to the client.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		LoggerFrom(r.Context()).Error("unhandled error", "error", err)
		apiErr = ErrInternal()
	}
	if apiErr.Status >= 500 {
		LoggerFrom(r.Context()).Error("server error", "code", apiErr.Code, "error", err)
	}
	JSON(w, r, apiErr.Status, map[string]any{"error": apiErr})
}

// DecodeJSON reads a JSON body with a size cap and rejects unknown fields, so
// a typo in a client payload fails loudly instead of silently doing nothing.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	const maxBody = 1 << 20 // 1 MiB; uploads use a different path
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrBadRequest("Request body is not valid JSON or contains unknown fields.")
	}
	if dec.More() {
		return ErrBadRequest("Request body must contain a single JSON object.")
	}
	return nil
}
