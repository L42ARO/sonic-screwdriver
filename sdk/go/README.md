# sonic-screwdriver (Go SDK)

## Project programs and machine installations

```go
frontDesk := sonic.NewMachine("dentist-front-desk", map[string]sonic.InstalledProgram{
    "dentasoft": {
        Executable: `C:\Program Files\DentaSoft\DentaSoft.exe`,
        WorkingDirectory: `C:\Program Files\DentaSoft`,
        WindowTitleContains: "DentaSoft",
        StartupTimeoutMS: 30000,
    },
})
clinic := sonic.NewProject("dental-clinic",
    sonic.WithMachine(frontDesk.Name),
    sonic.WithContext("Use DentaSoft for patient records."),
    sonic.WithPrograms(map[string]sonic.Program{
        "dentasoft": {Description: "Patient records, appointments and balances."},
    }),
)
// Define operations as usual, then sync at startup (handle errors in your application):
if err := frontDesk.Sync(ctx); err != nil { return err }
if err := clinic.Sync(ctx); err != nil { return err }
```

Both constructors accept `WithURL` and `WithAPIKey`. Machine sync replaces its complete
installation registry, independently of project operations; `frontDesk.Sync(ctx, true)`
explicitly permits clearing an empty registry. A nil project `Programs` map preserves
existing configuration; a non-nil empty map clears it. Project program and operation syncs
are separate requests. Paths refer to the named Windows machine. Sync only stores
configuration; the AI chooses which project programs to inspect or open during each job.

## Operations

Turn the tasks of a legacy program into typed Go operations. Input and output are plain
structs; their JSON and `jsonschema` tags become the schemas.

```go
var DentalClinic = sonic.NewProject("dental-clinic", sonic.WithMachine("dentist-front-desk"))

var UpdateBalance = sonic.Define[UpdateBalanceInput, BalanceChange](DentalClinic, sonic.Spec{
	Name:         "update_balance",
	Description:  "Set a patient's Balance Due.",
	Instructions: "Find the patient by Patient ID, click Edit, replace Balance Due, Save, answer Yes.",
})

err := DentalClinic.Sync(ctx)                                      // once, at startup
change, err := UpdateBalance.Run(ctx, UpdateBalanceInput{PatientID: "P-1004"})
```

- `Sync` makes the project's operations on the compute-box match the ones declared, in
  one call. It refuses to sync an empty project.
- `Run` returns `(O, error)`. Errors are `*sonic.Error` with a stable `Code`;
  `sonic.Code(err)` reads it.
- Per-call options: `sonic.IdempotencyKey`, `sonic.OnMachine`, `sonic.OnStep`.
- The input is checked by the compute-box against the schema; Go's type system covers the
  rest on the caller's side.
- `SONIC_URL` and `SONIC_API_KEY` environment variables configure the connection.

Module path `sonic-screwdriver/sdk/go`, package `sonic`. Apps in this repo use it through
a `replace` directive in their `go.mod`.
