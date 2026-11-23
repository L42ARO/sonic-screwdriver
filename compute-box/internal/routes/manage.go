package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/web"
)

// Machines and API keys are the only things managed from the dashboard; projects,
// operations and programs change only through the caller API (the SDKs).

// addMachine creates a machine (or reuses one the SDK declared) and shows its new token once.
func (d Deps) addMachine(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if !nameRE.MatchString(name) {
		d.showError(w, "Machine names start with a letter and use at most 64 letters, digits, underscores or hyphens.")
		return
	}
	d.issueMachineToken(w, r, name, false)
}

// regenerateMachineToken replaces a machine's token and disconnects the agent using the old one.
func (d Deps) regenerateMachineToken(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	if !d.ownsMachine(r, name) {
		http.NotFound(w, r)
		return
	}
	d.issueMachineToken(w, r, name, true)
}

func (d Deps) issueMachineToken(w http.ResponseWriter, r *http.Request, name string, replaced bool) {
	user := userOf(r)
	token, err := d.Store.IssueMachineToken(r.Context(), user, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if replaced {
		d.Hub.Disconnect(hub.Machine{User: user, Name: name})
	}
	d.showSecret(w, map[string]any{"Kind": "machine", "Name": name, "Secret": token, "Replaced": replaced})
}

// removeMachine deletes a machine, its programs and its token. Its job history stays.
func (d Deps) removeMachine(w http.ResponseWriter, r *http.Request) {
	user, name := userOf(r), r.PathValue("machine")
	if _, err := d.Store.DeleteMachine(r.Context(), user, name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.Hub.Disconnect(hub.Machine{User: user, Name: name})
	http.Redirect(w, r, "/#machines", http.StatusSeeOther)
}

func (d Deps) createAPIKey(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "API key"
	}
	if len(name) > 64 {
		d.showError(w, "Key names are at most 64 characters.")
		return
	}
	key, err := d.Store.CreateAPIKey(r.Context(), userOf(r), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.showSecret(w, map[string]any{"Kind": "key", "Name": name, "Secret": key})
}

func (d Deps) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := d.Store.RevokeAPIKey(r.Context(), userOf(r), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/#keys", http.StatusSeeOther)
}

// screenshot serves a step's screenshot to the job's owner only.
func (d Deps) screenshot(w http.ResponseWriter, r *http.Request) {
	job, err := d.Store.GetJob(r.Context(), userOf(r), r.PathValue("job"))
	file := r.PathValue("file")
	if err != nil || job == nil || file != filepath.Base(file) || !strings.HasSuffix(file, ".png") {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(d.ScreensDir, job.ID, file)
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

func (d Deps) ownsMachine(r *http.Request, name string) bool {
	machines, err := d.Store.ListMachines(r.Context(), userOf(r))
	if err != nil {
		return false
	}
	for _, m := range machines {
		if m.Name == name {
			return true
		}
	}
	return false
}

// showSecret renders a credential exactly once. The page must not be cached.
func (d Deps) showSecret(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	web.Render(w, "secret.html", data)
}

func (d Deps) showError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	web.Render(w, "secret.html", map[string]any{"Error": message})
}
