// Package sonic is a typed Go client for the sonic-screwdriver compute-box: it turns the
// tasks of a legacy program into typed Go functions.
//
//	var DentalClinic = sonic.NewProject("dental-clinic", sonic.WithMachine("dentist-front-desk"))
//
//	var LookupPatient = sonic.Define[LookupPatientInput, Patient](DentalClinic, sonic.Spec{
//		Name:         "lookup_patient",
//		Description:  "Look up a patient's record by patient ID.",
//		Instructions: "Type the ID into the Patient ID box, press Find, open the row, read the record.",
//	})
//
//	DentalClinic.Sync(ctx)                                       // once, at startup
//	patient, err := LookupPatient.Run(ctx, LookupPatientInput{PatientID: "P-1004"})
//
// Everything underneath is plain HTTP + JSON:
//
//	PUT  /v1/projects/{project}/operations                sync every operation, one call
//	POST /v1/projects/{project}/operations/{name}/jobs    run one, progress streamed as SSE
//	GET  /v1/machines                                     which local-agents are online
package sonic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// Project is one project on the compute-box and the operations declared in it.
type Project struct {
	Programs map[string]Program // nil preserves existing project programs; empty map clears
	Name     string
	Machine  string // local-agent jobs run on by default
	Context  string // added to every operation's instructions (e.g. how to log in)
	URL      string
	apiKey   string
	http     *http.Client
	ops      []Definition
}

// Option configures a Project.
type Option func(*Project)

// WithMachine sets the local-agent that jobs run on by default.
func WithMachine(name string) Option { return func(p *Project) { p.Machine = name } }

func WithPrograms(programs map[string]Program) Option {
	return func(p *Project) { p.Programs = programs }
}

// WithContext adds text to every operation's instructions, for things they all need.
func WithContext(text string) Option {
	return func(p *Project) { p.Context = strings.TrimSpace(dedent(text)) }
}

// WithURL sets the compute-box address. Defaults to SONIC_URL, then http://localhost:8080.
func WithURL(url string) Option { return func(p *Project) { p.URL = url } }

// WithAPIKey sets the API key. Defaults to SONIC_API_KEY.
func WithAPIKey(key string) Option { return func(p *Project) { p.apiKey = key } }

// NewProject declares a project. Operation names only need to be unique within it.
func NewProject(name string, opts ...Option) *Project {
	p := &Project{
		Name:   name,
		URL:    os.Getenv("SONIC_URL"),
		apiKey: os.Getenv("SONIC_API_KEY"),
		http:   &http.Client{}, // jobs stream for minutes; the caller's context bounds each call
	}
	if p.URL == "" {
		p.URL = "http://localhost:8080"
	}
	for _, opt := range opts {
		opt(p)
	}
	p.URL = strings.TrimRight(p.URL, "/")
	return p
}

// Definitions returns the JSON documents Sync sends, one per operation.
func (p *Project) Definitions() []Definition { return p.ops }

// Sync makes the project's operations on the compute-box match the ones declared here.
// Syncing nothing would delete every operation in the project, so it is refused; it
// usually means the file declaring them is not part of the build.
func (p *Project) Sync(ctx context.Context) error {
	if len(p.ops) == 0 {
		return newError("invalid_input", "no operations declared in %s; refusing to sync", p.Name)
	}
	if p.Programs != nil {
		resp, err := p.request(ctx, http.MethodPut, "/v1/projects/"+p.Name+"/programs", p.Programs, nil)
		if err != nil {
			return err
		}
		resp.Body.Close()
	}
	resp, err := p.request(ctx, http.MethodPut, "/v1/projects/"+p.Name+"/operations", p.ops, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Machine is a local-agent as the compute-box sees it.
type Machine struct {
	Name     string                      `json:"name"`
	Online   bool                        `json:"online"`
	Programs map[string]InstalledProgram `json:"-"`
	client   *Project
}

// Machines lists the local-agents and whether they are connected.
func (p *Project) Machines(ctx context.Context) ([]Machine, error) {
	resp, err := p.request(ctx, http.MethodGet, "/v1/machines", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct{ Machines []Machine }
	return out.Machines, json.NewDecoder(resp.Body).Decode(&out)
}

// Step is one action the agent took, streamed while a job runs.
type Step struct {
	N           int    `json:"n"`
	Description string `json:"description"`
}

func (p *Project) runJob(ctx context.Context, name string, input any, o runOptions) (json.RawMessage, string, error) {
	machine := o.machine
	if machine == "" {
		machine = p.Machine
	}
	if machine == "" {
		return nil, "", newError("invalid_input", "%s: no machine given and no default set", name)
	}
	headers := map[string]string{"Accept": "text/event-stream"}
	if o.idempotencyKey != "" {
		headers["Idempotency-Key"] = o.idempotencyKey
	}
	body := map[string]any{"machine": machine, "input": input}
	resp, err := p.request(ctx, http.MethodPost, "/v1/projects/"+p.Name+"/operations/"+name+"/jobs", body, headers)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	var jobID string
	for event, data := range events(resp.Body) {
		var msg struct {
			JobID  string          `json:"job_id"`
			Output json.RawMessage `json:"output"`
			Error  *struct{ Code, Message string }
			Step
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, jobID, newError("unreachable", "bad event from compute-box: %v", err)
		}
		if msg.JobID != "" {
			jobID = msg.JobID
		}
		switch event {
		case "step":
			if o.onStep != nil {
				o.onStep(msg.Step)
			}
		case "succeeded":
			return msg.Output, jobID, nil
		case "failed", "cancelled":
			e := &Error{Code: event, Message: "job " + event, JobID: jobID}
			if msg.Error != nil {
				e.Code, e.Message = msg.Error.Code, msg.Error.Message
			}
			return nil, jobID, e
		}
	}
	if ctx.Err() != nil {
		return nil, jobID, newError("cancelled", "%v", ctx.Err())
	}
	return nil, jobID, &Error{Code: "machine_disconnected", Message: "the job's stream ended before it finished", JobID: jobID}
}

func (p *Project) request(ctx context.Context, method, path string, body any, headers map[string]string) (*http.Response, error) {
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, newError("invalid_input", "%v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.URL+path, reader)
	if err != nil {
		return nil, newError("invalid_input", "%v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, newError("unreachable", "compute-box at %s: %v", p.URL, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, errorFromResponse(resp)
	}
	return resp, nil
}
