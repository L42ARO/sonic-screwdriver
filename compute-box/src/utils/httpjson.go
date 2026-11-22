// Package utils holds small helpers shared by the compute-box packages.
package utils

import (
	"encoding/json"
	"net/http"
)

// APIError is the caller-facing error shape: {"error": {"code": ..., "message": ...}}.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func NewError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes err in the standard error shape. Unknown errors become 500 internal.
func WriteError(w http.ResponseWriter, err error) {
	e, ok := err.(*APIError)
	if !ok {
		e = NewError(http.StatusInternalServerError, "internal", err.Error())
	}
	WriteJSON(w, e.Status, map[string]any{"error": e})
}

// ReadJSON decodes the request body into v, limited to 8 MB.
func ReadJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return NewError(http.StatusBadRequest, "invalid_input", "request body is not valid JSON: "+err.Error())
	}
	return nil
}
