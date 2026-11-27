package brain

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// The prompt is ordered for prompt caching: everything that never changes for an
// operation comes first (these rules, the tool list, the operation's task), and the
// conversation after it only ever grows at the end.

const rules = `You operate a Windows computer to complete one task in a legacy desktop program that has no API. You see the screen only through screenshots, and you act only through the tools: mouse, keyboard, zoom and wait.

How to work:
- Every action returns a new screenshot taken once the screen stopped changing. Study it before your next action.
- Coordinates are pixels in the screenshot: (0, 0) is the top-left corner. Aim for the centre of what you click.
- Use zoom to read small or unclear text before relying on it. Zoom coordinates are in the same screenshot space.
- After each action, check that it did what you expected. If not, correct it: misclicks are normal.
- Click into a text box before typing into it. To replace text, clear it first: click at the end of the text and press backspace (use count to press it many times at once).
- Legacy programs often ignore modern shortcuts. If a shortcut has no visible effect, don't keep trying others: fall back to the basic way a person would do it.
- Legacy programs can be slow. If the screen shows a busy cursor, "Please wait..." or is half drawn, use wait.
- Each result starts with the time since the job started and the time since your previous action. Use it with the screenshots: a program that is loading makes progress (a progress bar moves, text appears); a program that is waiting for something looks the same screenshot after screenshot.
- If a splash screen, loading message or progress bar has looked the same for about 15 seconds, stop waiting. The program may be waiting for input: click once on its title bar (the bar with the window's name, which has no buttons in its middle), then look again. If it still makes no progress, check list_running_applications for a dialog or second window and bring it forward with focus_window. Only fail after these steps, saying what the screen showed.
- Only report values you actually read on screen. Never guess or invent data.
- Do only what the task asks. Don't close the program or change anything else.
- Use list_programs to discover the programs relevant to this project and their usage guidance. Use list_running_applications to inspect processes and windows. These tools return metadata, not application contents.
- Decide which project programs the task needs and when. Use open_program with a registered program_id to open or focus one. It reuses a matching window and avoids launching a second instance. If startup times out, inspect and wait rather than repeatedly trying to launch. If configuration is missing, explain that with fail. For projects with no registered programs, you may still work in the already open target application.
- Use focus_window to bring an existing target window forward. Read application contents only from screenshots. Do not open terminals, run shell commands, or use the Start menu to launch unregistered programs. A window appearing does not mean the app has finished loading; verify its screenshot and handle login or dialogs as instructed.
- When the task is complete, call finish with the output. If it cannot be done, call fail with a code and the reason.`

// developerMessage is the cached prefix for one operation. It must stay byte-identical
// between calls, so it contains nothing that varies per job.
func developerMessage(def store.Definition) map[string]any {
	output, _ := json.MarshalIndent(def.Output, "", "  ")
	text := fmt.Sprintf("%s\n\n## Task: %s\n%s\n\n%s\n\n## Output\nfinish takes an `output` object matching this JSON Schema:\n%s",
		rules, def.Name, def.Description, strings.TrimSpace(def.Instructions), output)
	return map[string]any{
		"type":    "message",
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}
}

// userMessage starts the job: its input and the first screenshot. It is dynamic, so it
// comes after the cached prefix.
func userMessage(input json.RawMessage, width, height int, pngBase64 string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": fmt.Sprintf(
				"Task input:\n%s\n\nThe screen is %dx%d pixels. Here is the current screen.", input, width, height)},
			imageContent(pngBase64),
		},
	}
}

func imageContent(pngBase64 string) map[string]any {
	return map[string]any{
		"type":      "input_image",
		"image_url": "data:image/png;base64," + pngBase64,
		"detail":    "high",
	}
}

// tools returns the tool list. Its content and order never change, which keeps it in the
// cached prefix.
func tools() []any {
	point := map[string]any{
		"x": map[string]any{"type": "integer", "description": "pixels from the left of the screenshot"},
		"y": map[string]any{"type": "integer", "description": "pixels from the top of the screenshot"},
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	merge := func(a, b map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range a {
			out[k] = v
		}
		for k, v := range b {
			out[k] = v
		}
		return out
	}
	fn := func(name, description string, params map[string]any) map[string]any {
		return map[string]any{"type": "function", "name": name, "description": description, "parameters": params}
	}
	return []any{
		fn("list_programs", "List this project's program IDs, descriptions, usage guidance, and whether installed launch settings exist on this machine.", obj(map[string]any{})),
		fn("list_running_applications", "Inspect running processes and their visible windows, including executable paths where accessible, minimized state and foreground state.", obj(map[string]any{})),
		fn("open_program", "Open or focus a configured project program by ID. Returns a screenshot or a launch error; does not accept commands or paths.", obj(map[string]any{"program_id": map[string]any{"type": "string"}}, "program_id")),
		fn("screenshot", "Take a fresh screenshot without doing anything.", obj(map[string]any{})),
		fn("zoom", "See a region of the screen at full resolution, to read small text. Doesn't change anything.",
			obj(merge(point, map[string]any{
				"w": map[string]any{"type": "integer", "description": "width of the region"},
				"h": map[string]any{"type": "integer", "description": "height of the region"},
			}), "x", "y", "w", "h")),
		fn("click", "Move the mouse to a point and click.",
			obj(merge(point, map[string]any{
				"button": map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}},
				"count":  map[string]any{"type": "integer", "description": "number of clicks, default 1"},
			}), "x", "y")),
		fn("double_click", "Double-click a point.", obj(point, "x", "y")),
		fn("type", "Type text at the keyboard focus, exactly as given.",
			obj(map[string]any{"text": map[string]any{"type": "string"}}, "text")),
		fn("key", "Press a key or key chord, optionally several times. Examples: enter, tab, escape, backspace, delete, f3, ctrl+a, alt+f4, y.",
			obj(map[string]any{
				"keys":  map[string]any{"type": "string"},
				"count": map[string]any{"type": "integer", "description": "times to press it, default 1"},
			}, "keys")),
		fn("scroll", "Scroll the mouse wheel at a point. dy > 0 scrolls down, dx > 0 scrolls right, in notches.",
			obj(merge(point, map[string]any{
				"dx": map[string]any{"type": "integer"},
				"dy": map[string]any{"type": "integer"},
			}), "x", "y")),
		fn("drag", "Press the left button at one point, move to another, release.",
			obj(map[string]any{
				"from_x": map[string]any{"type": "integer"}, "from_y": map[string]any{"type": "integer"},
				"to_x": map[string]any{"type": "integer"}, "to_y": map[string]any{"type": "integer"},
			}, "from_x", "from_y", "to_x", "to_y")),
		fn("focus_window", "Bring an already open window to the front, by part of its title. It cannot open programs.",
			obj(map[string]any{"title_contains": map[string]any{"type": "string"}}, "title_contains")),
		fn("wait", "Wait for the program, then take a screenshot.",
			obj(map[string]any{"ms": map[string]any{"type": "integer", "description": "milliseconds, at most 10000"}}, "ms")),
		fn("finish", "The task is complete. Report the output, matching the output schema.",
			obj(map[string]any{"output": map[string]any{"type": "object", "description": "the task's output"}}, "output")),
		fn("fail", "The task cannot be completed.",
			obj(map[string]any{
				"code": map[string]any{"type": "string", "enum": []string{"not_found", "invalid_input", "agent_gave_up"},
					"description": "not_found: what the task asked for doesn't exist; invalid_input: the input can't be used; agent_gave_up: anything else"},
				"reason": map[string]any{"type": "string"},
			}, "code", "reason")),
	}
}
