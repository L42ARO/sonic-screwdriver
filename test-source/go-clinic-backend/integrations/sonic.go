// Package integrations holds the sonic-screwdriver plumbing for net/http. Nothing to edit
// here.
//
// Setup syncs the project's operations, adds /health, and Handle turns agent failures
// into HTTP errors so handlers can simply return them.
package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	sonic "github.com/L42ARO/sonic-screwdriver/sdk/go"
)

var status = map[string]int{
	"invalid_input":   http.StatusUnprocessableEntity,
	"not_found":       http.StatusNotFound,
	"unreachable":     http.StatusServiceUnavailable,
	"machine_offline": http.StatusServiceUnavailable,
	"machine_busy":    http.StatusServiceUnavailable,
}

// Setup syncs the project's operations and adds GET /health to mux.
func Setup(ctx context.Context, mux *http.ServeMux, project *sonic.Project) {
	if err := project.Sync(ctx); err != nil {
		log.Printf("could not sync operations: %v", err)
	}
	mux.HandleFunc("GET /health", Handle(func(w http.ResponseWriter, r *http.Request) error {
		machines, err := project.Machines(r.Context())
		if err != nil {
			return err
		}
		online := false
		for _, m := range machines {
			online = online || (m.Name == project.Machine && m.Online)
		}
		return json.NewEncoder(w).Encode(map[string]any{"ok": true, "machine_online": online})
	}))
}

// Handle adapts a handler that returns an error. Sonic errors become their HTTP status.
func Handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		code, message, httpStatus := "internal", err.Error(), http.StatusInternalServerError
		var e *sonic.Error
		if errors.As(err, &e) {
			code, message, httpStatus = e.Code, e.Message, http.StatusBadGateway
			if s, ok := status[e.Code]; ok {
				httpStatus = s
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
	}
}
