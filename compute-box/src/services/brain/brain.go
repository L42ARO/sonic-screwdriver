// Package brain decides what to do on screen to carry out an operation. A brain only
// sees screenshots and only acts through Env; it never talks to a machine directly.
package brain

import (
	"context"
	"encoding/json"
	"fmt"

	"sonic-screwdriver/compute-box/src/services/hub"
	"sonic-screwdriver/compute-box/src/store"
)

// Task is one job as a brain sees it.
type Task struct {
	JobID      string
	Project    string
	Definition store.Definition
	Input      json.RawMessage
	Screen     hub.Screen
	MaxSteps   int
}

// Env is the brain's only window onto the machine.
type Env interface {
	// Act runs one action (local-agent JSON, e.g. {"action":"click","x":1,"y":2}), records
	// it as a job step with `note`, and returns the result with a fresh screenshot.
	Act(ctx context.Context, action json.RawMessage, note string) (hub.Result, error)
	// Status updates the on-screen overlay, e.g. "thinking".
	Status(text string)
}

// Brain carries out tasks.
type Brain interface {
	// Model names what is deciding, e.g. "gpt-6-luna" or "scripted".
	Model() string
	// Run completes the task and returns its output JSON.
	Run(ctx context.Context, t Task, env Env) (json.RawMessage, error)
}

// Error is a task failure with a stable code the caller sees.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Describe turns a local-agent action into the short phrase shown in steps and logs.
func Describe(action json.RawMessage) string {
	var a struct {
		Action        string `json:"action"`
		X, Y, W, H    int
		Button        string `json:"button"`
		Count         int    `json:"count"`
		Text          string `json:"text"`
		Keys          string `json:"keys"`
		MS            int    `json:"ms"`
		DX, DY        int
		From, To      []int
		TitleContains string `json:"title_contains"`
		ProgramID     string `json:"program_id"`
	}
	json.Unmarshal(action, &a)
	switch a.Action {
	case "list_programs":
		return "listing project programs"
	case "list_running_applications":
		return "inspecting running applications"
	case "open_program":
		return "opening program " + a.ProgramID
	case "screenshot":
		return "taking a screenshot"
	case "zoom":
		return fmt.Sprintf("zooming into %dx%d at (%d, %d)", a.W, a.H, a.X, a.Y)
	case "click":
		s := fmt.Sprintf("clicking (%d, %d)", a.X, a.Y)
		if a.Button != "" && a.Button != "left" {
			s = a.Button + "-" + s
		}
		if a.Count > 1 {
			s += fmt.Sprintf(" x%d", a.Count)
		}
		return s
	case "double_click":
		return fmt.Sprintf("double-clicking (%d, %d)", a.X, a.Y)
	case "type":
		return fmt.Sprintf("typing %q", a.Text)
	case "key":
		if a.Count > 1 {
			return fmt.Sprintf("pressing %s x%d", a.Keys, a.Count)
		}
		return "pressing " + a.Keys
	case "scroll":
		return fmt.Sprintf("scrolling %d,%d at (%d, %d)", a.DX, a.DY, a.X, a.Y)
	case "drag":
		if len(a.From) == 2 && len(a.To) == 2 {
			return fmt.Sprintf("dragging (%d, %d) to (%d, %d)", a.From[0], a.From[1], a.To[0], a.To[1])
		}
		return "dragging"
	case "wait":
		return fmt.Sprintf("waiting %d ms", a.MS)
	case "focus_window":
		return fmt.Sprintf("focusing window %q", a.TitleContains)
	}
	return a.Action
}
