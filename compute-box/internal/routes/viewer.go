package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/demo"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/jobs"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/web"
)

// The dashboard shows what the SDKs declared; projects, operations and programs change
// only through the caller API. It manages machines and keys, installs the demo, and runs
// operations from the Playground through the same job service as the API.

// agentDownload is the local-agent release: SonicScrew-Local-Agent.exe plus the demo apps in demo\.
const agentDownload = "https://github.com/L42ARO/sonic-screwdriver/releases/latest/download/sonic-screwdriver-windows.zip"

// hostedURL is the SDKs' default server; code samples for it can leave the URL out.
const hostedURL = "https://app.sonicscrew.click"

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

// page renders a dashboard page with what the shell needs: navigation, spend, links.
func (d Deps) page(w http.ResponseWriter, r *http.Request, name, nav, title string, data map[string]any) {
	data["Title"], data["Nav"] = title, nav
	data["OwnerSignIn"] = d.ownerSignIn()
	data["ServerURL"] = serverURL(r)
	data["AgentDownload"] = agentDownload
	data["Demo"] = demo.The
	data["Spent"], data["Cap"] = 0.0, 0.0
	if d.Ledger != nil {
		spent, capUSD, _ := d.Ledger.Budget(r.Context(), userOf(r))
		data["Spent"], data["Cap"] = spent, capUSD
	}
	w.Header().Set("Cache-Control", "no-store")
	web.Render(w, name, data)
}

// serverURL is how this browser reaches the compute-box, which is also the URL to give
// the local agent and the SDKs.
func serverURL(r *http.Request) string {
	if secureRequest(r) {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

func (d Deps) machineViews(r *http.Request) ([]machineView, error) {
	ctx, uid := r.Context(), userOf(r)
	machines, err := d.Store.ListMachines(ctx, uid)
	if err != nil {
		return nil, err
	}
	views := make([]machineView, 0, len(machines))
	for _, m := range machines {
		v := machineView{Machine: m, Online: d.Hub.Online(hub.Machine{User: uid, Name: m.Name})}
		if current, _ := d.Store.ListJobs(ctx, uid, m.Name, []string{store.StatusRunning}, 1); len(current) > 0 {
			v.Current = current[0]
		}
		waiting, _ := d.Store.ListJobs(ctx, uid, m.Name, []string{store.StatusQueued}, 100)
		v.Queued = len(waiting)
		installed, _ := d.Store.MachinePrograms(ctx, uid, m.Name)
		v.Installed = sortedInstallations(installed)
		views = append(views, v)
	}
	return views, nil
}

func hasProject(projects []*store.ProjectSummary, name string) bool {
	for _, p := range projects {
		if p.Name == name {
			return true
		}
	}
	return false
}

// viewerHome is the overview: getting started, totals, recent jobs and machine status.
func (d Deps) viewerHome(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userOf(r)
	machines, err := d.machineViews(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projects, err := d.Store.ListProjects(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	recent, _ := d.Store.ListJobs(ctx, uid, "", nil, 6)
	keys, _ := d.Store.ListAPIKeys(ctx, uid)

	online, running, queued := 0, 0, 0
	agentConnected, demoHasToken := false, false
	for _, m := range machines {
		if m.Online {
			online++
		}
		if m.Current != nil {
			running++
		}
		queued += m.Queued
		agentConnected = agentConnected || m.Online || m.LastSeen != ""
		demoHasToken = demoHasToken || (m.Name == demo.The.Machine && m.TokenPrefix != "")
	}
	hasDemo := hasProject(projects, demo.The.Project)
	done := 0
	for _, ok := range []bool{hasDemo, agentConnected, len(recent) > 0, len(keys) > 0} {
		if ok {
			done++
		}
	}
	d.page(w, r, "home.html", "overview", "Overview", map[string]any{
		"Machines": machines, "Projects": projects, "Jobs": recent,
		"Online": online, "Running": running, "Queued": queued,
		"HasDemo": hasDemo, "AgentConnected": agentConnected, "DemoHasToken": demoHasToken,
		"HasJob": len(recent) > 0, "HasKey": len(keys) > 0, "DoneCount": done, "AllDone": done == 4,
	})
}

func (d Deps) viewerMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := d.machineViews(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.page(w, r, "machines.html", "machines", "Machines", map[string]any{"Machines": machines})
}

func (d Deps) viewerProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := d.Store.ListProjects(r.Context(), userOf(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.page(w, r, "projects.html", "projects", "Projects", map[string]any{
		"Projects": projects, "HasDemo": hasProject(projects, demo.The.Project),
	})
}

func (d Deps) viewerProject(w http.ResponseWriter, r *http.Request) {
	ctx, uid, name := r.Context(), userOf(r), r.PathValue("name")
	projects, err := d.Store.ListProjects(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var project *store.ProjectSummary
	for _, p := range projects {
		if p.Name == name {
			project = p
		}
	}
	if project == nil {
		http.NotFound(w, r)
		return
	}
	operations, _ := d.Store.ListOperations(ctx, uid, name)
	programs, _ := d.Store.ProjectPrograms(ctx, uid, name)
	all, _ := d.Store.ListJobs(ctx, uid, "", nil, 200)
	var recent []*store.Job
	for _, j := range all {
		if j.Project == name && len(recent) < 8 {
			recent = append(recent, j)
		}
	}
	d.page(w, r, "project.html", "projects", name, map[string]any{
		"Project": project, "Operations": operations, "Programs": sortedPrograms(programs), "Jobs": recent,
		"IsDemo": name == demo.The.Project, "DemoMachine": demo.The.Machine,
	})
}

func (d Deps) viewerJobs(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userOf(r)
	list, err := d.Store.ListJobs(ctx, uid, "", nil, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projects, _ := d.Store.ListProjects(ctx, uid)
	machines, _ := d.Store.ListMachines(ctx, uid)
	q := r.URL.Query()
	d.page(w, r, "jobs.html", "jobs", "Jobs", map[string]any{
		"Jobs": list, "Projects": projects, "Machines": machines,
		"Statuses": []string{store.StatusQueued, store.StatusRunning, store.StatusSucceeded, store.StatusFailed, store.StatusCancelled},
		"Filter":   map[string]string{"project": q.Get("project"), "machine": q.Get("machine"), "status": q.Get("status")},
	})
}

// viewerJob shows one job's steps and screenshots, live while it runs.
func (d Deps) viewerJob(w http.ResponseWriter, r *http.Request) {
	j, err := d.Store.GetJob(r.Context(), userOf(r), r.PathValue("id"))
	if err != nil || j == nil {
		http.NotFound(w, r)
		return
	}
	steps, _ := d.Store.ListSteps(r.Context(), j.ID)
	d.page(w, r, "job.html", "jobs", j.Operation, map[string]any{"Job": j, "Steps": steps})
}

func (d Deps) viewerKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := d.Store.ListAPIKeys(r.Context(), userOf(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.page(w, r, "keys.html", "keys", "API keys", map[string]any{"Keys": keys})
}

func (d Deps) viewerSettings(w http.ResponseWriter, r *http.Request) {
	hasPassword, _ := d.Store.HasPassword(r.Context(), userOf(r))
	d.page(w, r, "settings.html", "settings", "Settings", map[string]any{
		"HasPassword": hasPassword, "PasswordSaved": r.URL.Query().Get("password") == "saved",
		"MinPassword": store.MinPasswordLength,
	})
}

// playground lets a signed-in user run an operation by hand and shows the matching code.
func (d Deps) playground(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d.renderPlayground(w, r, http.StatusOK, q.Get("project"), q.Get("operation"), q.Get("machine"), "", "")
}

func (d Deps) renderPlayground(w http.ResponseWriter, r *http.Request, status int, projectName, operationName, machineName, input, problem string) {
	ctx, uid := r.Context(), userOf(r)
	projects, err := d.Store.ListProjects(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	machines, _ := d.machineViews(r)
	data := map[string]any{"Projects": projects, "Machines": machines, "Error": problem}

	// The project asked for, else the demo, else the first.
	var project *store.ProjectSummary
	for _, p := range projects {
		if p.Name == projectName || (project == nil && projectName == "" && p.Name == demo.The.Project) {
			project = p
		}
	}
	if project == nil && len(projects) > 0 && projectName == "" {
		project = projects[0]
	}
	if project == nil && len(projects) > 0 {
		http.Redirect(w, r, "/playground", http.StatusSeeOther) // unknown project
		return
	}
	if project != nil {
		isDemo := project.Name == demo.The.Project
		operations, _ := d.Store.ListOperations(ctx, uid, project.Name)
		var operation *store.Definition
		for i := range operations {
			if operations[i].Name == operationName || (operation == nil && i == 0) {
				operation = &operations[i]
			}
		}
		if machineName == "" {
			machineName = defaultMachine(machines, isDemo)
		}
		if operation != nil && input == "" {
			input = exampleInput(*operation, isDemo)
		}
		data["Project"], data["Operations"], data["IsDemo"] = project, operations, isDemo
		if operation != nil {
			data["Operation"] = operation
		}
	}
	data["Machine"], data["Input"], data["HostedURL"] = machineName, input, hostedURL
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	d.page(w, r, "playground.html", "playground", "Playground", data)
}

// defaultMachine picks the demo machine for the demo, else the first online machine.
func defaultMachine(machines []machineView, isDemo bool) string {
	for _, m := range machines {
		if isDemo && m.Name == demo.The.Machine {
			return m.Name
		}
	}
	for _, m := range machines {
		if m.Online {
			return m.Name
		}
	}
	if len(machines) > 0 {
		return machines[0].Name
	}
	return ""
}

// exampleInput is the demo's sample input, or an empty value of each input field.
func exampleInput(op store.Definition, isDemo bool) string {
	if example, ok := demo.The.Examples[op.Name]; ok && isDemo {
		return indent(example)
	}
	var schema struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	json.Unmarshal(op.Input, &schema)
	skeleton := map[string]any{}
	for name, p := range schema.Properties {
		switch t, _ := p.Type.(string); t {
		case "integer", "number":
			skeleton[name] = 0
		case "boolean":
			skeleton[name] = false
		case "array":
			skeleton[name] = []any{}
		case "object":
			skeleton[name] = map[string]any{}
		default:
			skeleton[name] = ""
		}
	}
	b, _ := json.MarshalIndent(skeleton, "", "  ")
	return string(b)
}

func indent(raw json.RawMessage) string {
	var out bytes.Buffer
	if json.Indent(&out, raw, "", "  ") != nil {
		return string(raw)
	}
	return out.String()
}

// playgroundRun queues the job like the caller API does and opens its live page.
func (d Deps) playgroundRun(w http.ResponseWriter, r *http.Request) {
	project, operation := r.FormValue("project"), r.FormValue("operation")
	machine, input := r.FormValue("machine"), strings.TrimSpace(r.FormValue("input"))
	if input == "" {
		input = "{}"
	}
	retry := func(problem string) {
		d.renderPlayground(w, r, http.StatusBadRequest, project, operation, machine, input, problem)
	}
	if !json.Valid([]byte(input)) || !strings.HasPrefix(input, "{") {
		retry("The input must be a JSON object, like {\"patient_id\": \"P-1004\"}.")
		return
	}
	if machine == "" {
		retry("Pick a machine to run on. Add one on the Machines page.")
		return
	}
	job, err := d.Jobs.Submit(r.Context(), userOf(r), jobs.Submission{
		Project: project, Operation: operation, Machine: machine, Input: json.RawMessage(input),
	})
	if err != nil {
		var apiErr *utils.APIError
		if errors.As(err, &apiErr) {
			retry(apiErr.Message)
		} else {
			retry(err.Error())
		}
		return
	}
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// installDemo adds (or resets) the demo project and machine, then opens the Playground.
func (d Deps) installDemo(w http.ResponseWriter, r *http.Request) {
	if err := demo.Install(r.Context(), d.Store, userOf(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/playground?project="+demo.The.Project, http.StatusSeeOther)
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
