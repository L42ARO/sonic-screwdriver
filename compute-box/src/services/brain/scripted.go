package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scripted replays a fixed list of actions from scripts/<operation>.json and returns
// the script's output. It exists to test the pipeline end to end without a model.
//
// Strings may contain {{field}} placeholders filled from the job input. A string that is
// exactly "{{field}}" becomes the input value itself (keeping numbers numbers).
//
//	{"actions": [{"action": "type", "text": "{{patient_id}}"}], "output": {"found": true}}
type Scripted struct {
	Dir string
}

func (s *Scripted) Model() string { return "scripted" }

func (s *Scripted) Run(ctx context.Context, t Task, env Env) (json.RawMessage, error) {
	raw, err := os.ReadFile(filepath.Join(s.Dir, t.Definition.Name+".json"))
	if err != nil {
		return nil, fail("agent_gave_up", "no script for operation %s", t.Definition.Name)
	}
	var input map[string]any
	json.Unmarshal(t.Input, &input)
	var script struct {
		Actions []any `json:"actions"`
		Output  any   `json:"output"`
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fail("agent_gave_up", "script for %s is not valid JSON: %v", t.Definition.Name, err)
	}
	filled, _ := json.Marshal(fill(doc, input))
	json.Unmarshal(filled, &script)

	for _, a := range script.Actions {
		action, _ := json.Marshal(a)
		res, err := env.Act(ctx, action, "scripted")
		if err != nil {
			return nil, err
		}
		if !res.OK {
			return nil, fail("agent_gave_up", "scripted action %s failed: %s", Describe(action), res.Error)
		}
	}
	return json.Marshal(script.Output)
}

func fill(v any, input map[string]any) any {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "{{") && strings.HasSuffix(x, "}}") && strings.Count(x, "{{") == 1 {
			if val, ok := input[strings.TrimSpace(x[2:len(x)-2])]; ok {
				return val
			}
		}
		for k, val := range input {
			x = strings.ReplaceAll(x, "{{"+k+"}}", fmt.Sprint(val))
		}
		return x
	case []any:
		for i := range x {
			x[i] = fill(x[i], input)
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = fill(x[k], input)
		}
		return x
	}
	return v
}
