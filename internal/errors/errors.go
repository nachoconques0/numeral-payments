// Package errors defines the application error type shared by every layer and
// its mapping to HTTP status codes.
package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
)

// AppError is the structured error returned by services and rendered by controllers.
type AppError struct {
	Code    int      `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
	err     error
}

func (e *AppError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.err)
	}
	return e.Message
}

// Unwrap exposes the wrapped cause to errors.Is and errors.As.
func (e *AppError) Unwrap() error { return e.err }

// WithDetails attaches human readable details, such as schema violations.
func (e *AppError) WithDetails(details ...string) *AppError {
	e.Details = append(e.Details, details...)
	return e
}

func newAppError(code int, message string, err error) *AppError {
	return &AppError{Code: code, Message: message, err: err}
}

// BadRequest reports a malformed or semantically invalid request.
func BadRequest(message string, err error) *AppError {
	return newAppError(http.StatusBadRequest, message, err)
}

// Unauthorized reports missing or invalid credentials.
func Unauthorized(message string, err error) *AppError {
	return newAppError(http.StatusUnauthorized, message, err)
}

// Conflict reports a request that contradicts what is already stored.
func Conflict(message string, err error) *AppError {
	return newAppError(http.StatusConflict, message, err)
}

// NotFound reports a resource that does not exist.
func NotFound(message string, err error) *AppError {
	return newAppError(http.StatusNotFound, message, err)
}

// MethodNotAllowed reports a known path called with an unsupported method.
func MethodNotAllowed(message string, err error) *AppError {
	return newAppError(http.StatusMethodNotAllowed, message, err)
}

// UnsupportedMediaType reports a body encoding the service cannot read.
func UnsupportedMediaType(message string, err error) *AppError {
	return newAppError(http.StatusUnsupportedMediaType, message, err)
}

// InternalError reports an unexpected failure.
func InternalError(message string, err error) *AppError {
	return newAppError(http.StatusInternalServerError, message, err)
}

// From converts any error into an AppError, defaulting to 500 so an unmapped
// failure does not leak an infrastructure message to the client.
func From(err error) *AppError {
	var appErr *AppError
	if stderrors.As(err, &appErr) {
		return appErr
	}
	return InternalError("internal server error", err)
}
