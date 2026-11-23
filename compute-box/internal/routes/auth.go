package routes

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
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

// OwnerLink is the default sign-in: the self-hosted owner opens a single-use link that the
// compute-box prints at startup (or `compute-box login-link` prints on demand).
type OwnerLink struct{ Store *store.Store }

func (OwnerLink) LoginPath() string { return "/login" }

func (o OwnerLink) Mount(mux *http.ServeMux, acc ext.Accounts) {
	// GET only shows a button: link previews and prefetchers must not use up the link.
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		web.Render(w, "login.html", map[string]any{"Token": r.URL.Query().Get("token")})
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		user, ok, err := o.Store.ConsumeLoginToken(r.Context(), r.FormValue("token"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			web.Render(w, "login.html", map[string]any{"Failed": true})
			return
		}
		if err := acc.StartSession(w, r, ext.UserID(user)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
