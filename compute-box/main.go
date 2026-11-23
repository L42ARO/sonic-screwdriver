// compute-box: the brain of sonic-screwdriver. It serves the caller API, holds the
// connections from local-agents, and runs each job with a model that drives the screen.
//
//	go run .              start the server
//	go run . login-link   print a one-time dashboard sign-in link (works while the server runs)
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
		if len(os.Args) != 2 || os.Args[1] != "login-link" {
			log.Fatal("usage: compute-box [login-link]")
		}
		link, err := server.LoginLink(context.Background(), cfg)
		if err != nil {
			log.Fatalf("creating sign-in link: %v", err)
		}
		fmt.Printf("Dashboard sign-in link (works once, for 24 hours):\n  %s\n", link)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, server.Options{Config: cfg}); err != nil {
		log.Fatal(err)
	}
}
