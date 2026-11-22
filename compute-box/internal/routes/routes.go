// Package routes maps HTTP requests to the compute-box services.
package routes

import (
	"log"
	"net/http"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/budget"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/jobs"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Deps are the services the handlers use.
type Deps struct {
	Store      *store.Store
	Hub        *hub.Hub
	Jobs       *jobs.Service
	Ledger     *budget.Ledger
	ScreensDir string
	// Extra, if set, registers additional handlers after the built-in ones.
	Extra func(mux *http.ServeMux)
}

// New returns the compute-box's HTTP handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Caller API
	mux.HandleFunc("PUT /v1/projects/{project}/programs", d.projectPrograms)
	mux.HandleFunc("GET /v1/projects/{project}/programs", d.projectPrograms)
	mux.HandleFunc("PUT /v1/machines/{machine}/programs", d.machinePrograms)
	mux.HandleFunc("GET /v1/machines/{machine}/programs", d.machinePrograms)
	mux.HandleFunc("PUT /v1/projects/{project}/operations", d.syncOperations)
	mux.HandleFunc("GET /v1/projects/{project}/operations", d.listOperations)
	mux.HandleFunc("POST /v1/projects/{project}/operations/{name}/jobs", d.createJob)
	mux.HandleFunc("GET /v1/jobs/{id}", d.getJob)
	mux.HandleFunc("GET /v1/jobs/{id}/events", d.jobEvents)
	mux.HandleFunc("GET /v1/machines", d.listMachines)
	mux.HandleFunc("GET /v1/usage", d.usage)

	// Local-agents
	mux.HandleFunc("GET /v1/agent", d.Hub.ServeAgent)

	// Dashboard and job viewer
	mux.HandleFunc("GET /{$}", d.viewerHome)
	mux.HandleFunc("GET /jobs/{id}", d.viewerJob)
	mux.Handle("GET /screens/", http.StripPrefix("/screens/", http.FileServer(http.Dir(d.ScreensDir))))

	if d.Extra != nil {
		d.Extra(mux)
	}
	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/v1/agent" && r.URL.Path[:min(len(r.URL.Path), 9)] != "/screens/" {
			log.Printf("http: %s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}
