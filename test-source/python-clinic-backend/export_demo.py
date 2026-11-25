"""Writes the demo project the dashboard's "Set up the demo" button creates.

Captures exactly what this example syncs at startup (machine programs, project programs
and operations) and saves it to compute-box/internal/demo/demo.json, so the dashboard's
demo and this example never drift apart. Re-run after changing dentasoft.py:

    .\\.venv\\Scripts\\python export_demo.py
"""
import asyncio
import json
import os
from pathlib import Path

import httpx

# The demo always points at the DentaSoft.exe shipped beside the agent.
for name in ("DENTASOFT_SCRIPT", "DENTASOFT_EXECUTABLE", "DENTASOFT_PYTHON"):
    os.environ.pop(name, None)

from dentasoft import dental_clinic, front_desk  # noqa: E402
from integrations.sonic import lifespan  # noqa: E402

OUT = Path(__file__).resolve().parents[2] / "compute-box" / "internal" / "demo" / "demo.json"

# Inputs the Playground starts with, so a first run works without typing anything.
# Patients come from test-target/dentasoft/patients.seed.json.
EXAMPLES = {
    "lookup_patient": {"patient_id": "P-1004"},
    "update_balance": {"patient_id": "P-1004", "balance": 120.5},
}


async def main():
    captured = {}

    def handle(request):
        path = request.url.path
        body = json.loads(request.content)
        if path.startswith("/v1/machines/"):
            captured["machine_programs"] = body
        elif path.endswith("/programs"):
            captured["project_programs"] = body
        elif path.endswith("/operations"):
            captured["operations"] = body
        return httpx.Response(200, json={})

    transport = httpx.MockTransport(handle)
    front_desk._client._http = httpx.AsyncClient(transport=transport, base_url="http://capture")
    dental_clinic._http = httpx.AsyncClient(transport=transport, base_url="http://capture")
    async with lifespan(None):
        pass
    missing = {"machine_programs", "project_programs", "operations"} - captured.keys()
    if missing:
        raise SystemExit(f"startup did not sync {sorted(missing)}")
    names = {op["name"] for op in captured["operations"]}
    if set(EXAMPLES) != names:
        raise SystemExit(f"EXAMPLES must cover exactly the operations {sorted(names)}")
    demo = {"project": dental_clinic.project, "machine": front_desk.name, **captured, "examples": EXAMPLES}
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(demo, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"wrote {OUT} ({len(captured['operations'])} operations)")


asyncio.run(main())
