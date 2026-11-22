// Package server runs a compute-box. It is the public entry point for programs that
// embed the compute-box: the stock binary in this module calls Run with the defaults, and
// other programs can call it with their own Options.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

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

// Options customize a compute-box. The zero value runs the stock server.
type Options struct {
	Config Config

	// Routes, if set, registers extra HTTP handlers next to the built-in ones. A pattern
	// that conflicts with a built-in route panics at startup.
	Routes func(mux *http.ServeMux)
}

// Run starts the compute-box and blocks until ctx is cancelled or the server fails.
func Run(ctx context.Context, opts Options) error {
	cfg := opts.Config
	token, err := config.AgentToken(cfg.DataDir, cfg.AgentToken)
	if err != nil {
		return fmt.Errorf("loading agent token: %w", err)
	}
	cfg.AgentToken = token
	fmt.Printf("Agent authentication token: %s\nEnter this in the local-agent setup. This is NOT an OpenAI API key.\n", token)

	db, err := store.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	ledger, err := budget.New(ctx, db, cfg.BudgetUSD, notify.New(cfg.NtfyTopic))
	if err != nil {
		return fmt.Errorf("loading budget: %w", err)
	}
	b, err := newBrain(cfg, ledger)
	if err != nil {
		return fmt.Errorf("brain: %w", err)
	}

	agents := hub.New(db, cfg.AgentToken)
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
			Extra: opts.Routes,
		}),
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	log.Printf("compute-box on %s (brain: %s), viewer at http://%s", cfg.Addr, b.Model(), cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// RotateAgentToken replaces the saved agent token and returns the new one. It refuses
// when SONIC_AGENT_TOKEN overrides the saved token.
func RotateAgentToken(cfg Config) (string, error) {
	if cfg.AgentToken != "" {
		return "", errors.New("SONIC_AGENT_TOKEN overrides the saved token; change that override to rotate it")
	}
	return config.RotateAgentToken(cfg.DataDir)
}

func newBrain(cfg Config, ledger *budget.Ledger) (brain.Brain, error) {
	if cfg.Brain == "scripted" {
		return &brain.Scripted{Dir: cfg.ScriptsDir}, nil
	}
	return brain.NewOpenAI(cfg.OpenAIKey, cfg.Model, cfg.Reasoning, ledger)
}
