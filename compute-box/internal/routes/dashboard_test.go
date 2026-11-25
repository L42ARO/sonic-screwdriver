package routes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/routes"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/budget"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// The dashboard shows what the SDK synced on one page, escapes it, and can't change it.
func TestDashboardShowsSyncedConfigurationReadOnly(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger := budget.New(db, budget.NewCapLimiter(db, 3, nil))
	srv := httptest.NewServer(routes.New(routes.Deps{Store: db, Hub: hub.New(db), Ledger: ledger}))
	defer srv.Close()
	owner := newOwner(t, db)
	page := map[string]string{"Cookie": owner.cookie}

	expect(t, do(t, "GET", srv.URL+"/projects", "", page), 200, "No projects yet")
	expect(t, do(t, "GET", srv.URL+"/", "", page), 200, "Get started")

	expect(t, do(t, "PUT", srv.URL+"/v1/machines/front-desk/programs",
		`{"dentasoft":{"executable":"C:\\Apps\\DentaSoft.exe","args":["--clinic","main"],"window_title_contains":"DentaSoft"}}`, nil), 200, "dentasoft")
	expect(t, do(t, "PUT", srv.URL+"/v1/projects/clinic/programs",
		`{"dentasoft":{"description":"Patient records","instructions":"Log in as <drsmith>"}}`, nil), 200, "dentasoft")
	expect(t, do(t, "PUT", srv.URL+"/v1/projects/clinic/operations",
		`[{"name":"lookup_patient","description":"Look up a patient.","instructions":"Use DentaSoft.\n\nFind them.",
		  "input":{"type":"object","properties":{"patient_id":{"type":"string"}}},"output":{"type":"object"}}]`, nil), 200, "lookup_patient")

	for path, wants := range map[string][]string{
		"/projects": {"clinic", "1 operations · 1 programs"},
		"/projects/clinic": {
			"Look up a patient.",        // operation
			"Log in as &lt;drsmith&gt;", // program instructions, escaped
			`&#34;patient_id&#34;: {`,   // indented schema
			"No jobs yet.",
		},
		"/machines": {
			`C:\Apps\DentaSoft.exe`, // machine installation
			"--clinic main",
			"never connected",
		},
		"/": {`0 <small>/ 2</small>`}, // machines online: front-desk (synced) and dentist-front-desk (token)
		"/playground?project=clinic": {
			"lookup_patient",
			`&#34;patient_id&#34;: &#34;&#34;`, // an empty input to fill in, from the schema
		},
	} {
		for _, want := range wants {
			expect(t, do(t, "GET", srv.URL+path, "", page), 200, want)
		}
	}
	for _, path := range []string{"/", "/projects", "/machines", "/jobs", "/playground", "/keys"} {
		for _, method := range []string{"PUT", "DELETE"} {
			if res := do(t, method, srv.URL+path, "", nil); res.StatusCode != 405 {
				t.Fatalf("%s %s: want 405, got %d", method, path, res.StatusCode)
			}
		}
	}
}

// "Add demo project" installs the DentaSoft demo for this user only; the Playground starts
// with its sample input and queues real jobs through the job service.
func TestDemoAndPlayground(t *testing.T) {
	box := newBox(t)
	owner := newOwner(t, box.db)
	u := box.srv.URL
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, form url.Values, origin string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", u+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", owner.cookie)
		req.Header.Set("Origin", origin)
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	page := map[string]string{"Cookie": owner.cookie}

	expect(t, do(t, "GET", u+"/playground", "", page), 200, "Nothing to run yet")
	expect(t, post("/dashboard/demo", nil, "https://evil.example"), 403, "")
	res := post("/dashboard/demo", nil, u)
	if res.StatusCode != 303 || res.Header.Get("Location") != "/playground?project=demo-project" {
		t.Fatalf("add demo: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	expect(t, do(t, "GET", u+"/playground", "", page), 200, `&#34;patient_id&#34;: &#34;P-1004&#34;`)
	expect(t, do(t, "GET", u+"/machines", "", page), 200, `%SONIC_AGENT_DIR%\demo\DentaSoft.exe`)
	expect(t, do(t, "GET", u+"/projects/demo-project", "", page), 200, "update_balance")
	expect(t, do(t, "GET", u+"/", "", page), 200, "2 of 4") // demo added; the test owner already has an API key

	run := url.Values{"project": {"demo-project"}, "operation": {"lookup_patient"}, "machine": {"demo-machine"}}
	run.Set("input", `{"patient_id": `) // not JSON
	expect(t, post("/dashboard/playground/run", run, u), 400, "must be a JSON object")
	run.Set("input", `{"patient_id": "nope"}`) // fails the operation's input schema
	expect(t, post("/dashboard/playground/run", run, u), 400, "patient_id")
	run.Set("input", `{"patient_id": "P-1004"}`)
	expect(t, post("/dashboard/playground/run", run, u), 400, "demo-machine is not connected")
	token, _ := box.db.IssueMachineToken(context.Background(), owner.id, "demo-machine")
	// Names are unique: adding one that already has a token is refused with the reason.
	expect(t, post("/dashboard/machines", url.Values{"name": {"demo-machine"}}, u), 400, "already exists")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fakeAgent(t, ctx, "ws"+strings.TrimPrefix(u, "http")+"/v1/agent", token, make(chan string, 10))
	waitFor(t, func() bool { return box.hub.Online(hub.Machine{User: owner.id, Name: "demo-machine"}) })
	res = post("/dashboard/playground/run", run, u)
	if res.StatusCode != 303 || !strings.HasPrefix(res.Header.Get("Location"), "/jobs/job_") {
		t.Fatalf("run: %d %s %s", res.StatusCode, res.Header.Get("Location"), regexp.MustCompile(`notice bad" role="alert">[^<]*`).FindString(body(t, res)))
	}
	job := res.Header.Get("Location")
	expect(t, do(t, "GET", u+job, "", page), 200, `P-1004`)
	expect(t, do(t, "GET", u+"/jobs", "", page), 200, strings.TrimPrefix(job, "/jobs/"))

	// Another user's demo is separate.
	other, _ := box.db.EnsureUser(context.Background(), "someone-else")
	session, _ := box.db.CreateSession(context.Background(), other, time.Hour)
	expect(t, do(t, "GET", u+"/projects", "", map[string]string{"Cookie": "sonic_session=" + session}), 200, "No projects yet")
}

// The logo files are public (the sign-in page shows them) and every page links to them.
func TestLogoFiles(t *testing.T) {
	box := newBox(t)
	u := box.srv.URL
	for path, contentType := range map[string]string{
		"/logo.svg": "image/svg+xml", "/favicon.svg": "image/svg+xml",
		"/favicon.ico": "image/x-icon", "/apple-touch-icon.png": "image/png",
	} {
		res := do(t, "GET", u+path, "", nil)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != contentType || res.ContentLength == 0 {
			t.Fatalf("%s: %d %s", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		res.Body.Close()
	}
	page := body(t, do(t, "GET", u+"/login", "", nil))
	for _, want := range []string{`href="/favicon.svg"`, `href="/favicon.ico"`, `href="/apple-touch-icon.png"`, `src="/logo.svg"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("sign-in page is missing %s", want)
		}
	}
}
