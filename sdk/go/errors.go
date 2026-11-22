package sonic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Error is anything that went wrong reaching the compute-box or running a job.
//
// Code is stable and safe to branch on: unreachable, invalid_input, not_found,
// machine_offline, machine_busy, machine_disconnected, timeout, agent_gave_up,
// output_invalid, cancelled.
type Error struct {
	Code    string
	Message string
	JobID   string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Code returns the sonic error code of err, or "" if err is not a sonic error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func newError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// errorFromResponse reads an error response: {"error": {"code": ..., "message": ...}}.
func errorFromResponse(r *http.Response) *Error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var parsed struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Code != "" {
		return &Error{Code: parsed.Error.Code, Message: parsed.Error.Message}
	}
	return newError(fmt.Sprintf("http_%d", r.StatusCode), "%s", body)
}
