// compute-box: the brain of sonic-screwdriver. It serves the caller API, holds the
// connections from local-agents, and runs each job with a model that drives the screen.
//
//	go run .
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"sonic-screwdriver/compute-box/src/config"
	"sonic-screwdriver/compute-box/src/routes"
	"sonic-screwdriver/compute-box/src/services/brain"
	"sonic-screwdriver/compute-box/src/services/budget"
	"sonic-screwdriver/compute-box/src/services/hub"
	"sonic-screwdriver/compute-box/src/services/jobs"
	"sonic-screwdriver/compute-box/src/services/notify"
	"sonic-screwdriver/compute-box/src/store"
)

func main() {
	cfg := config.Load()
	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "rotate-agent-token" {
			log.Fatal("usage: compute-box [rotate-agent-token]")
		}
		if cfg.AgentToken != "" {
			log.Fatal("SONIC_AGENT_TOKEN overrides the saved token; change that override to rotate it")
		}
		token, err := config.RotateAgentToken(cfg.DataDir)
		if err != nil {
			log.Fatalf("rotating agent token: %v", err)
		}
		fmt.Printf("Agent authentication token: %s\nRestart the compute-box and update each local agent's settings.\n", token)
		return
	}
	token, err := config.AgentToken(cfg.DataDir, cfg.AgentToken)
	if err != nil {
		log.Fatalf("loading agent token: %v", err)
	}
	cfg.AgentToken = token
	fmt.Printf("Agent authentication token: %s\nEnter this in the local-agent setup. This is NOT an OpenAI API key.\n", token)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	notifier := notify.New(cfg.NtfyTopic)
	ledger, err := budget.New(ctx, db, cfg.BudgetUSD, notifier)
	if err != nil {
		log.Fatalf("loading budget: %v", err)
	}
	b, err := newBrain(cfg, ledger)
	if err != nil {
		log.Fatalf("brain: %v", err)
	}

	agents := hub.New(db, cfg.AgentToken)
	screensDir := filepath.Join(cfg.DataDir, "screens")
	jobService := jobs.New(db, agents, b, jobs.Options{
		ScreensDir: screensDir, JobTimeout: cfg.JobTimeout, MaxSteps: cfg.MaxSteps,
	})
	if err := jobService.Recover(ctx); err != nil {
		log.Fatalf("recovering jobs: %v", err)
	}

	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Addr:              cfg.Addr,
		Handler: routes.New(routes.Deps{
			Store: db, Hub: agents, Jobs: jobService, Ledger: ledger, ScreensDir: screensDir,
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
		log.Fatal(err)
	}
}

func newBrain(cfg config.Config, ledger *budget.Ledger) (brain.Brain, error) {
	if cfg.Brain == "scripted" {
		return &brain.Scripted{Dir: cfg.ScriptsDir}, nil
	}
	return brain.NewOpenAI(cfg.OpenAIKey, cfg.Model, cfg.Reasoning, ledger)
}
