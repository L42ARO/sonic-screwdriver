// Package demo installs the DentaSoft demo into an account: the project, its operations
// and program guidance, and a machine whose DentaSoft is the copy shipped beside the
// local-agent (%SONIC_AGENT_DIR%\demo\DentaSoft.exe, expanded on the agent's computer).
//
// demo.json is generated from test-source/python-clinic-backend by export_demo.py, so the
// dashboard's demo is exactly what that example syncs.
package demo

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

//go:embed demo.json
var raw []byte

// Demo is the demo project and the machine it runs on.
type Demo struct {
	Project         string                            `json:"project"`
	Machine         string                            `json:"machine"`
	MachinePrograms map[string]store.InstalledProgram `json:"machine_programs"`
	ProjectPrograms map[string]store.Program          `json:"project_programs"`
	Operations      []store.Definition                `json:"operations"`
	// Examples are the inputs the Playground starts with, per operation.
	Examples map[string]json.RawMessage `json:"examples"`
}

// The is the embedded demo.
var The = func() Demo {
	var d Demo
	if err := json.Unmarshal(raw, &d); err != nil || d.Project == "" || d.Machine == "" || len(d.Operations) == 0 {
		panic("demo.json is invalid; regenerate it with export_demo.py")
	}
	return d
}()

// Install creates or resets the demo project and machine for the user. Other projects
// and machines are untouched; the machine's token, if any, keeps working.
func Install(ctx context.Context, s *store.Store, userID int64) error {
	if err := s.SetMachinePrograms(ctx, userID, The.Machine, The.MachinePrograms); err != nil {
		return err
	}
	if err := s.SetProjectPrograms(ctx, userID, The.Project, The.ProjectPrograms); err != nil {
		return err
	}
	_, err := s.SyncOperations(ctx, userID, The.Project, The.Operations)
	return err
}
