// Package server runs a compute-box. It is the public entry point for programs that
// embed the compute-box: the stock binary in this module calls Run with the defaults, and
// other programs can call it with their own Options (see package ext for the extension
// points).
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/ext"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/config"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/routes"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/brain"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/budget"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/jobs"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/notify"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// Config holds the compute-box settings. ConfigFromEnv fills it from the environment.
type Config = config.Config

// ConfigFromEnv reads .env (if present) and then the environment; real variables win.
func ConfigFromEnv() Config { return config.Load() }

// Options customize a compute-box. The zero value runs the stock, single-owner server.
type Options struct {
	Config Config

	// SignIn decides how people sign in to the dashboard. Default: the single owner
	// signs in with a password (Config.OwnerPassword or set in the dashboard), or with a
	// single-use link from LoginLink.
	SignIn ext.SignIn

	// Limiter decides how much model usage each user may spend. Default: every user may
	// spend up to Config.BudgetUSD.
	Limiter ext.Limiter

	// Routes, if set, registers extra HTTP handlers next to the built-in ones. A pattern
	// that conflicts with a built-in route panics at startup.
	Routes func(mux *http.ServeMux)
}

// loginLinkTTL is how long a sign-in link from LoginLink stays valid.
const loginLinkTTL = 24 * time.Hour

// Run starts the compute-box and blocks until ctx is cancelled or the server fails.
func Run(ctx context.Context, opts Options) error {
	cfg := opts.Config
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	limiter := opts.Limiter
	if limiter == nil {
		limiter = budget.NewCapLimiter(db, cfg.BudgetUSD, notify.New(cfg.NtfyTopic))
	}
	ledger := budget.New(db, limiter)
	b, err := newBrain(cfg, ledger)
	if err != nil {
		return fmt.Errorf("brain: %w", err)
	}

	agents := hub.New(db)
	screensDir := filepath.Join(cfg.DataDir, "screens")
	jobService := jobs.New(db, agents, b, jobs.Options{
		ScreensDir: screensDir, JobTimeout: cfg.JobTimeout, MaxSteps: cfg.MaxSteps,
	})
	if err := jobService.Recover(ctx); err != nil {
		return fmt.Errorf("recovering jobs: %w", err)
	}

	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Addr:              cfg.Addr,
		Handler: routes.New(routes.Deps{
			Store: db, Hub: agents, Jobs: jobService, Ledger: ledger, ScreensDir: screensDir,
			SignIn: opts.SignIn, Extra: opts.Routes,
		}),
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	if opts.SignIn == nil {
		if err := ownerPassword(ctx, db, cfg); err != nil {
			return err
		}
	}
	log.Printf("compute-box on %s (brain: %s)", cfg.Addr, b.Model())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ownerPassword applies SONIC_OWNER_PASSWORD if the owner has no password yet, and says
// how to sign in. Credentials are never written to the log.
func ownerPassword(ctx context.Context, db *store.Store, cfg Config) error {
	owner := db.DefaultUserID()
	has, err := db.HasPassword(ctx, owner)
	if err != nil {
		return err
	}
	if !has && cfg.OwnerPassword != "" {
		if err := db.SetPassword(ctx, owner, cfg.OwnerPassword, ""); err != nil {
			return fmt.Errorf("SONIC_OWNER_PASSWORD: %w", err)
		}
		has = true
		log.Printf("dashboard password set from SONIC_OWNER_PASSWORD")
	}
	if has {
		log.Printf("dashboard: %s (sign in with the owner password)", baseURL(cfg))
	} else {
		log.Printf("dashboard: %s has no owner password yet: set SONIC_OWNER_PASSWORD, or run `compute-box login-link` for a one-time sign-in link", baseURL(cfg))
	}
	return nil
}

// LoginLink creates a new single-use sign-in link for the owner of a self-hosted
// compute-box. It can run while the server is running.
func LoginLink(ctx context.Context, cfg Config) (string, error) {
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		return "", err
	}
	defer db.Close()
	return loginLink(ctx, db, cfg)
}

func loginLink(ctx context.Context, db *store.Store, cfg Config) (string, error) {
	token, err := db.CreateLoginToken(ctx, db.DefaultUserID(), loginLinkTTL)
	if err != nil {
		return "", err
	}
	return baseURL(cfg) + "/login?token=" + token, nil
}

// baseURL is SONIC_PUBLIC_URL, or a guess from the listen address for local use.
func baseURL(cfg Config) string {
	if cfg.PublicURL != "" {
		return cfg.PublicURL
	}
	host := cfg.Addr
	if strings.HasPrefix(host, ":") || strings.HasPrefix(host, "0.0.0.0:") {
		host = "localhost" + host[strings.LastIndex(host, ":"):]
	}
	return "http://" + host
}

func newBrain(cfg Config, ledger *budget.Ledger) (brain.Brain, error) {
	if cfg.Brain == "scripted" {
		return &brain.Scripted{Dir: cfg.ScriptsDir}, nil
	}
	return brain.NewOpenAI(cfg.OpenAIKey, cfg.Model, cfg.Reasoning, ledger)
}
