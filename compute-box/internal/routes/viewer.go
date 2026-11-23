package routes

import (
	"net/http"
	"sort"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/web"
)

// The dashboard is read-only: projects, operations and programs change only through the
// caller API (the SDKs), so what is shown always matches what the SDKs declared.

type namedProgram struct {
	Name string
	store.Program
}

type namedInstallation struct {
	Name string
	store.InstalledProgram
}

type machineView struct {
	store.Machine
	Online    bool
	Current   *store.Job
	Queued    int
	Installed []namedInstallation
}

type projectView struct {
	*store.ProjectSummary
	Operations []store.Definition
	Programs   []namedProgram
}

// viewerHome is the unified dashboard: machines, recent jobs and projects on one page.
func (d Deps) viewerHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := userOf(r)
	machines, err := d.Store.ListMachines(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projects, err := d.Store.ListProjects(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	online, running, queued := 0, 0, 0
	machineViews := make([]machineView, 0, len(machines))
	for _, m := range machines {
		v := machineView{Machine: m, Online: d.Hub.Online(hub.Machine{User: uid, Name: m.Name})}
		if v.Online {
			online++
		}
		if current, _ := d.Store.ListJobs(ctx, uid, m.Name, []string{store.StatusRunning}, 1); len(current) > 0 {
			v.Current = current[0]
			running++
		}
		waiting, _ := d.Store.ListJobs(ctx, uid, m.Name, []string{store.StatusQueued}, 100)
		v.Queued = len(waiting)
		queued += v.Queued
		installed, _ := d.Store.MachinePrograms(ctx, uid, m.Name)
		v.Installed = sortedInstallations(installed)
		machineViews = append(machineViews, v)
	}

	projectViews := make([]projectView, 0, len(projects))
	for _, p := range projects {
		operations, _ := d.Store.ListOperations(ctx, uid, p.Name)
		programs, _ := d.Store.ProjectPrograms(ctx, uid, p.Name)
		projectViews = append(projectViews, projectView{p, operations, sortedPrograms(programs)})
	}

	jobs, _ := d.Store.ListJobs(ctx, uid, "", nil, 40)
	keys, _ := d.Store.ListAPIKeys(ctx, uid)
	spent, capUSD, _ := d.Ledger.Budget(ctx, uid)
	web.Render(w, "home.html", map[string]any{
		"Machines": machineViews, "Projects": projectViews, "Jobs": jobs, "Keys": keys,
		"Online": online, "Running": running, "Queued": queued, "Spent": spent, "Cap": capUSD,
	})
}

// viewerJob shows one job's steps and screenshots.
func (d Deps) viewerJob(w http.ResponseWriter, r *http.Request) {
	j, err := d.Store.GetJob(r.Context(), userOf(r), r.PathValue("id"))
	if err != nil || j == nil {
		http.NotFound(w, r)
		return
	}
	steps, _ := d.Store.ListSteps(r.Context(), j.ID)
	web.Render(w, "job.html", map[string]any{"Job": j, "Steps": steps})
}

func sortedPrograms(m map[string]store.Program) []namedProgram {
	out := make([]namedProgram, 0, len(m))
	for name, p := range m {
		out = append(out, namedProgram{name, p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedInstallations(m map[string]store.InstalledProgram) []namedInstallation {
	out := make([]namedInstallation, 0, len(m))
	for name, p := range m {
		out = append(out, namedInstallation{name, p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
