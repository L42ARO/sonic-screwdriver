# sonic-screwdriver (Python SDK)

## Project programs and machine installations

```python
from sonic_screwdriver import Sonic, Machine, InstalledProgram, Program

front_desk = Machine("dentist-front-desk", programs={
    "dentasoft": InstalledProgram(
        executable=r"C:\Program Files\DentaSoft\DentaSoft.exe",
        window_title_contains="DentaSoft",
        args=[],
        working_directory=r"C:\Program Files\DentaSoft",
    ),
})
clinic = Sonic(
    project="dental-clinic",
    machine=front_desk.name,
    context="Use DentaSoft for patient records.",
    programs={"dentasoft": Program(
        description="Patient records, appointments and balances.",
        instructions="Open when patient information is needed; log in if prompted.",
    )},
)
# Declare operations with @clinic.operation as usual, then at startup:
await front_desk.sync()
await clinic.sync()
# At shutdown:
await front_desk.aclose()
await clinic.aclose()
```

These syncs store configuration in the compute-box; neither opens an app. During jobs
the AI can inspect running applications and choose when to open a project program by ID.
Paths are absolute paths on the named Windows machine. Use the same `url` / `api_key`
settings for both objects when overriding the environment defaults. For a Python test
app, configure the absolute Python executable and pass the absolute script path in `args`.

Machine sync replaces that machine's complete program registry; configure it once for all
projects sharing the machine. An empty registry requires `sync(allow_empty=True)`.
Project `programs=None` (the default) preserves existing configuration; `programs={}`
explicitly clears it. Program and operation syncs are separate HTTP requests. The existing
`context` behavior is unchanged: it is included in each operation's instructions.

## Operations

Turn the tasks of a legacy program into typed async functions.

```python
from typing import Annotated
from pydantic import BaseModel, Field
from sonic_screwdriver import Sonic

dentasoft = Sonic(project="dental-clinic", machine="dentist-front-desk")

class BalanceChange(BaseModel):
    previous_balance: float
    new_balance: float

@dentasoft.operation
async def update_balance(patient_id: str, balance: Annotated[float, Field(ge=0)]) -> BalanceChange:
    """Set a patient's Balance Due.

    Find the patient by Patient ID, click Edit, replace Balance Due, click Save,
    answer Yes, then re-read Balance Due to confirm it changed.
    """

await dentasoft.sync()                                  # once, at startup
change = await update_balance("P-1004", balance=0)      # typed result
```

- The **signature** is the input, the **return type** is the output, the **docstring** is
  the description (first paragraph) and the agent's instructions (the rest). The body is
  never run.
- `sync()` makes the project's operations on the compute-box match the ones defined in
  code, in one call. Safe to repeat.
- Calls validate arguments before sending and validate the result before returning.
  Failures raise `SonicError` with a stable `code`.
- Per-call options: `await update_balance.with_options(idempotency_key="inv-5521")(...)`.
- `SONIC_URL` and `SONIC_API_KEY` environment variables configure the connection. Without
  `SONIC_URL`, the SDK uses the hosted compute-box at `https://app.sonicscrew.click`; set it (for
  example `http://localhost:8080`) to use your own.

Install locally: `pip install -e sdk/python`.

`dentasoft.definitions()` returns the exact JSON that `sync()` sends, if you want to
write it to files instead.

## Layout

- `client.py` the `Sonic` project object: declaring, syncing, running jobs
- `operation.py` turns a typed, documented function into an operation
- `errors.py` the one exception type, `SonicError`
- `_sse.py` reads the job's event stream
