package common

import "errors"

// AppError represents an error with an attached code and HTTP status.
type AppError struct {
	Code       string
	Message    string
	HTTPStatus int
	Err        error
	Details    any
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

// Unwrap allows errors.Is/As to inspect the underlying error.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewAppError constructs an AppError.
func NewAppError(code, message string, status int, err error) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: status, Err: err}
}

// IsAppError checks whether the error is an AppError.
func IsAppError(err error) bool {
	var target *AppError
	return errors.As(err, &target)
}
