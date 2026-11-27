package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/budget"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// OpenAI drives the screen with an OpenAI model through the Responses API.
type OpenAI struct {
	client    openAIClient
	model     string
	reasoning string
	ledger    Meter
}

// Meter checks and records model spend per user; *budget.Ledger implements it.
type Meter interface {
	Allow(ctx context.Context, userID int64) error
	Record(ctx context.Context, userID int64, jobID, model string, t budget.Tokens) float64
}

func NewOpenAI(key, model, reasoningEffort string, ledger Meter) (*OpenAI, error) {
	if key == "" {
		return nil, fmt.Errorf("no OpenAI key: set OPENAI_KEY in .env")
	}
	if !budget.KnownModel(model) {
		return nil, fmt.Errorf("no pricing for model %q: the budget cap can't be enforced", model)
	}
	return &OpenAI{
		client:    openAIClient{key: key, http: &http.Client{Timeout: 3 * time.Minute}},
		model:     model,
		reasoning: reasoningEffort,
		ledger:    ledger,
	}, nil
}

func (b *OpenAI) Model() string { return b.model }

const (
	maxTurns         = 80 // model calls per job
	maxNudges        = 2  // replies without a tool call before giving up
	maxFinishRetries = 3  // finish calls whose output doesn't match the schema
)

func (b *OpenAI) Run(ctx context.Context, t Task, env Env) (json.RawMessage, error) {
	first, err := env.Act(ctx, json.RawMessage(`{"action":"screenshot"}`), "initial screenshot")
	if err != nil {
		return nil, err
	}
	if first.Screenshot == nil {
		return nil, fail("agent_gave_up", "the machine returned no screenshot: %s", first.Error)
	}
	clock := newScreenClock(time.Now(), first.Screenshot.PNGBase64)

	// Cached prefix first (developer message + tools), then the job, then an append-only
	// conversation. Earlier items are never edited.
	items := []any{
		developerMessage(t.Definition),
		userMessage(t.Input, first.Screenshot.Width, first.Screenshot.Height, first.Screenshot.PNGBase64),
	}
	req := responsesRequest{
		Model:             b.model,
		Tools:             tools(),
		ParallelToolCalls: false,
		Store:             false,
		Include:           []string{"reasoning.encrypted_content"},
		PromptCacheKey:    "sonic:" + t.Project + ":" + t.Definition.Name,
	}
	if b.reasoning != "" {
		req.Reasoning = &reasoning{Effort: b.reasoning}
	}

	actions, nudges, finishRetries := 0, 0, 0
	for turn := 0; turn < maxTurns; turn++ {
		if err := b.ledger.Allow(ctx, t.UserID); err != nil {
			return nil, fail("budget_exceeded", "%v", err)
		}
		env.Status("thinking")
		req.Input = items
		resp, err := b.client.create(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fail("agent_gave_up", "model call failed: %v", err)
		}
		u := resp.Usage
		cost := b.ledger.Record(ctx, t.UserID, t.JobID, b.model, budget.Tokens{
			Input: u.InputTokens, Cached: u.InputTokensDetails.CachedTokens,
			CacheWrite: u.InputTokensDetails.CacheWriteTokens,
			Output:     u.OutputTokens, Reasoning: u.OutputTokensDetails.ReasoningTokens,
		})
		log.Printf("brain: %s turn %d: in=%d cached=%d write=%d out=%d cost=$%.5f",
			t.JobID, turn+1, u.InputTokens, u.InputTokensDetails.CachedTokens,
			u.InputTokensDetails.CacheWriteTokens, u.OutputTokens, cost)

		var calls []outputItem
		var note []string
		for _, raw := range resp.Output {
			items = append(items, raw) // reasoning, messages and calls go back verbatim
			var it outputItem
			json.Unmarshal(raw, &it)
			switch it.Type {
			case "function_call":
				calls = append(calls, it)
			case "message":
				for _, c := range it.Content {
					if c.Text != "" {
						note = append(note, c.Text)
					}
				}
			}
		}
		if resp.IncompleteDetails != nil && len(calls) == 0 {
			return nil, fail("agent_gave_up", "model stopped early: %s", resp.IncompleteDetails.Reason)
		}
		if len(calls) == 0 {
			nudges++
			if nudges > maxNudges {
				return nil, fail("agent_gave_up", "the model stopped using tools: %s", strings.Join(note, " "))
			}
			items = append(items, map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "Continue with a tool call. Call finish when the task is done, or fail if it can't be."},
			}})
			continue
		}

		for _, call := range calls {
			switch call.Name {
			case "finish":
				var args struct {
					Output json.RawMessage `json:"output"`
				}
				json.Unmarshal([]byte(call.Arguments), &args)
				if err := utils.Validate("output", t.Definition.Output, args.Output); err != nil {
					finishRetries++
					if finishRetries > maxFinishRetries {
						return nil, fail("output_invalid", "%v", err)
					}
					items = append(items, textOutput(call.CallID, "Not accepted: "+err.Error()+" Fix the output and call finish again."))
					continue
				}
				return args.Output, nil

			case "fail":
				var args struct{ Code, Reason string }
				json.Unmarshal([]byte(call.Arguments), &args)
				switch args.Code {
				case "not_found", "invalid_input":
				default:
					args.Code = "agent_gave_up"
				}
				return nil, fail(args.Code, "%s", args.Reason)

			default:
				action, err := toAction(call.Name, call.Arguments)
				if err != nil {
					items = append(items, textOutput(call.CallID, "Error: "+err.Error()))
					continue
				}
				actions++
				if actions > t.MaxSteps {
					return nil, fail("agent_gave_up", "reached the limit of %d screen actions", t.MaxSteps)
				}
				res, err := env.Act(ctx, action, strings.Join(note, " "))
				note = nil
				if err != nil {
					return nil, err
				}
				items = append(items, actionOutput(call.CallID, call.Name, res, clock))
			}
		}
	}
	return nil, fail("agent_gave_up", "reached the limit of %d model turns", maxTurns)
}

// toAction converts a tool call into a local-agent action.
func toAction(name, arguments string) (json.RawMessage, error) {
	var a map[string]any
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return nil, fmt.Errorf("arguments are not valid JSON")
	}
	if a == nil {
		a = map[string]any{}
	}
	switch name {
	case "screenshot", "zoom", "click", "double_click", "type", "key", "scroll", "focus_window", "list_programs", "list_running_applications", "open_program":
	case "wait":
		if ms, _ := a["ms"].(float64); ms > 10000 {
			a["ms"] = 10000
		}
	case "drag":
		a = map[string]any{"from": []any{a["from_x"], a["from_y"]}, "to": []any{a["to_x"], a["to_y"]}}
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	a["action"] = name
	return json.Marshal(a)
}

func textOutput(callID, text string) map[string]any {
	return map[string]any{"type": "function_call_output", "call_id": callID, "output": text}
}

// actionOutput reports an action's result with the screenshot taken after it, starting
// with how much time has passed and whether the screen changed (see screenClock).
func actionOutput(callID, tool string, res hub.Result, clock *screenClock) map[string]any {
	full := ""
	if res.Screenshot != nil && tool != "zoom" {
		full = res.Screenshot.PNGBase64
	}
	timing := clock.observe(time.Now(), full)
	var text string
	switch {
	case !res.OK:
		text = "The action failed (" + res.ErrorCode + "): " + res.Error
	case tool == "list_programs":
		text = "Project programs:"
	case tool == "list_running_applications":
		text = "Running applications and windows:"
	case tool == "zoom" && res.Screenshot != nil && len(res.Screenshot.Region) == 4:
		r := res.Screenshot.Region
		text = fmt.Sprintf("Zoomed view of the region at (%d, %d), %dx%d, shown larger. Coordinates you use stay in full-screenshot space.", r[0], r[1], r[2], r[3])
	default:
		text = "Done. The screen now:"
	}
	if len(res.Data) > 0 {
		text += "\n" + string(res.Data)
	}
	if res.Unsettled {
		text += " (The screen was still changing when this was taken; consider waiting.)"
	}
	text = timing + " " + text
	if res.Screenshot == nil {
		return textOutput(callID, text)
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": callID,
		"output": []any{
			map[string]any{"type": "input_text", "text": text},
			imageContent(res.Screenshot.PNGBase64),
		},
	}
}
