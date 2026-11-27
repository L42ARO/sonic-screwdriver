package brain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
)

func TestApplicationToolResultsReachModel(t *testing.T) {
	for _, tool := range []string{"list_programs", "list_running_applications"} {
		action, err := toAction(tool, `{}`)
		if err != nil || !strings.Contains(string(action), tool) {
			t.Fatalf("%s: %s %v", tool, action, err)
		}
		output := actionOutput("call", tool, hub.Result{OK: true, Data: json.RawMessage(`{"dentasoft":{"configured":true}}`)}, newJobClock(time.Now()))
		if !strings.Contains(output["output"].(string), `"configured":true`) {
			t.Fatalf("metadata missing: %v", output)
		}
	}
	if _, err := toAction("launch_program", `{"executable":"evil.exe"}`); err == nil {
		t.Fatal("raw launch exposed to model")
	}
	output := actionOutput("call", "open_program", hub.Result{ErrorCode: "startup_timeout", Error: "wait for window"}, newJobClock(time.Now()))
	if !strings.Contains(output["output"].(string), "startup_timeout") {
		t.Fatal("launch error code missing")
	}
}
