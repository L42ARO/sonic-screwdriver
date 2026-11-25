# Clinic Patient Portal (sample caller)

A small FastAPI backend for a dental clinic's patient portal. Its patient records live in
DentaSoft, a 1998 program with no API. The backend reads and updates them through the
sonic-screwdriver compute-box, which drives DentaSoft on the clinic's PC with a
computer-use agent.

This sample shows how we expect developers to use the system from a real codebase.

## Layout

```
main.py                the FastAPI app; one line wires in sonic
dentasoft.py           written by the developer: the project and DentaSoft's
                       tasks as typed async functions
integrations/
  sonic.py             sonic plumbing, never edited: startup sync, shutdown,
                       error handling, /health
routes/patients.py     GET /patients/{id}, PUT /patients/{id}/balance
```

The client library is the Python SDK in `sdk/python` at the repo root, installed like
any package. No client code lives in the app.

## Defining an operation

```python
dental_clinic = Sonic(project="demo-project", machine="demo-machine")

@dental_clinic.operation
async def update_balance(patient_id: PatientId, balance: Annotated[float, Field(ge=0)]) -> BalanceChange:
    """Set a patient's Balance Due.

    Find the patient by Patient ID and open their record. Click Edit, replace Balance Due,
    click Save, answer Yes, then re-read Balance Due to confirm it changed.
    """
```

The signature is the input, the return type is the output, and the docstring is the
description plus the agent's instructions. The body is never run. Nobody scripts the
clicks: the agent works them out from the screen.

Calling it is an ordinary function call:

```python
change = await update_balance("P-1004", 0)
change.new_balance   # 0.0
```

`dentasoft.py` declares both `front_desk = Machine(...)` with the installation paths and
`dental_clinic = Sonic(...)` with program guidance and operations. On startup,
`integrations/sonic.py` syncs the machine, project programs and operations. No separate
evaluation-runner configuration command is needed. A sync failure stops startup visibly.

## Run

Start the compute-box, add the machine `demo-machine` in its dashboard and connect the
local-agent with that machine token, then run from
this example's directory:

```powershell
python -m venv .venv
.\.venv\Scripts\pip install -r requirements.txt
$env:SONIC_URL = "http://localhost:8080"
$env:SONIC_API_KEY = "sonic_sk_..."   # from the dashboard: Create API key
$env:DENTASOFT_MACHINE = "demo-machine"
.\.venv\Scripts\uvicorn main:app --reload
```

Then `GET http://localhost:8000/patients/P-1004`, or open `http://localhost:8000/docs`.

By default the agent opens `DentaSoft.exe` from the release zip: the `demo` folder next to
`SonicScrew-Local-Agent.exe` (`%SONIC_AGENT_DIR%\demo\DentaSoft.exe`, expanded by the agent on its own
computer). Set `DENTASOFT_EXECUTABLE` if it is somewhere else. To run DentaSoft from this
source checkout instead, set `DENTASOFT_SCRIPT` to the absolute path of
`test-target\dentasoft\dentasoft.py` (and optionally `DENTASOFT_PYTHON` to the python.exe
that runs it). Paths are always on the agent's Windows computer. The agent can open
DentaSoft when needed; startup does not launch it or reset patient data. This demo declares the machine's
complete program registry; add other installations to `front_desk.programs` if sharing it.

## Compute-box API this sample relies on

| Call | Purpose |
|---|---|
| `PUT /v1/machines/{machine}/programs` | sync the machine's installation settings |
| `PUT /v1/projects/{project}/programs` | sync relevant program descriptions |
| `PUT /v1/projects/{project}/operations` | body: array of definitions; replaces the project's set, returns created/updated/removed/unchanged |
| `POST /v1/projects/{project}/operations/{name}/jobs` | body: `{"machine", "input"}`; optional `Idempotency-Key`; with `Accept: text/event-stream` streams the job |
| `GET /v1/machines` | `{"machines": [{"name", "online"}]}` |

Stream events: `queued {job_id}`, `step {n, description}`, `succeeded {job_id, output}`,
`failed {job_id, error: {code, message}}`, `cancelled`.

Errors before a job starts: HTTP 4xx/5xx with `{"error": {"code", "message"}}`.

## Endpoint errors

| Agent error code | This app returns |
|---|---|
| `not_found`, or lookup with `found: false` | 404 |
| `invalid_input` | 422 |
| `unreachable`, `machine_offline`, `machine_busy` | 503 |
| anything else, including `output_invalid` | 502 |

## Known shortcut

The DentaSoft login is written into the project context, so it is sent to the model.
Fine for a fake app; real credentials will need a proper secret mechanism.
