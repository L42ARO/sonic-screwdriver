package sonic

import (
	"context"
	"net/http"
)

type Program struct {
	Description  string `json:"description"`
	Instructions string `json:"instructions,omitempty"`
}

type InstalledProgram struct {
	Executable          string   `json:"executable"`
	Args                []string `json:"args,omitempty"`
	WorkingDirectory    string   `json:"working_directory,omitempty"`
	WindowTitleContains string   `json:"window_title_contains"`
	StartupTimeoutMS    int      `json:"startup_timeout_ms,omitempty"`
}

// NewMachine configures installations independently of any project's operations.
// Options WithURL and WithAPIKey select the compute-box connection.
func NewMachine(name string, programs map[string]InstalledProgram, opts ...Option) *Machine {
	return &Machine{Name: name, Programs: programs, client: NewProject("", opts...)}
}

// Sync replaces this machine's program registry; true explicitly permits clearing it.
func (m *Machine) Sync(ctx context.Context, allowEmpty ...bool) error {
	if m.client == nil {
		return newError("invalid_input", "use NewMachine to configure a machine")
	}
	if len(m.Programs) == 0 && (len(allowEmpty) == 0 || !allowEmpty[0]) {
		return newError("invalid_input", "no machine programs declared; pass true to clear")
	}
	programs := m.Programs
	if programs == nil {
		programs = map[string]InstalledProgram{}
	}
	resp, err := m.client.request(ctx, http.MethodPut, "/v1/machines/"+m.Name+"/programs", programs, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
