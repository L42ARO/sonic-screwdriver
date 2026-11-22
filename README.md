# sonic-screwdriver

An API for software that has no API. A business calls a normal HTTP endpoint such as
"look up patient P-1004", and an AI agent carries it out on the legacy program the way a
person would: it looks at the screen and uses the mouse and keyboard. There is no
accessibility tree, database access or other shortcut. That is the point: the target is
software that nothing but a human (or a vision agent) can drive.

Status: prototype, working end to end. Callers in Python, TypeScript and Go can look up
and update patients in the DentaSoft test program through the compute-box and a
local-agent, with `gpt-6-luna` deciding every click. Windows only, one job at a time per
machine, everything running locally.

## How the pieces fit

```
 test-source (any client, anywhere)
        |  HTTP: sync operations, run one, results streamed (SSE)
        v
 compute-box (Go)  ---- OpenAI API (the "brain")
        |  WebSocket, opened outbound by the local-agent
        v
 local-agent (Rust) on the Windows machine
        |  screenshots in, mouse and keyboard out
        v
 legacy program (test-target/dentasoft for now)
```

- **local-agent** is the hands and eyes. It takes screenshots and performs clicks, typing
  and key presses. It has no intelligence and does exactly what it is told.
- **compute-box** is the brain. It holds the operation definitions, runs the model loop,
  sends actions to the local-agent and returns structured results to callers. It will run
  as a local server first and in Docker later, locally or in the cloud.
- **test-target** is a fake legacy program the agent practises on.
- **test-source** holds example client programs, in several languages, that call the API.

## Where everything is

| Path | What it is | Status |
|---|---|---|
| `local-agent/` | Rust executor for screen, mouse and keyboard | working, connects to the compute-box |
| `local-agent/README.md` | full action list, CLI, overlay, coordinate rules | |
| `local-agent/src/main.rs` | command line: `connect`, `screenshot`, `windows`, `exec` | |
| `local-agent/src/agent.rs` | connected mode: WebSocket client, reconnects forever | |
| `local-agent/src/protocol.rs` | action and result types sent over the wire | |
| `local-agent/src/executor.rs` | runs one action and returns a result with a screenshot | |
| `local-agent/src/screen.rs` | capture, downscale, zoom, wait-for-screen-to-settle | |
| `local-agent/src/input.rs` | mouse and keyboard injection, key chords and repeats | |
| `local-agent/src/win.rs` | DPI awareness, window listing and focus | |
| `local-agent/src/overlay.rs` | the "AGENT AT WORK" tab and edge glow shown while it runs | |
| `local-agent/scripts/dentasoft-smoke.json` | scripted test: log in, find a patient, edit, save | |
| `compute-box/` | Go server: caller API, agent hub, job queues, OpenAI brain, budget, viewer | working |
| `compute-box/README.md` | settings, layout, API, how a job runs, caching and cost | |
| `compute-box/.env` | `OPENAI_KEY` for the model calls. Secret: never commit it | |
| `test-target/dentasoft/` | DentaSoft, a fake 1998 dental records program | done |
| `test-target/dentasoft/README.md` | login, workflows, keyboard shortcuts | |
| `test-target/dentasoft/dentasoft.py` | the app: Python Tkinter, all drawn on one canvas, laggy | |
| `test-target/dentasoft/patients.seed.json` | the 12 starting patients | |
| `test-target/dentasoft/patients.json` | live data the app edits; recreated from the seed with `--reset` | |
| `sdk/python/` | Python SDK: operations as typed async functions, sync, SSE, errors | working |
| `sdk/typescript/` | TypeScript SDK: operations from Zod schemas | working |
| `sdk/go/` | Go SDK: operations from structs, with generics | working |
| `test-source/python-clinic-backend/` | sample caller: FastAPI clinic portal backed by DentaSoft | working |
| `test-source/python-clinic-backend/dentasoft.py` | DentaSoft's tasks declared as typed functions | |
| `test-source/python-clinic-backend/integrations/sonic.py` | the sample's sonic plumbing: sync, errors, health | |
| `test-source/typescript-clinic-backend/` | the same clinic backend in TypeScript, Express + Zod | working |
| `test-source/go-clinic-backend/` | the same clinic backend in Go, net/http + structs | working |

## Running it

Requirements: Windows, Go, Rust, Python 3, and an OpenAI key in `compute-box/.env` as
`OPENAI_KEY=...`.

```powershell
# 1. the compute-box (viewer at http://127.0.0.1:8080)
cd compute-box; go run .

# 2. the local-agent, on the machine with DentaSoft
cd local-agent; cargo build --release
.\target\release\local-agent.exe
# In setup: URL http://localhost:8080, machine dentist-front-desk,
# and the agent authentication token printed by the compute-box (not the OpenAI key).

# 3. Python sample: startup registers the machine installation and project automatically
# The agent can open DentaSoft when needed.
cd test-source\python-clinic-backend; .\.venv\Scripts\uvicorn main:app
curl http://localhost:8000/patients/P-1004
```

Don't touch the mouse or keyboard while the overlay is showing. To test the plumbing
without a model, start the compute-box with `SONIC_BRAIN=scripted`.

DentaSoft login: user `drsmith`, password `molar123`.

## Cloud deployment

For EC2 deployment, run `./deploy.ps1`; the cloud box is then reachable at
`http://<host>:6080`. See [the compute-box deployment instructions](compute-box/README.md#docker-on-ec2)
for the agent token, SDK URL, environment settings and persistent Docker data volume.

## Programs and installations

Projects declare relevant program IDs, descriptions and usage guidance. Machines hold
the executable paths, argument arrays, working directories and window matchers for those
IDs. Both configurations live in the compute-box's SQLite database; the local-agent
needs no registry. Python, TypeScript and Go SDKs can sync them independently.

During a job the AI chooses when to call `list_programs`, `list_running_applications`,
`open_program(program_id)` or `focus_window`. There is no automatic startup sequence.
The compute-box resolves an allowed project program against the selected machine's
installation. The executor reuses a matching window or launches the configured executable
directly, waits for its window and returns a screenshot. The AI handles login and loading
dialogs through vision. See the SDK READMEs for configuration examples.

For a real closed-app evaluation, run `python evals/run.py dentasoft --cold-start`.

## Decisions so far

- Vision only. The agent never reads a UI Automation or accessibility tree.
- Windows first. One dedicated machine per local-agent; a person only watches, and later
  gets a stop hotkey.
- OpenAI is the model provider. The agent's actions are our own tool definitions, so the
  local-agent's action set stays the same whatever model sits behind it.
- An operation is a named task with an input schema and an output schema, declared in the
  caller's own code and synced per project. Callers run it and get progress and the result
  as a Server-Sent Events stream.
- Recording and replaying click paths is a later speed and cost optimization, not part of
  the first version.

## Next steps

- Stop hotkey on the local-agent.
- Per-machine enrollment and caller authorization for a multi-user compute-box.
- Generated OpenAPI spec and the `sonic` CLI.
- Replay of recorded click paths, to cut cost and time on repeated operations.

## License

- The compute-box, local-agent, examples, test targets and evals are licensed under the
  [GNU Affero General Public License v3.0](LICENSE). If you modify them and distribute
  them, or let others use them over a network (for example as a hosted service), you
  must publish your modified source under the same license.
- The SDKs in [`sdk/`](sdk) are licensed under the [Mozilla Public License 2.0](sdk/go/LICENSE).
  Changes to the SDK files themselves must be shared, but applications that use the SDKs
  can be under any license, including proprietary.
