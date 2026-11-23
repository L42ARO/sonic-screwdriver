// Package ext defines the extension points of a compute-box. A program that embeds the
// compute-box (see package server) can replace how people sign in to the dashboard and
// how much model usage each user may spend. Everything else — API keys, machine tokens,
// sessions, and scoping every request to its owner — is handled by the core.
package ext

import (
	"context"
	"net/http"
)

// UserID identifies an owner. Every project, machine, job and credential belongs to one.
type UserID int64

// Accounts is what the core offers a SignIn implementation.
type Accounts interface {
	// EnsureUser returns the user with this name, creating it if needed. Names are
	// unique; use a stable identifier such as "github:12345".
	EnsureUser(ctx context.Context, name string) (UserID, error)
	// StartSession signs the browser in as user and redirects it to the dashboard.
	StartSession(w http.ResponseWriter, r *http.Request, user UserID) error
}

// SignIn decides how a person signs in to the dashboard.
type SignIn interface {
	// LoginPath is where signed-out visitors are sent, e.g. "/login".
	LoginPath() string
	// Mount registers the sign-in handlers. Once a handler knows who the person is, it
	// calls accounts.StartSession.
	Mount(mux *http.ServeMux, accounts Accounts)
}

// Limiter decides how much model usage each user may spend. The core records every
// model call's cost; the limiter only decides.
type Limiter interface {
	// Allow returns an error if the user may not start another model call. The error
	// text is shown to the caller.
	Allow(ctx context.Context, user UserID) error
	// Budget reports what the user has spent and may spend (0 means no cap), in USD.
	Budget(ctx context.Context, user UserID) (spentUSD, capUSD float64, err error)
}
