# Clinic Patient Portal, Go (sample caller)

The same clinic backend as the Python sample, written the way a Go developer would: the
standard library's router and plain structs. Patient records live in DentaSoft, a 1998
program with no API, reached through the sonic-screwdriver compute-box.

## Layout

```
main.go                the server; one line wires in sonic
dentasoft.go           written by the developer: the project and DentaSoft's
                       tasks as typed operations
patients.go            GET /patients/{id}, PUT /patients/{id}/balance
integrations/
  sonic.go             sonic plumbing, never edited: startup sync, /health,
                       error handling
```

In Go a folder is a package, so the handlers sit next to `dentasoft.go` in package
`main` rather than in a `routes/` folder.

## Defining an operation

```go
var DentalClinic = sonic.NewProject("dental-clinic-go", sonic.WithMachine("dentist-front-desk"))

type UpdateBalanceInput struct {
	PatientID string  `json:"patient_id" jsonschema:"pattern=^P-\\d{4}$"`
	Balance   float64 `json:"balance" jsonschema:"minimum=0"`
}

type BalanceChange struct {
	PreviousBalance float64 `json:"previous_balance"`
	NewBalance      float64 `json:"new_balance"`
}

var UpdateBalance = sonic.Define[UpdateBalanceInput, BalanceChange](DentalClinic, sonic.Spec{
	Name:         "update_balance",
	Description:  "Set a patient's Balance Due.",
	Instructions: `Find the patient by Patient ID, click Edit, replace Balance Due, click Save...`,
})
```

The structs are the schemas: JSON tags name the fields and `jsonschema` tags add rules.
Calling it returns a typed result and an error, like any Go function:

```go
change, err := UpdateBalance.Run(ctx, UpdateBalanceInput{PatientID: "P-1004", Balance: 0},
	sonic.IdempotencyKey("inv-5521"))
```

Handlers return errors and `integrations.Handle` turns sonic errors into the right HTTP
status.

## Run

```powershell
$env:SONIC_URL = "http://localhost:8080"
$env:SONIC_API_KEY = "sonic_sk_..."   # from the dashboard: Create API key
go run .
```

The compute-box is not built yet, so this sample can't do real work today.
