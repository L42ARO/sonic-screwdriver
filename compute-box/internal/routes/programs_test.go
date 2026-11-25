package routes_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/routes"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/brain"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/jobs"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

func TestProgramConfigurationAndLaunchResolution(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scripts := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scripts, 0755); err != nil {
		t.Fatal(err)
	}
	h := hub.New(db)
	owner := newOwner(t, db)
	js := jobs.New(db, h, &brain.Scripted{Dir: scripts}, jobs.Options{JobTimeout: time.Second * 10, MaxSteps: 10, ScreensDir: dir})
	srv := httptest.NewServer(routes.New(routes.Deps{Store: db, Hub: h, Jobs: js, ScreensDir: dir}))
	defer srv.Close()
	machineURL := srv.URL + "/v1/machines/dentist-front-desk/programs"
	projectURL := srv.URL + "/v1/projects/clinic/programs"
	installed := `{"dentasoft":{"executable":"C:\\Apps\\DentaSoft.exe","args":["a b","x&y"],"window_title_contains":"DentaSoft"},"billing":{"executable":"C:\\Apps\\Billing.exe","window_title_contains":"Billing"}}`
	expect(t, do(t, "PUT", machineURL, installed, nil), 200, `"dentasoft"`)
	expect(t, do(t, "PUT", projectURL, `{"dentasoft":{"description":"Patient records"},"viewer":{"description":"Referrals"}}`, nil), 200, `"viewer"`)
	// Syncing operations must not overwrite either program registry.
	defs := `[{"name":"lookup","description":"Look up","instructions":"Find","input":{"type":"object"},"output":{"type":"object"}}]`
	expect(t, do(t, "PUT", srv.URL+"/v1/projects/clinic/operations", defs, nil), 200, "lookup")
	expect(t, do(t, "GET", projectURL, "", nil), 200, "Patient records")
	expect(t, do(t, "GET", machineURL, "", nil), 200, `"billing"`)
	// Validation fails atomically instead of deleting existing configuration.
	for _, bad := range []string{`null`, `{"bad":{"executable":"relative.exe","window_title_contains":"X"}}`, `{"bad":{"executable":"C:\\Apps\\a.exe","window_title_contains":""}}`, `{"bad":{"executable":"C:\\a.exe","window_title_contains":"A","startup_timeout_ms":120001}}`,
		// A variable must start the path and be followed by a separator; anything else is relative.
		`{"bad":{"executable":"%APPDATA%","window_title_contains":"X"}}`, `{"bad":{"executable":"app%X%\\a.exe","window_title_contains":"X"}}`, `{"bad":{"executable":"%1%\\a.exe","window_title_contains":"X"}}`} {
		expect(t, do(t, "PUT", machineURL, bad, nil), 400, "invalid_definition")
	}
	expect(t, do(t, "GET", machineURL, "", nil), 200, `"billing"`)
	// Paths may start with a variable the agent expands on its own machine, alongside
	// ordinary absolute paths anywhere on the computer.
	expect(t, do(t, "PUT", srv.URL+"/v1/machines/vars/programs",
		`{"demo":{"executable":"%SONIC_AGENT_DIR%\\demo\\DentaSoft.exe","window_title_contains":"DentaSoft"},`+
			`"mine":{"executable":"E:\\Clinic Apps\\x.exe","working_directory":"%LOCALAPPDATA%\\x","window_title_contains":"X"}}`, nil), 200, `%SONIC_AGENT_DIR%`)
	// A second machine has its own installation, and configuration survives reconnect.
	expect(t, do(t, "PUT", srv.URL+"/v1/machines/other/programs", `{"dentasoft":{"executable":"D:\\Legacy\\DentaSoft.exe","window_title_contains":"DentaSoft"}}`, nil), 200, `D:\\Legacy`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	actions := make(chan string, 30)
	go fakeAgent(t, ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/agent", owner.machineToken, actions)
	waitFor(t, func() bool { return h.Online(hub.Machine{User: owner.id, Name: "dentist-front-desk"}) })
	expect(t, do(t, "GET", machineURL, "", nil), 200, `C:\\Apps`)

	writeScript := func(action string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(scripts, "lookup.json"), []byte(`{"actions":[`+action+`],"output":{}}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func() [][2]string {
		return readEvents(t, do(t, "POST", srv.URL+"/v1/projects/clinic/operations/lookup/jobs", `{"machine":"dentist-front-desk","input":{}}`, map[string]string{"Accept": "text/event-stream"}))
	}
	writeScript(`{"action":"open_program","program_id":"dentasoft","executable":"C:\\evil.exe","args":["override"]}`)
	events := run()
	if events[len(events)-1][0] != "succeeded" {
		t.Fatalf("launch job: %v", events)
	}
	var action struct {
		Action, Executable string
		Args               []string
	}
	if err := json.Unmarshal([]byte(<-actions), &action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "launch_program" || action.Executable != `C:\Apps\DentaSoft.exe` || len(action.Args) != 2 || action.Args[0] != "a b" || action.Args[1] != "x&y" {
		t.Fatalf("wrong resolved action: %+v", action)
	}

	for _, tc := range []struct{ action, message string }{
		{`{"action":"open_program","program_id":"billing"}`, "not registered for this project"},
		{`{"action":"open_program","program_id":"viewer"}`, "no installation configured"},
		{`{"action":"launch_program","executable":"C:\\evil.exe"}`, "raw launch settings are not accepted"},
	} {
		writeScript(tc.action)
		events = run()
		final := events[len(events)-1]
		if final[0] != "failed" || !strings.Contains(final[1], tc.message) {
			t.Fatalf("want %s: %v", tc.message, events)
		}
		if len(actions) != 0 {
			t.Fatal("rejected launch reached executor")
		}
	}
	expect(t, do(t, "PUT", projectURL, `{}`, nil), 200, `{}`)
	expect(t, do(t, "GET", machineURL, "", nil), 200, `"billing"`)
	// Opening the same database after applying the additive schema preserves config.
	other, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	saved, err := other.MachinePrograms(context.Background(), other.DefaultUserID(), "dentist-front-desk")
	if err != nil || saved["dentasoft"].Executable != `C:\Apps\DentaSoft.exe` {
		t.Fatalf("persistence: %v %v", saved, err)
	}
}
