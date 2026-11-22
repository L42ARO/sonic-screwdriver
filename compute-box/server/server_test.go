package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

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
