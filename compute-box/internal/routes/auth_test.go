package routes_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/routes"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/brain"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/budget"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/jobs"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

type testBox struct {
	srv *httptest.Server
	db  *store.Store
	hub *hub.Hub
}

func newBox(t *testing.T) testBox {
	t.Helper()
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	os.MkdirAll(scripts, 0o755)
	os.WriteFile(filepath.Join(scripts, "lookup.json"), []byte(`{"actions":[{"action":"click","x":1,"y":1}],"output":{"ok":true}}`), 0o644)
	db, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := hub.New(db)
	screens := filepath.Join(dir, "screens")
	js := jobs.New(db, h, &brain.Scripted{Dir: scripts}, jobs.Options{ScreensDir: screens, JobTimeout: time.Minute, MaxSteps: 10})
	srv := httptest.NewServer(routes.New(routes.Deps{
		Store: db, Hub: h, Jobs: js, Ledger: budget.New(db, budget.NewCapLimiter(db, 3, nil)), ScreensDir: screens,
	}))
	t.Cleanup(srv.Close)
	return testBox{srv, db, h}
}

func as(o testOwner) map[string]string {
	return map[string]string{"Authorization": "Bearer " + o.apiKey, "Cookie": o.cookie}
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return string(b)
}

// Two users with identically named projects and machines never see each other's data.
func TestUsersAreIsolated(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	aliceID, _ := box.db.EnsureUser(ctx, "alice")
	bobID, _ := box.db.EnsureUser(ctx, "bob")
	alice := withCredentials(t, box.db, testOwner{id: aliceID})
	bob := withCredentials(t, box.db, testOwner{id: bobID})
	u := box.srv.URL

	defs := `[{"name":"lookup","description":"Alice's lookup","instructions":"secret alice instructions","input":{"type":"object"},"output":{"type":"object"}}]`
	expect(t, do(t, "PUT", u+"/v1/projects/clinic/operations", defs, as(alice)), 200, "lookup")
	expect(t, do(t, "PUT", u+"/v1/projects/clinic/programs", `{"dentasoft":{"description":"alice program"}}`, as(alice)), 200, "alice program")
	expect(t, do(t, "PUT", u+"/v1/machines/dentist-front-desk/programs",
		`{"dentasoft":{"executable":"C:\\Alice\\D.exe","window_title_contains":"D"}}`, as(alice)), 200, "Alice")

	// Bob sees none of it, under the same names.
	expect(t, do(t, "GET", u+"/v1/projects/clinic/operations", "", as(bob)), 200, `{"operations":[]}`)
	expect(t, do(t, "GET", u+"/v1/projects/clinic/programs", "", as(bob)), 200, `{}`)
	expect(t, do(t, "GET", u+"/v1/machines/dentist-front-desk/programs", "", as(bob)), 200, `{}`)
	expect(t, do(t, "POST", u+"/v1/projects/clinic/operations/lookup/jobs", `{"machine":"dentist-front-desk"}`, as(bob)), 404, "unknown_operation")

	// Alice runs a job on her machine.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	actions := make(chan string, 10)
	go fakeAgent(t, cctx, "ws"+strings.TrimPrefix(u, "http")+"/v1/agent", alice.machineToken, actions)
	waitFor(t, func() bool { return box.hub.Online(hub.Machine{User: aliceID, Name: "dentist-front-desk"}) })
	if box.hub.Online(hub.Machine{User: bobID, Name: "dentist-front-desk"}) {
		t.Fatal("alice's agent made bob's machine of the same name online")
	}
	events := readEvents(t, do(t, "POST", u+"/v1/projects/clinic/operations/lookup/jobs", `{"machine":"dentist-front-desk"}`,
		map[string]string{"Authorization": "Bearer " + alice.apiKey, "Accept": "text/event-stream"}))
	if events[len(events)-1][0] != "succeeded" {
		t.Fatalf("alice's job: %v", events)
	}
	var queued struct{ JobID string `json:"job_id"` }
	json.Unmarshal([]byte(events[0][1]), &queued)
	jobID := queued.JobID

	// Bob can't reach her job, its events, its page or its screenshots.
	expect(t, do(t, "GET", u+"/v1/jobs/"+jobID, "", as(alice)), 200, jobID)
	expect(t, do(t, "GET", u+"/v1/jobs/"+jobID, "", as(bob)), 404, "not_found")
	expect(t, do(t, "GET", u+"/v1/jobs/"+jobID+"/events", "", as(bob)), 404, "not_found")
	expect(t, do(t, "GET", u+"/jobs/"+jobID, "", as(bob)), 404, "")
	page := body(t, do(t, "GET", u+"/jobs/"+jobID, "", as(alice)))
	shot := regexp.MustCompile(`/screens/[^"]+\.png`).FindString(page)
	if shot == "" {
		t.Fatalf("no screenshot on alice's job page")
	}
	expect(t, do(t, "GET", u+shot, "", as(alice)), 200, "PNG")
	expect(t, do(t, "GET", u+shot, "", as(bob)), 404, "")
	expect(t, do(t, "GET", u+shot, "", map[string]string{"Authorization": "Bearer " + alice.apiKey}), 401, "") // API keys don't open the dashboard

	// Lists, usage and the dashboard are per user.
	expect(t, do(t, "GET", u+"/v1/machines", "", as(alice)), 200, `"online":true`)
	expect(t, do(t, "GET", u+"/v1/machines", "", as(bob)), 200, `"online":false`)
	dashboard := body(t, do(t, "GET", u+"/", "", as(bob)))
	for _, leaked := range []string{"secret alice instructions", "alice program", `C:\Alice`, jobID} {
		if strings.Contains(dashboard, leaked) {
			t.Fatalf("bob's dashboard shows alice's %q", leaked)
		}
	}
	if !strings.Contains(body(t, do(t, "GET", u+"/", "", as(alice))), "secret alice instructions") {
		t.Fatal("alice's dashboard is missing her own project")
	}

	// Bob can't manage alice's machine; removing "his" machine of that name leaves hers alone.
	form := map[string]string{"Cookie": bob.cookie, "Origin": u, "Content-Type": "application/x-www-form-urlencoded"}
	expect(t, do(t, "POST", u+"/dashboard/machines/dentist-front-desk/remove", "", form), 200, "")  // 303 to the dashboard, followed
	if !box.hub.Online(hub.Machine{User: aliceID, Name: "dentist-front-desk"}) {
		t.Fatal("bob's remove disconnected alice's machine")
	}
	keys, _ := box.db.ListAPIKeys(ctx, aliceID)
	expect(t, do(t, "POST", u+"/dashboard/keys/"+itoa(keys[0].ID)+"/revoke", "", form), 200, "")
	expect(t, do(t, "GET", u+"/v1/machines", "", as(alice)), 200, `"name"`) // alice's key still works
}

// API keys, sign-in links, sessions and dashboard credential management.
func TestCredentials(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	u := box.srv.URL
	owner := box.db.DefaultUserID()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(method, path, form string, headers map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, u+path, strings.NewReader(form))
		if form != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// The caller API needs a valid key.
	for _, auth := range []string{"", "Bearer", "Bearer nope", "Basic abc"} {
		res := send("GET", "/v1/machines", "", map[string]string{"Authorization": auth})
		if b := body(t, res); res.StatusCode != 401 || !strings.Contains(b, "unauthorized") {
			t.Fatalf("auth %q: %d %s", auth, res.StatusCode, b)
		}
	}

	// Signed out: pages redirect to sign-in, everything else is refused.
	if res := send("GET", "/", "", nil); res.StatusCode != 303 || res.Header.Get("Location") != "/login" {
		t.Fatalf("signed-out dashboard: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res := send("POST", "/dashboard/keys", "name=x", map[string]string{"Origin": u}); res.StatusCode != 401 {
		t.Fatalf("signed-out post: %d", res.StatusCode)
	}

	// A sign-in link shows a button (so link previews can't use it up), works once, and
	// gives a session cookie.
	link, _ := box.db.CreateLoginToken(ctx, owner, time.Hour)
	if res := send("GET", "/login?token="+link, "", nil); res.StatusCode != 200 || !strings.Contains(body(t, res), `name="token" value="`+link) {
		t.Fatal("login page does not offer the token")
	}
	if res := send("POST", "/login", "token="+url.QueryEscape(link), map[string]string{"Origin": "https://evil.example"}); res.StatusCode != 403 {
		t.Fatalf("cross-site sign-in: %d", res.StatusCode)
	}
	res := send("POST", "/login", "token="+url.QueryEscape(link), map[string]string{"Origin": u})
	var cookie string
	for _, c := range res.Cookies() {
		if c.Name == "sonic_session" {
			cookie = c.Name + "=" + c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("weak session cookie: %+v", c)
			}
		}
	}
	if res.StatusCode != 303 || cookie == "" {
		t.Fatalf("sign-in: %d, cookie %q", res.StatusCode, cookie)
	}
	if res := send("POST", "/login", "token="+url.QueryEscape(link), map[string]string{"Origin": u}); res.StatusCode != 401 {
		t.Fatalf("sign-in link worked twice: %d", res.StatusCode)
	}
	if res := send("GET", "/", "", map[string]string{"Cookie": cookie}); res.StatusCode != 200 {
		t.Fatalf("dashboard with session: %d", res.StatusCode)
	}

	signedIn := map[string]string{"Cookie": cookie, "Origin": u}
	// Dashboard posts from another site are refused even with a valid session.
	if res := send("POST", "/dashboard/keys", "name=x", map[string]string{"Cookie": cookie, "Origin": "https://evil.example"}); res.StatusCode != 403 {
		t.Fatalf("cross-site post: %d", res.StatusCode)
	}

	// Create an API key: shown once, works, then revoke it.
	page := body(t, send("POST", "/dashboard/keys", "name=production", signedIn))
	key := regexp.MustCompile(`sonic_sk_[A-Za-z0-9_-]+`).FindString(page)
	if key == "" {
		t.Fatalf("key not shown: %s", page)
	}
	if res := send("GET", "/v1/machines", "", map[string]string{"Authorization": "Bearer " + key}); res.StatusCode != 200 {
		t.Fatalf("new key rejected: %d", res.StatusCode)
	}
	if strings.Contains(body(t, send("GET", "/", "", signedIn)), key) {
		t.Fatal("dashboard shows a full API key after creation")
	}
	keys, _ := box.db.ListAPIKeys(ctx, owner)
	send("POST", "/dashboard/keys/"+itoa(keys[0].ID)+"/revoke", "", signedIn)
	if res := send("GET", "/v1/machines", "", map[string]string{"Authorization": "Bearer " + key}); res.StatusCode != 401 {
		t.Fatalf("revoked key still works: %d", res.StatusCode)
	}

	// Add a machine: its token connects an agent. Regenerating drops that agent and the
	// old token stops working; removing deletes the machine.
	page = body(t, send("POST", "/dashboard/machines", "name=front-desk", signedIn))
	token := regexp.MustCompile(`sonic_mt_[A-Za-z0-9_-]+`).FindString(page)
	if token == "" {
		t.Fatalf("machine token not shown: %s", page)
	}
	if res := send("POST", "/dashboard/machines", "name=bad name", signedIn); res.StatusCode != 400 {
		t.Fatalf("invalid machine name: %d", res.StatusCode)
	}
	desk := hub.Machine{User: owner, Name: "front-desk"}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go fakeAgent(t, cctx, "ws"+strings.TrimPrefix(u, "http")+"/v1/agent", token, make(chan string, 1))
	waitFor(t, func() bool { return box.hub.Online(desk) })
	page = body(t, send("POST", "/dashboard/machines/front-desk/token", "", signedIn))
	if fresh := regexp.MustCompile(`sonic_mt_[A-Za-z0-9_-]+`).FindString(page); fresh == "" || fresh == token {
		t.Fatalf("regenerate did not issue a new token")
	}
	waitFor(t, func() bool { return !box.hub.Online(desk) })
	if res := send("POST", "/dashboard/machines/nobody/token", "", signedIn); res.StatusCode != 404 {
		t.Fatalf("token for unknown machine: %d", res.StatusCode)
	}
	send("POST", "/dashboard/machines/front-desk/remove", "", signedIn)
	if machines, _ := box.db.ListMachines(ctx, owner); len(machines) != 0 {
		t.Fatalf("machine not removed: %+v", machines)
	}

	// Signing out ends the session.
	send("POST", "/logout", "", signedIn)
	if res := send("GET", "/", "", map[string]string{"Cookie": cookie}); res.StatusCode != 303 {
		t.Fatalf("session survived sign-out: %d", res.StatusCode)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// The owner sets a password in the dashboard, then signs in with it; guessing is locked out.
func TestPasswordSignIn(t *testing.T) {
	box := newBox(t)
	owner := newOwner(t, box.db)
	u := box.srv.URL
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, form url.Values, cookie string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", u+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", u)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	signIn := func(password string) *http.Response {
		return post("/login", url.Values{"password": {password}}, "")
	}

	// No password yet: only links work, and the page says so.
	if page := body(t, do(t, "GET", u+"/login", "", nil)); strings.Contains(page, `name="password"`) {
		t.Fatal("password form shown before a password is set")
	}
	expect(t, signIn(""), 401, "Wrong password")

	// Setting it: length and confirmation are checked; another browser gets signed out.
	other, _ := box.db.CreateSession(context.Background(), owner.id, time.Hour)
	expect(t, post("/dashboard/password", url.Values{"password": {"short"}, "confirm": {"short"}}, owner.cookie), 400, "at least")
	expect(t, post("/dashboard/password", url.Values{"password": {"correct horse"}, "confirm": {"correct horsf"}}, owner.cookie), 400, "match")
	expect(t, post("/dashboard/password", url.Values{"password": {"correct horse"}, "confirm": {"correct horse"}}, owner.cookie), 303, "")
	expect(t, do(t, "GET", u+"/", "", map[string]string{"Cookie": owner.cookie}), 200, "Change password")
	if _, ok, _ := box.db.UserBySession(context.Background(), other); ok {
		t.Fatal("setting the password left other browsers signed in")
	}
	// Changing it needs the current one.
	expect(t, post("/dashboard/password", url.Values{"current": {"guess"}, "password": {"new password!"}, "confirm": {"new password!"}}, owner.cookie), 400, "current password is wrong")

	// Signing in with it.
	expect(t, do(t, "GET", u+"/login", "", nil), 200, `name="password"`)
	expect(t, signIn("wrong password"), 401, "Wrong password")
	res := signIn("correct horse")
	if res.StatusCode != 303 || !strings.HasPrefix(res.Header.Get("Set-Cookie"), "sonic_session=sonic_ss_") {
		t.Fatalf("password sign-in: %d %v", res.StatusCode, res.Header)
	}
	session := strings.SplitN(res.Header.Get("Set-Cookie"), ";", 2)[0]
	expect(t, do(t, "GET", u+"/", "", map[string]string{"Cookie": session}), 200, "API keys")

	// Five wrong guesses lock the address out, even for the right password.
	for range 5 {
		expect(t, signIn("guess"), 401, "")
	}
	expect(t, signIn("correct horse"), 429, "Too many wrong passwords")
	// A sign-in link still works during the lockout.
	link, _ := box.db.CreateLoginToken(context.Background(), owner.id, time.Hour)
	expect(t, post("/login", url.Values{"token": {link}}, ""), 303, "")
}
