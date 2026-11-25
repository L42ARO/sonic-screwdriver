// DentaSoft, the clinic's 1998 records program, as typed Go functions.
//
// DentaSoft has no API. Each operation below is carried out by an agent that looks at the
// screen of the clinic's PC and uses the mouse and keyboard. The structs are the contract;
// the instructions tell the agent what to do.
package main

import (
	"cmp"
	"os"

	sonic "github.com/L42ARO/sonic-screwdriver/sdk/go"
)

// DentalClinic is this app's project on the compute-box. SONIC_URL and SONIC_API_KEY set
// the connection.
var DentalClinic = sonic.NewProject("demo-project-go",
	sonic.WithMachine(cmp.Or(os.Getenv("DENTASOFT_MACHINE"), "demo-machine")),
	sonic.WithPrograms(map[string]sonic.Program{"dentasoft": {Description: "Patient records, appointments and balances."}}),
	sonic.WithContext(`Use DentaSoft Patient Manager; open the dentasoft program if needed. If its login box is showing,
		log in with user name drsmith and password molar123. DentaSoft is slow: after each
		action wait for 'Please wait...' to clear. Its text boxes only support typing and
		Backspace: shortcuts like Ctrl+A, Home or Delete do nothing.`),
)

type LookupPatientInput struct {
	PatientID string `json:"patient_id" jsonschema:"pattern=^P-\\d{4}$" jsonschema_description:"DentaSoft patient ID, e.g. P-1004"`
}

type Patient struct {
	Found           bool     `json:"found"`
	FirstName       string   `json:"first_name,omitempty"`
	LastName        string   `json:"last_name,omitempty"`
	DateOfBirth     string   `json:"date_of_birth,omitempty" jsonschema_description:"MM/DD/YYYY as shown"`
	Phone           string   `json:"phone,omitempty"`
	Insurance       string   `json:"insurance,omitempty"`
	Balance         *float64 `json:"balance,omitempty" jsonschema_description:"Balance Due in dollars"`
	NextAppointment string   `json:"next_appointment,omitempty" jsonschema_description:"MM/DD/YYYY, empty if none"`
	Allergies       string   `json:"allergies,omitempty"`
}

var LookupPatient = sonic.Define[LookupPatientInput, Patient](DentalClinic, sonic.Spec{
	Name:        "lookup_patient",
	Description: "Look up a patient's record by patient ID.",
	Instructions: `
		Type the patient ID into the Patient ID box in the Find Patient panel and press Find.
		Click the matching row under Results, then read the fields in the Patient Record panel,
		zooming in if any text is hard to read. If DentaSoft says no records were found,
		return found=false and leave the other fields empty.`,
})

type UpdateBalanceInput struct {
	PatientID string  `json:"patient_id" jsonschema:"pattern=^P-\\d{4}$"`
	Balance   float64 `json:"balance" jsonschema:"minimum=0" jsonschema_description:"New Balance Due in dollars"`
}

type BalanceChange struct {
	PreviousBalance float64 `json:"previous_balance"`
	NewBalance      float64 `json:"new_balance"`
}

var UpdateBalance = sonic.Define[UpdateBalanceInput, BalanceChange](DentalClinic, sonic.Spec{
	Name:        "update_balance",
	Description: "Set a patient's Balance Due.",
	Instructions: `
		Find the patient by Patient ID and open their record. Note the current Balance Due.
		Click Edit, clear the Balance Due field, type the new amount with two decimals, click
		Save, answer Yes to the confirmation and OK to the success message. Then re-read
		Balance Due and only report success if it shows the new amount. If the patient does
		not exist, fail with code not_found.`,
})
