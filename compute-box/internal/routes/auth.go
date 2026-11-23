package routes

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/ext"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/web"
)

const (
	sessionCookie = "sonic_session"
	sessionTTL    = 30 * 24 * time.Hour
)

type userKey struct{}

// userOf returns the user a request was authenticated as. Only handlers wrapped by api
// or dashboard are called, so it is always set.
func userOf(r *http.Request) int64 { return r.Context().Value(userKey{}).(int64) }

func withUser(r *http.Request, user int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userKey{}, user))
}

// api requires an API key: Authorization: Bearer <key>.
func (d Deps) api(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		var user int64
		if ok && key != "" {
			var err error
			if user, ok, err = d.Store.UserByAPIKey(r.Context(), strings.TrimSpace(key)); err != nil {
				utils.WriteError(w, err)
				return
			}
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="compute-box"`)
			utils.WriteError(w, utils.NewError(http.StatusUnauthorized, "unauthorized",
				"missing or invalid API key: create one in the dashboard and send it as Authorization: Bearer <key> (the SDKs read SONIC_API_KEY)"))
			return
		}
		h(w, withUser(r, user))
	}
}

// dashboard requires a signed-in browser. Signed-out page views go to the sign-in page;
// other requests get 401. Form posts must come from this site.
func (d Deps) dashboard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := d.sessionUser(r)
		if !ok {
			if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/screens/") {
				http.Redirect(w, r, d.SignIn.LoginPath(), http.StatusSeeOther)
			} else {
				http.Error(w, "sign in first", http.StatusUnauthorized)
			}
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		h(w, withUser(r, user))
	}
}

func (d Deps) sessionUser(r *http.Request) (int64, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return 0, false
	}
	user, ok, err := d.Store.UserBySession(r.Context(), c.Value)
	if err != nil {
		log.Printf("auth: reading session: %v", err)
	}
	return user, ok
}

// sameOrigin reports whether a state-changing browser request came from this site's own
// pages. Browsers send Origin on form posts; Referer is the fallback.
func sameOrigin(r *http.Request) bool {
	source := r.Header.Get("Origin")
	if source == "" || source == "null" {
		source = r.Header.Get("Referer")
	}
	u, err := url.Parse(source)
	return err == nil && source != "" && u.Host == r.Host
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// accounts is what sign-in implementations use to create users and sessions.
type accounts struct{ store *store.Store }

func (a accounts) EnsureUser(ctx context.Context, name string) (ext.UserID, error) {
	id, err := a.store.EnsureUser(ctx, name)
	return ext.UserID(id), err
}

func (a accounts) StartSession(w http.ResponseWriter, r *http.Request, user ext.UserID) error {
	session, err := a.store.CreateSession(r.Context(), int64(user), sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: session, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureRequest(r),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
	return nil
}

func (d Deps) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		d.Store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, d.SignIn.LoginPath(), http.StatusSeeOther)
}

// OwnerLink is the default sign-in for a self-hosted compute-box, which has one owner.
// The owner signs in with their password (from SONIC_OWNER_PASSWORD or set in the
// dashboard), or with a single-use link from `compute-box login-link`, which is also the
// way back in when the password is forgotten. Links are never written to the log.
type OwnerLink struct{ Store *store.Store }

func (OwnerLink) LoginPath() string { return "/login" }

func (o OwnerLink) Mount(mux *http.ServeMux, acc ext.Accounts) {
	throttle := newLoginThrottle()
	page := func(w http.ResponseWriter, r *http.Request, status int, data map[string]any) {
		data["HasPassword"], _ = o.Store.HasPassword(r.Context(), o.Store.DefaultUserID())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		web.Render(w, "login.html", data)
	}
	// GET only shows a button for a link: previews and prefetchers must not use it up.
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		page(w, r, http.StatusOK, map[string]any{"Token": r.URL.Query().Get("token")})
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		owner := o.Store.DefaultUserID()
		if token := r.FormValue("token"); token != "" {
			user, ok, err := o.Store.ConsumeLoginToken(r.Context(), token)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !ok {
				page(w, r, http.StatusUnauthorized, map[string]any{"LinkFailed": true})
				return
			}
			owner = user
		} else {
			client := clientAddress(r)
			if wait := throttle.blocked(client); wait > 0 {
				page(w, r, http.StatusTooManyRequests, map[string]any{"Wait": fmt.Sprintf("%d minutes", int(wait.Minutes())+1)})
				return
			}
			ok, err := o.Store.CheckPassword(r.Context(), owner, r.FormValue("password"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !ok {
				throttle.failed(client)
				log.Printf("auth: failed password sign-in from %s", client)
				page(w, r, http.StatusUnauthorized, map[string]any{"PasswordFailed": true})
				return
			}
			throttle.succeeded(client)
		}
		if err := acc.StartSession(w, r, ext.UserID(owner)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

// clientAddress is the connecting IP. X-Forwarded-For is ignored because anyone can set
// it; behind a reverse proxy all clients share the proxy's budget, which is stricter.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginThrottle locks out an address after repeated wrong passwords, so a password on
// a public port can't be guessed at speed.
type loginThrottle struct {
	mu       sync.Mutex
	failures map[string]*loginFailures
}

type loginFailures struct {
	count       int
	first, until time.Time
}

const (
	loginMaxFailures = 5
	loginWindow      = 15 * time.Minute
	loginLockout     = 15 * time.Minute
)

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{failures: map[string]*loginFailures{}}
}

// blocked returns how long the address must wait before trying again (0: it may try).
func (t *loginThrottle) blocked(addr string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for a, f := range t.failures { // forget old entries so the map can't grow without bound
		if now.After(f.until) && now.Sub(f.first) > loginWindow {
			delete(t.failures, a)
		}
	}
	if f := t.failures[addr]; f != nil && now.Before(f.until) {
		return f.until.Sub(now)
	}
	return 0
}

func (t *loginThrottle) failed(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	f := t.failures[addr]
	if f == nil || now.Sub(f.first) > loginWindow {
		f = &loginFailures{first: now}
		t.failures[addr] = f
	}
	f.count++
	if f.count >= loginMaxFailures {
		f.until = now.Add(loginLockout)
		f.count, f.first = 0, now
	}
}

func (t *loginThrottle) succeeded(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, addr)
}

// setPassword sets or changes the owner's dashboard password. Changing it needs the
// current one, and signs out every other browser.
func (d Deps) setPassword(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	has, err := d.Store.HasPassword(r.Context(), user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if has {
		ok, err := d.Store.CheckPassword(r.Context(), user, r.FormValue("current"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			d.showError(w, "The current password is wrong. Forgot it? Sign in with a link (compute-box login-link) and set a new one.")
			return
		}
	}
	password := r.FormValue("password")
	if password != r.FormValue("confirm") {
		d.showError(w, "The two new passwords don't match.")
		return
	}
	if len(password) < store.MinPasswordLength {
		d.showError(w, fmt.Sprintf("Passwords are at least %d characters.", store.MinPasswordLength))
		return
	}
	var session string
	if c, err := r.Cookie(sessionCookie); err == nil {
		session = c.Value
	}
	if err := d.Store.SetPassword(r.Context(), user, password, session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?password=saved#account", http.StatusSeeOther)
}
