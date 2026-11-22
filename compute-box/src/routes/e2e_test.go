package routes_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"sonic-screwdriver/compute-box/src/routes"
	"sonic-screwdriver/compute-box/src/services/brain"
	"sonic-screwdriver/compute-box/src/services/budget"
	"sonic-screwdriver/compute-box/src/services/hub"
	"sonic-screwdriver/compute-box/src/services/jobs"
	"sonic-screwdriver/compute-box/src/store"
)

// A 1x1 PNG, standing in for screenshots.
const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// TestEndToEnd runs the whole caller path against a pretend local-agent with the
// scripted brain: sync, offline rejection, a streamed job, idempotency and failure.
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	os.MkdirAll(scripts, 0o755)
	os.WriteFile(filepath.Join(scripts, "lookup_patient.json"), []byte(`{
		"actions": [{"action": "click", "x": 232, "y": 184}, {"action": "type", "text": "{{patient_id}}"}, {"action": "key", "keys": "enter"}],
		"output": {"found": true, "last_name": "Dawson", "balance": 310.75}
	}`), 0o644)

	db, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, _ := budget.New(context.Background(), db, 3, nil)
	h := hub.New(db, "secret")
	js := jobs.New(db, h, &brain.Scripted{Dir: scripts}, jobs.Options{
		ScreensDir: filepath.Join(dir, "screens"), JobTimeout: time.Minute, MaxSteps: 40,
	})
	srv := httptest.NewServer(routes.New(routes.Deps{Store: db, Hub: h, Jobs: js, Ledger: ledger, ScreensDir: filepath.Join(dir, "screens")}))
	defer srv.Close()

	// Sync the Python sample's operations.
	defs := `[
	 {"name":"lookup_patient","description":"Look up a patient.","instructions":"Find them.",
	  "input":{"type":"object","properties":{"patient_id":{"type":"string","pattern":"^P-\\d{4}$"}},"required":["patient_id"]},
	  "output":{"type":"object","properties":{"found":{"type":"boolean"}},"required":["found"]}},
	 {"name":"update_balance","description":"Set a balance.","instructions":"Edit it.",
	  "input":{"type":"object","properties":{"patient_id":{"type":"string"},"balance":{"type":"number","minimum":0}},"required":["patient_id","balance"]},
	  "output":{"type":"object","properties":{"new_balance":{"type":"number"}},"required":["new_balance"]}}]`
	res := do(t, "PUT", srv.URL+"/v1/projects/dental-clinic/operations", defs, nil)
	expect(t, res, 200, `"created":["lookup_patient","update_balance"]`)
	res = do(t, "PUT", srv.URL+"/v1/projects/dental-clinic/operations", defs, nil)
	expect(t, res, 200, `"unchanged":["lookup_patient","update_balance"]`)

	job := `{"machine":"dentist-front-desk","input":{"patient_id":"P-1004"}}`
	jobURL := srv.URL + "/v1/projects/dental-clinic/operations/lookup_patient/jobs"
	expect(t, do(t, "POST", jobURL, job, nil), 503, `"machine_offline"`)

	// A pretend agent connects and answers every action with an OK screenshot.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	actions := make(chan string, 100)
	go fakeAgent(t, ctx, strings.Replace(srv.URL, "http", "ws", 1)+"/v1/agent", actions)
	waitFor(t, func() bool { return h.Online("dentist-front-desk") })

	expect(t, do(t, "POST", jobURL, `{"machine":"dentist-front-desk","input":{"patient_id":"nope"}}`, nil), 422, `"invalid_input"`)
	expect(t, do(t, "POST", srv.URL+"/v1/projects/dental-clinic/operations/missing/jobs", job, nil), 404, `"unknown_operation"`)

	// Stream a job to completion.
	res = do(t, "POST", jobURL, job, map[string]string{"Accept": "text/event-stream", "Idempotency-Key": "k1"})
	events := readEvents(t, res)
	names := []string{}
	for _, e := range events {
		names = append(names, e[0])
	}
	if got := strings.Join(names, ","); got != "queued,started,step,step,step,succeeded" {
		t.Fatalf("events = %s", got)
	}
	if !strings.Contains(events[5][1], `"last_name":"Dawson"`) || !strings.Contains(events[3][1], `typing \"P-1004\"`) {
		t.Fatalf("unexpected events: %v", events)
	}
	if got := []string{<-actions, <-actions, <-actions}; !strings.Contains(got[1], `"P-1004"`) {
		t.Fatalf("agent received %v", got)
	}

	// Same idempotency key: the original job comes back, nothing runs again.
	res = do(t, "POST", jobURL, job, map[string]string{"Accept": "text/event-stream", "Idempotency-Key": "k1"})
	again := readEvents(t, res)
	if again[0][1] != events[0][1] || len(actions) != 0 {
		t.Fatalf("idempotent retry ran again: %v", again)
	}

	// An operation without a script fails cleanly with a stable code.
	res = do(t, "POST", srv.URL+"/v1/projects/dental-clinic/operations/update_balance/jobs",
		`{"machine":"dentist-front-desk","input":{"patient_id":"P-1004","balance":0}}`, map[string]string{"Accept": "text/event-stream"})
	failed := readEvents(t, res)
	if last := failed[len(failed)-1]; last[0] != "failed" || !strings.Contains(last[1], "agent_gave_up") {
		t.Fatalf("expected failure, got %v", failed)
	}

	expect(t, do(t, "GET", srv.URL+"/v1/machines", "", nil), 200, `"online":true`)
	expect(t, do(t, "GET", srv.URL+"/", "", nil), 200, "lookup_patient")
}

func fakeAgent(t *testing.T, ctx context.Context, url string, actions chan<- string) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Errorf("dial: %v", err)
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	hello, _ := json.Marshal(map[string]any{"type": "hello", "machine": "dentist-front-desk", "token": "secret",
		"version": "test", "screen": map[string]any{"width": 1280, "height": 720, "scale": 1.5}})
	ws.Write(ctx, websocket.MessageText, hello)
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Type   string          `json:"type"`
			ID     string          `json:"id"`
			Action json.RawMessage `json:"action"`
		}
		json.Unmarshal(data, &msg)
		if msg.Type != "action" {
			continue
		}
		actions <- string(msg.Action)
		reply, _ := json.Marshal(map[string]any{"type": "result", "id": msg.ID, "result": map[string]any{
			"ok": true, "action": "x", "elapsed_ms": 5,
			"screenshot": map[string]any{"width": 1, "height": 1, "scale": 1, "png_base64": pixel}}})
		ws.Write(ctx, websocket.MessageText, reply)
	}
}

func do(t *testing.T, method, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func expect(t *testing.T, res *http.Response, status int, contains string) {
	t.Helper()
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != status || !strings.Contains(string(b), contains) {
		t.Fatalf("got %d %s, want %d containing %s", res.StatusCode, b, status, contains)
	}
}

// readEvents reads an SSE response to the end: pairs of (event, data).
func readEvents(t *testing.T, res *http.Response) [][2]string {
	t.Helper()
	defer res.Body.Close()
	var out [][2]string
	var name string
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = line[7:]
		case strings.HasPrefix(line, "data: "):
			out = append(out, [2]string{name, line[6:]})
		}
	}
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}
