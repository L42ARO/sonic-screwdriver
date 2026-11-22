// Patient endpoints of the clinic portal, backed by DentaSoft.
package main

import (
	"encoding/json"
	"net/http"

	"sonic-screwdriver/sdk/go"
)

func getPatient(w http.ResponseWriter, r *http.Request) error {
	patient, err := LookupPatient.Run(r.Context(), LookupPatientInput{PatientID: r.PathValue("id")})
	if err != nil {
		return err
	}
	if !patient.Found {
		return &sonic.Error{Code: "not_found", Message: "no patient " + r.PathValue("id")}
	}
	return writeJSON(w, patient)
}

func setBalance(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Balance   float64 `json:"balance"`
		RequestID string  `json:"request_id"` // lets callers retry safely
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return &sonic.Error{Code: "invalid_input", Message: err.Error()}
	}
	change, err := UpdateBalance.Run(r.Context(),
		UpdateBalanceInput{PatientID: r.PathValue("id"), Balance: body.Balance},
		sonic.IdempotencyKey(body.RequestID),
	)
	if err != nil {
		return err
	}
	return writeJSON(w, change)
}

func writeJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}
