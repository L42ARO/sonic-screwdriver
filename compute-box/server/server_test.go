package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/ext"
	"github.com/L42ARO/sonic-screwdriver/compute-box/server"
)

// An embedding program (like a hosted layer) can run the stock server with extra routes
// and stop it by cancelling the context.
func TestRunServesExtraRoutesAndStopsOnCancel(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	cfg := server.Config{
		Addr: addr, DataDir: t.TempDir(), Brain: "scripted", ScriptsDir: t.TempDir(),
		JobTimeout: time.Minute, MaxSteps: 10,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Options{Config: cfg, Routes: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /hosted/hello", func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, "hello from the embedding program")
			})
		}})
	}()

	var body string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		res, err := http.Get("http://" + addr + "/hosted/hello")
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		body = string(b)
		break
	}
	if body != "hello from the embedding program" {
		t.Fatalf("extra route: got %q", body)
	}
	if res, err := http.Get("http://" + addr + "/healthz"); err != nil || res.StatusCode != 200 {
		t.Fatalf("built-in route: %v %v", res, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

// customSignIn stands in for a hosted sign-in (e.g. GitHub): it creates a user by name
// and starts a session for them.
type customSignIn struct{}

func (customSignIn) LoginPath() string { return "/hosted/login" }
func (customSignIn) Mount(mux *http.ServeMux, acc ext.Accounts) {
	mux.HandleFunc("GET /hosted/login", func(w http.ResponseWriter, r *http.Request) {
		user, err := acc.EnsureUser(r.Context(), "github:"+r.URL.Query().Get("as"))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		acc.StartSession(w, r, user)
	})
}

// An embedding program can replace how people sign in; the default link sign-in is gone.
func TestCustomSignInReplacesTheDefault(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	cfg := server.Config{Addr: addr, DataDir: t.TempDir(), Brain: "scripted", ScriptsDir: t.TempDir(), JobTimeout: time.Minute, MaxSteps: 10}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Run(ctx, server.Options{Config: cfg, SignIn: customSignIn{}})

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var res *http.Response
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if res, err = noRedirect.Get("http://" + addr + "/"); err == nil {
			break
		}
	}
	if err != nil || res.StatusCode != 303 || res.Header.Get("Location") != "/hosted/login" {
		t.Fatalf("signed-out visitors should go to the custom sign-in: %v %v", res, err)
	}
	if res, _ := http.Get("http://" + addr + "/login"); res.StatusCode != 404 {
		t.Fatalf("default sign-in still mounted: %d", res.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	res, err = browser.Get("http://" + addr + "/hosted/login?as=octocat")
	if err != nil || res.StatusCode != 200 || res.Request.URL.Path != "/" {
		t.Fatalf("custom sign-in did not land on the dashboard: %v %v", res, err)
	}
}

// SONIC_OWNER_PASSWORD sets the owner's password on first start, but never overrides
// one that is already set (e.g. changed in the dashboard).
func TestOwnerPasswordFromConfig(t *testing.T) {
	data := t.TempDir()
	start := func(password string) (addr string, stop func()) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr = l.Addr().String()
		l.Close()
		cfg := server.Config{Addr: addr, DataDir: data, Brain: "scripted", ScriptsDir: t.TempDir(),
			JobTimeout: time.Minute, MaxSteps: 10, OwnerPassword: password}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- server.Run(ctx, server.Options{Config: cfg}) }()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if res, err := http.Get("http://" + addr + "/healthz"); err == nil {
				res.Body.Close()
				break
			}
		}
		return addr, func() { cancel(); <-done }
	}
	signIn := func(addr, password string) int {
		req, _ := http.NewRequest("POST", "http://"+addr+"/login", strings.NewReader(url.Values{"password": {password}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://"+addr)
		res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	addr, stop := start("first password")
	if got := signIn(addr, "first password"); got != 303 {
		t.Fatalf("sign-in with SONIC_OWNER_PASSWORD: %d", got)
	}
	stop()
	addr, stop = start("second password")
	defer stop()
	if got := signIn(addr, "second password"); got != 401 {
		t.Fatalf("a later SONIC_OWNER_PASSWORD replaced the set password: %d", got)
	}
	if got := signIn(addr, "first password"); got != 303 {
		t.Fatalf("the original password stopped working: %d", got)
	}
}
