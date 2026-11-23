// Package routes maps HTTP requests to the compute-box services.
package routes

import (
	"log"
	"net/http"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/ext"
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
	// SignIn decides how people sign in to the dashboard (default: OwnerLink).
	SignIn ext.SignIn
	// Extra, if set, registers additional handlers after the built-in ones.
	Extra func(mux *http.ServeMux)
}

// New returns the compute-box's HTTP handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	if d.SignIn == nil {
		d.SignIn = OwnerLink{Store: d.Store}
	}

	// Caller API: API key
	mux.HandleFunc("PUT /v1/projects/{project}/programs", d.api(d.projectPrograms))
	mux.HandleFunc("GET /v1/projects/{project}/programs", d.api(d.projectPrograms))
	mux.HandleFunc("PUT /v1/machines/{machine}/programs", d.api(d.machinePrograms))
	mux.HandleFunc("GET /v1/machines/{machine}/programs", d.api(d.machinePrograms))
	mux.HandleFunc("PUT /v1/projects/{project}/operations", d.api(d.syncOperations))
	mux.HandleFunc("GET /v1/projects/{project}/operations", d.api(d.listOperations))
	mux.HandleFunc("POST /v1/projects/{project}/operations/{name}/jobs", d.api(d.createJob))
	mux.HandleFunc("GET /v1/jobs/{id}", d.api(d.getJob))
	mux.HandleFunc("GET /v1/jobs/{id}/events", d.api(d.jobEvents))
	mux.HandleFunc("GET /v1/machines", d.api(d.listMachines))
	mux.HandleFunc("GET /v1/usage", d.api(d.usage))

	// Local-agents: machine token, checked in the hello message
	mux.HandleFunc("GET /v1/agent", d.Hub.ServeAgent)

	// Dashboard: signed-in browser
	d.SignIn.Mount(mux, accounts{d.Store})
	mux.HandleFunc("POST /logout", d.dashboard(d.logout))
	mux.HandleFunc("GET /{$}", d.dashboard(d.viewerHome))
	mux.HandleFunc("GET /jobs/{id}", d.dashboard(d.viewerJob))
	mux.HandleFunc("GET /screens/{job}/{file}", d.dashboard(d.screenshot))
	mux.HandleFunc("POST /dashboard/machines", d.dashboard(d.addMachine))
	mux.HandleFunc("POST /dashboard/machines/{machine}/token", d.dashboard(d.regenerateMachineToken))
	mux.HandleFunc("POST /dashboard/machines/{machine}/remove", d.dashboard(d.removeMachine))
	mux.HandleFunc("POST /dashboard/keys", d.dashboard(d.createAPIKey))
	mux.HandleFunc("POST /dashboard/keys/{id}/revoke", d.dashboard(d.revokeAPIKey))

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
