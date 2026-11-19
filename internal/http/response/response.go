// Package response writes the standard JSON envelopes (plan §15.1).
//
//	success: { "data": …, "meta": { … } }
//	error:   { "error": { "code", "message", "fields", "request_id" } }
package response

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Stable machine-readable error codes.
const (
	CodeBadRequest        = "bad_request"
	CodeValidation        = "validation_failed"
	CodeUnauthenticated   = "unauthenticated"
	CodeForbidden         = "forbidden"
	CodeNotFound          = "not_found"
	CodeConflict          = "conflict"
	CodeRateLimited       = "rate_limited"
	CodeInternal          = "internal_error"
	CodeCSRF              = "csrf_failed"
	CodeInvalidCode       = "invalid_or_expired_code"
	CodeInvalidCreds      = "invalid_credentials"
	CodeReauthRequired    = "reauthentication_required"
	CodeWeakPIN           = "weak_pin"
	CodeChannelDisabled   = "channel_unavailable"
	CodeMaintenance       = "maintenance"
	CodeUnsupportedMedia  = "unsupported_media_type"
	CodeParentUnpublished = "parent_not_published"
	CodeHasDependents     = "has_dependents"
	CodeMerged            = "location_merged"
)

// Conflict builds a 409 with a specific code.
func Conflict(code, message string) *Error { return New(http.StatusConflict, code, message) }

// Error is a typed API error. Message is a localizable message ID resolved by the handler layer.
type Error struct {
	Status     int               `json:"-"`
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	Fields     map[string]string `json:"fields,omitempty"`
	RetryAfter time.Duration     `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds an error.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Validation builds a 422 error with per-field codes.
func Validation(fields map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: CodeValidation, Message: "One or more fields are invalid.", Fields: fields}
}

// RateLimited builds a 429 error.
func RateLimited(retry time.Duration) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: CodeRateLimited, Message: "Too many requests. Please try again later.", RetryAfter: retry}
}

// Common errors.
var (
	ErrUnauthenticated = New(http.StatusUnauthorized, CodeUnauthenticated, "Authentication is required.")
	ErrForbidden       = New(http.StatusForbidden, CodeForbidden, "You do not have permission to do this.")
	ErrNotFound        = New(http.StatusNotFound, CodeNotFound, "The requested resource was not found.")
	ErrBadJSON         = New(http.StatusBadRequest, CodeBadRequest, "The request body is not valid JSON.")
	ErrCSRF            = New(http.StatusForbidden, CodeCSRF, "The request could not be verified. Refresh and try again.")
)

type envelope struct {
	Data  any `json:"data"`
	Meta  any `json:"meta,omitempty"`
	Error any `json:"error,omitempty"`
}

// JSON writes a success envelope.
func JSON(w http.ResponseWriter, status int, data any, meta any) {
	write(w, status, envelope{Data: data, Meta: meta})
}

// OK writes 200 with data.
func OK(w http.ResponseWriter, data any) { JSON(w, http.StatusOK, data, nil) }

// NoContent writes 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

type errBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id"`
}

// Fail writes an error envelope. Unknown errors become 500 and are logged, never exposed.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.ErrorContext(r.Context(), "unhandled error", "error", err, "path", r.URL.Path)
		e = New(http.StatusInternalServerError, CodeInternal, "Something went wrong. Please try again.")
	}
	if e.RetryAfter > 0 {
		secs := int(e.RetryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	write(w, e.Status, map[string]any{"error": errBody{
		Code: e.Code, Message: e.Message, Fields: e.Fields, RequestID: reqctx.RequestID(r.Context()),
	}})
}

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// MaxBody is the default request body limit for JSON endpoints.
const MaxBody = 64 << 10

// Decode parses a JSON body strictly (unknown fields rejected, size bounded).
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !isJSON(ct) {
		return New(http.StatusUnsupportedMediaType, CodeUnsupportedMedia, "Content-Type must be application/json.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrBadJSON
	}
	return nil
}

func isJSON(ct string) bool {
	return len(ct) >= 16 && ct[:16] == "application/json"
}
