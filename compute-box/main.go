// compute-box: the brain of sonic-screwdriver. It serves the caller API, holds the
// connections from local-agents, and runs each job with a model that drives the screen.
//
//	go run .
//	go run . rotate-agent-token
//
// Programs that embed the compute-box import the server package instead.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/L42ARO/sonic-screwdriver/compute-box/server"
)

func main() {
	cfg := server.ConfigFromEnv()
	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "rotate-agent-token" {
			log.Fatal("usage: compute-box [rotate-agent-token]")
		}
		token, err := server.RotateAgentToken(cfg)
		if err != nil {
			log.Fatalf("rotating agent token: %v", err)
		}
		fmt.Printf("Agent authentication token: %s\nRestart the compute-box and update each local agent's settings.\n", token)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, server.Options{Config: cfg}); err != nil {
		log.Fatal(err)
	}
}
