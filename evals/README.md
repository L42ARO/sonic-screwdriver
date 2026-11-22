# evals

Behavioural tests for the agent. Each case runs a real operation on a real target app
through the compute-box and the local-agent, then checks two things against ground truth:

- **the job's result**: the status (succeeded or failed), the output, or the error code;
- **the app's own data file**: the expected change for writes, or no change at all for
  reads and refused writes.

Before every case the target app is restarted from its seed data, so cases are
independent and each one starts from the app's first screen.

## Run

The compute-box (OpenAI brain) and a local-agent must already be running and connected.
Don't touch the computer during a run.

```
python evals/run.py                         # every suite
python evals/run.py stockpilot              # one suite
python evals/run.py dentasoft -k balance    # cases whose name contains "balance"
python evals/run.py --repeat 3              # each case three times, to measure consistency
python evals/run.py dentasoft --cold-start  # AI must discover and open the closed app
python evals/run.py dentasoft --configure-only # register installation and operations; no jobs
```

Run this runner on the target Windows machine: it registers the base Python executable
and absolute test-script paths as machine installations, preserving other program IDs.
Project programs and operations are synced before running cases. The default mode still
opens a freshly seeded app before each job. `--cold-start` only stops the app and removes
its live data, leaving launch to the model's application tools. Neither mode makes data
reset part of production application launch. Cold-start mode requires the OpenAI brain;
existing scripted click fixtures do not choose application tools themselves.

Each case prints PASS or FAIL with steps, seconds, cost and job id, followed by every
difference found. Open the job id in the viewer (`http://127.0.0.1:8080/jobs/<id>`) to see
each step's screenshot. A JSON report is written to `evals/results/`.

## Layout

```
run.py                      the runner (standard library only)
make_cases.py               writes each suite's cases.json from the apps' seed data
suites/<suite>/
  suite.json                project name, target app, shared context for every operation
  operations.json           the operations, as callers would sync them
  cases.json                generated: inputs and expected results
```

## Matching rules

- Objects are compared on the expected keys only; extra output fields are fine.
- Lists are compared as sets: order doesn't matter, but nothing may be missing or extra.
- Numbers match within half a cent. Strings ignore case and repeated spaces.
- An expected empty value may be left out of the output.

## Adding cases

Edit `make_cases.py` and rerun it (`python evals/make_cases.py`). Derive expected values
from the seed data rather than typing them, so they can't drift from the app.
