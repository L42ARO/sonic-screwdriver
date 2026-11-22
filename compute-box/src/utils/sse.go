package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSE writes Server-Sent Events to an HTTP response.
type SSE struct {
	w http.ResponseWriter
	f http.Flusher
}

// NewSSE prepares the response for streaming. It returns nil if streaming is unsupported.
func NewSSE(w http.ResponseWriter) *SSE {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &SSE{w: w, f: f}
}

// Event sends one named event with JSON data.
func (s *SSE) Event(name string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// KeepAlive sends a comment line so proxies don't close an idle stream.
func (s *SSE) KeepAlive() error {
	if _, err := fmt.Fprint(s.w, ": keep-alive\n\n"); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}
