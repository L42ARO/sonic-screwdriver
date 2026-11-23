package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/brain"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// env is how a running job's brain touches the machine: every action is sent through
// the hub, recorded as a step with its screenshot, and streamed to the caller.
type env struct {
	svc       *Service
	job       *store.Job
	stream    *stream
	programs  map[string]store.Program
	installed map[string]store.InstalledProgram

	mu sync.Mutex
	n  int
}

func (e *env) Act(ctx context.Context, action json.RawMessage, note string) (hub.Result, error) {
	res, err := e.dispatch(ctx, action)
	if err != nil {
		return res, err
	}
	e.mu.Lock()
	e.n++
	n := e.n
	e.mu.Unlock()

	desc := brain.Describe(action)
	step := store.Step{JobID: e.job.ID, N: n, Description: desc, Action: string(action), OK: res.OK, Error: res.Error, Note: note}
	if res.Screenshot != nil && res.Screenshot.PNGBase64 != "" {
		if name, err := e.saveScreenshot(n, res.Screenshot.PNGBase64); err != nil {
			log.Printf("jobs: %s: saving screenshot: %v", e.job.ID, err)
		} else {
			step.Screenshot = name
		}
	}
	if err := e.svc.store.AddStep(context.Background(), step); err != nil {
		log.Printf("jobs: %s: saving step: %v", e.job.ID, err)
	}
	e.stream.publish(Event{"step", map[string]any{"job_id": e.job.ID, "n": n, "description": desc}}, false)
	return res, nil
}

// dispatch resolves model-visible program IDs here, never accepting launch settings
// from a brain. The maps are a stable configuration snapshot for this running job.
func (e *env) dispatch(ctx context.Context, action json.RawMessage) (hub.Result, error) {
	var a struct {
		Action    string `json:"action"`
		ProgramID string `json:"program_id"`
	}
	if err := json.Unmarshal(action, &a); err != nil {
		return hub.Result{}, err
	}
	reject := func(code, message string) (hub.Result, error) {
		return hub.Result{Action: a.Action, ErrorCode: code, Error: message}, nil
	}
	switch a.Action {
	case "list_programs":
		catalog := map[string]any{}
		for id, p := range e.programs {
			_, configured := e.installed[id]
			catalog[id] = map[string]any{"description": p.Description, "instructions": p.Instructions, "configured": configured}
		}
		raw, err := json.Marshal(catalog)
		return hub.Result{OK: true, Action: a.Action, Data: raw}, err
	case "launch_program":
		return reject("invalid_action", "use open_program with a project program_id; raw launch settings are not accepted")
	case "open_program":
		if _, ok := e.programs[a.ProgramID]; !ok {
			return reject("program_not_in_project", "program is not registered for this project: "+a.ProgramID)
		}
		p, ok := e.installed[a.ProgramID]
		if !ok {
			return reject("application_not_configured", "no installation configured on "+e.job.Machine+" for "+a.ProgramID)
		}
		raw, err := json.Marshal(struct {
			Action string `json:"action"`
			store.InstalledProgram
		}{"launch_program", p})
		if err != nil {
			return hub.Result{}, err
		}
		res, err := e.svc.hub.Do(ctx, machineOf(e.job), raw)
		res.Action = a.Action
		return res, err
	default:
		return e.svc.hub.Do(ctx, machineOf(e.job), action)
	}
}

func (e *env) Status(text string) {
	e.svc.hub.Tell(machineOf(e.job), map[string]string{"type": "status", "text": text})
}

func (e *env) saveScreenshot(n int, b64 string) (string, error) {
	png, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(e.svc.screensDir, e.job.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%03d.png", n)
	return name, os.WriteFile(filepath.Join(dir, name), png, 0o644)
}
