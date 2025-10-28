package common

import (
	"encoding/json"
	"net/http"
)

// ErrorBody represents a consistent error payload returned by the API.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// JSON writes the provided value to the response writer as JSON.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// JSONError renders an error response using the canonical error shape.
func JSONError(w http.ResponseWriter, status int, code, message string, details any) {
	JSON(w, status, map[string]any{
		"error": ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}
