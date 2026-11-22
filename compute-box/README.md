# compute-box

The brain of sonic-screwdriver. It serves the caller API, holds the WebSocket connections
from local-agents, queues jobs per machine, and runs each job with a model that looks at
screenshots and drives the mouse and keyboard through the agent.

## Run

### Docker (self-hosting)

On any Linux server with Docker and Docker Compose:

```sh
cd compute-box
cp .env.example .env        # add OPENAI_KEY, or set SONIC_BRAIN=scripted to test without a model
docker compose up --build --detach --wait
docker compose exec compute-box cat /data/agent-token   # the token for your local-agents
```

The box listens on port 6080 on all interfaces (`SONIC_PORT` and `SONIC_PUBLISH_ADDRESS`
change that). Point the local-agent and `SONIC_URL` at `http://<server>:6080`; the
dashboard is at `/` and health is at `/healthz`. Traffic is plain HTTP and the caller API
has no authentication yet, so put it behind a firewall or an HTTPS reverse proxy before
exposing it beyond a network you trust.

SQLite, screenshots and the agent token live in the named `data` volume; keep it when
updating (`docker compose up --build --detach` replaces only the container). Use
`docker compose logs -f compute-box` to follow logs.

The image runs without root, has a read-only root filesystem, includes HTTPS CA roots,
and stores writable data only in its volume. `/healthz` is used for container health
checks. The build keeps Go's compiler cache in memory, so it also fits small servers.

### Local Go process

```powershell
go run .
```

Then open http://127.0.0.1:8080 for the job viewer. Start a local-agent with
the local-agent executable and configure its URL, machine name and the agent token printed
at server startup so jobs have somewhere to run.

On first startup the box generates a 256-bit agent token and saves it to
`SONIC_DATA_DIR/agent-token`. The same token is printed every startup. This secret is
independent of `OPENAI_KEY`, which is never printed or sent to agents. Preserve the data
directory across container/server restarts. `SONIC_AGENT_TOKEN` overrides the saved token.

To explicitly rotate a generated token:

```powershell
go run . rotate-agent-token
```

Restart the box after rotation and update local-agent settings. If an environment/.env
override is active, change that override instead. A malformed saved token fails startup
rather than silently disconnecting agents by generating another token.

For a cloud deployment, terminate HTTPS/WSS at a reverse proxy and forward the agent
WebSocket route to the box, including WebSocket upgrade support. The local agent connects
outbound; it needs no inbound port. This prototype still uses one shared agent token and
one owner; adding TLS does not add caller API authorization or per-machine enrollment.

## Settings

Read from the environment, then `.env` (the environment wins).

| Variable | Default | Meaning |
|---|---|---|
| `OPENAI_KEY` | | OpenAI API key. Never logged. |
| `SONIC_MODEL` | `gpt-6-luna` | Model for the OpenAI brain. Only priced models are accepted. |
| `SONIC_REASONING` | model default | Reasoning effort sent to the model. |
| `SONIC_BRAIN` | `openai` | `scripted` replays `scripts/<operation>.json` instead of using a model. |
| `SONIC_BUDGET_USD` | `3.00` | Hard cap on total model spend. New model calls are refused past it. |
| `SONIC_NTFY_TOPIC` | | ntfy.sh topic for alerts (budget cap reached). |
| `SONIC_ADDR` | `127.0.0.1:8080` | Listen address. Localhost only unless changed. |
| `SONIC_AGENT_TOKEN` | generated and persisted | Override for the shared agent authentication token. Printed at startup; unrelated to the OpenAI key. |
| `SONIC_DATA_DIR` | `data` | SQLite database and job screenshots. |
| `SONIC_JOB_TIMEOUT` | `10m` | A job running longer fails with `timeout`. |
| `SONIC_MAX_STEPS` | `40` | Most screen actions one job may take. |

## Layout

```
main.go                     the stock binary: reads the config and calls server.Run
server/                     public API: Run(ctx, Options), Config, Options.Routes
internal/                   everything else; not importable from other modules
  config/                   settings from the environment and .env
  store/                    SQLite: users, projects, operations, machines, jobs, steps, usage
  routes/                   HTTP handlers: caller API, agent endpoint, viewer
  services/
    hub/                    WebSocket connections from local-agents; send an action, get a result
    jobs/                   submit, per-machine queues, run with the brain, event streams
    brain/                  what to do on screen: OpenAI (Responses API) and scripted
    budget/                 pricing, cost per call, running total, the spending cap
    notify/                 ntfy.sh alerts
  utils/                    JSON errors, SSE writer, JSON Schema validation, ids
  web/                      viewer templates
scripts/                    fixtures for the scripted brain
```

## Caller API

| Call | Purpose |
|---|---|
| `PUT /v1/projects/{project}/operations` | Replace a project's operations with the posted array. All or nothing. |
| `GET /v1/projects/{project}/operations` | List them. |
| `POST /v1/projects/{project}/operations/{name}/jobs` | Run one. Body `{"machine", "input"}`, optional `Idempotency-Key`. With `Accept: text/event-stream` the job streams until it ends. |
| `GET /v1/jobs/{id}` | A job's status, output or error. |
| `GET /v1/jobs/{id}/events` | Replay a job's stream, for clients that dropped. |
| `GET /v1/machines` | Machines, whether online, queue length. |
| `PUT /v1/machines/{machine}/programs` | Replace a machine's installation map; creates an offline machine if needed. |
| `GET /v1/machines/{machine}/programs` | Read its installation map. |
| `PUT /v1/projects/{project}/programs` | Replace project program descriptions and usage guidance; creates the project if needed. |
| `GET /v1/projects/{project}/programs` | Read its relevant programs. |
| `GET /v1/usage` | Tokens and spend so far. |

Stream events: `queued`, `started`, `step {n, description}`, then `succeeded {output}` or
`failed {error: {code, message}, outcome_unknown}`. Errors before a job starts are HTTP
4xx/5xx with `{"error": {"code", "message"}}`.

Error codes: `invalid_input`, `unknown_operation`, `machine_offline`, `not_found`,
`agent_gave_up`, `output_invalid`, `machine_disconnected`, `timeout`, `budget_exceeded`,
`idempotency_conflict`.

Program configuration endpoints accept JSON objects keyed by program ID. A project entry
has `description` (required) and `instructions` (optional). A machine entry has
`executable` (absolute Windows path), `args` (array), `working_directory` (optional absolute
path), `window_title_contains` (required), and `startup_timeout_ms` (default 30000,
maximum 120000). Paths refer to the target Windows machine even when this server runs
elsewhere. `PUT` replaces only that registry; `{}` explicitly clears it and `null` is
rejected. Operation sync never changes program configuration. An unknown owner's GET
returns an empty map.

The running job takes a snapshot of both registries when execution begins. The model's
`list_programs` result contains project guidance and a `configured` flag, without launch
settings. `list_running_applications` returns live process/window metadata from Windows.
`open_program` accepts only a program ID: the job environment checks project membership,
resolves the machine installation, and sends a `launch_program` executor action. Raw
launch actions from a brain are rejected. These calls are recorded in the normal step
stream. Their recoverable tool errors include `program_not_in_project`,
`application_not_configured`, `launch_failed`, `startup_timeout`, and
`application_ambiguous`; the AI may inspect, wait, or end the job with a reason.

## How a job runs

1. The input is checked against the operation's input schema, and the machine must be
   connected. Otherwise the call fails immediately.
2. The job waits in its machine's queue. Jobs on one machine run one at a time.
3. The brain gets a first screenshot, then loops: call the model, run the action it
   chose on the machine, send back the new screenshot. Every action is saved as a step
   with its screenshot and streamed to the caller.
   The model can inspect running applications and open configured project programs as
   needed during this loop. Applications need not be open beforehand.
4. The model ends by calling `finish` with output, which must match the output schema,
   or `fail` with a code and reason.

## Prompt caching and cost

The model input is ordered so everything that never changes for an operation comes
first (the rules, the tool list, the operation's description and instructions), then the
job's input and first screenshot, then a conversation that only grows at the end. Earlier
items are never edited. In practice nearly every token after the first call is a cache
hit.

Each model call records input, cached, cache-write, output and reasoning tokens. Cost uses
the agreed `CalculateTokenCost`. OpenAI also bills cache writes at 1.25x the input rate,
which that formula doesn't include, so the budget cap is enforced on whichever is higher:
the agreed figure or one that adds the write premium. Both are stored per call.

## Tests

```powershell
go test ./...
```

`internal/routes/e2e_test.go` runs the whole caller path against a pretend agent with the
scripted brain. `internal/services/budget/pricing_test.go` pins the cost formula.
`server/server_test.go` runs the server the way an embedding program would.

## Embedding

Other Go programs can run the compute-box with their own additions:

```go
import "github.com/L42ARO/sonic-screwdriver/compute-box/server"

cfg := server.ConfigFromEnv()
err := server.Run(ctx, server.Options{
    Config: cfg,
    Routes: func(mux *http.ServeMux) { mux.HandleFunc("GET /extra", handler) },
})
```

Only the `server` package is public; `internal/` may change between releases.
