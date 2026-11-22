// Clinic patient-portal backend. Its patient data lives in DentaSoft, reached via the agent.
//
//	go run .
package main

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"os"

	"clinic/integrations"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /patients/{id}", integrations.Handle(getPatient))
	mux.HandleFunc("PUT /patients/{id}/balance", integrations.Handle(setBalance))
	integrations.Setup(context.Background(), mux, DentalClinic)

	addr := ":" + cmp.Or(os.Getenv("PORT"), "8000")
	log.Printf("clinic portal on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
