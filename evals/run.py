"""Run evaluation cases against the real agent and check the results against ground truth.

For each case: restart the target app with fresh seed data, run the operation through the
compute-box, then compare the job's output with the expected output and the app's own
data file with the expected state. The compute-box and a connected local-agent must
already be running.

    python evals/run.py                         # every suite
    python evals/run.py stockpilot              # one suite
    python evals/run.py dentasoft -k balance    # cases whose name contains "balance" (-k repeatable)
    python evals/run.py --repeat 3              # each case three times

Only the standard library is used.
"""
import argparse
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SUITES = os.path.join(ROOT, "evals", "suites")
RESULTS = os.path.join(ROOT, "evals", "results")


# --------------------------------------------------------------------------- compute-box
class Box:
    def __init__(self, url, machine, api_key):
        self.url, self.machine = url.rstrip("/"), machine
        self.auth = {"Authorization": f"Bearer {api_key}"} if api_key else {}

    def call(self, method, path, body=None, timeout=30):
        req = urllib.request.Request(self.url + path, method=method,
                                     data=None if body is None else json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json", **self.auth})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return r.status, json.loads(r.read() or b"null")
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read() or b"null")

    def run_job(self, project, operation, input_, timeout=900):
        """Run one job and follow its event stream. Returns (final event name, data)."""
        req = urllib.request.Request(
            f"{self.url}/v1/projects/{project}/operations/{operation}/jobs", method="POST",
            data=json.dumps({"machine": self.machine, "input": input_}).encode(),
            headers={"Content-Type": "application/json", "Accept": "text/event-stream", **self.auth})
        try:
            resp = urllib.request.urlopen(req, timeout=timeout)
        except urllib.error.HTTPError as e:
            body = json.loads(e.read() or b"{}")
            return "rejected", {"error": body.get("error", {}), "http": e.code}
        name, last = None, ("ended", {})
        with resp:
            for raw in resp:
                line = raw.decode("utf-8").rstrip("\r\n")
                if line.startswith("event:"):
                    name = line[6:].strip()
                elif line.startswith("data:"):
                    data = json.loads(line[5:].strip())
                    if name in ("succeeded", "failed", "cancelled"):
                        last = (name, data)
        return last


# --------------------------------------------------------------------------- target app
def stop_app(script):
    base = os.path.basename(script).replace(".", r"\.")
    subprocess.run(["powershell", "-NoProfile", "-Command",
                    "Get-CimInstance Win32_Process -Filter \"Name='python.exe'\" | "
                    f"Where-Object {{ $_.CommandLine -match '{base}' }} | "
                    "ForEach-Object { Stop-Process -Id $_.ProcessId -Force }"],
                   capture_output=True)


def start_app(app, cold=False):
    script = os.path.join(ROOT, app["script"])
    stop_app(script)
    data = os.path.join(ROOT, app["data"])
    if os.path.exists(data):
        os.remove(data)
    if cold:
        return  # The model must discover and open the registered program itself.
    subprocess.Popen([sys.executable, script, "--reset"], cwd=os.path.dirname(script),
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(app.get("startup_seconds", 4))


def read_data(app):
    with open(os.path.join(ROOT, app["data"]), encoding="utf-8") as f:
        return json.load(f)


def read_seed(app):
    with open(os.path.join(ROOT, app["seed"]), encoding="utf-8") as f:
        return json.load(f)


# --------------------------------------------------------------------------- comparison
def norm(s):
    return " ".join(str(s).split()).casefold()


def same(expected, actual, path="output"):
    """Returns a list of differences. Dicts compare on the expected keys only; lists of
    objects compare as sets (order ignored); numbers within half a cent; strings ignore
    case and extra spaces."""
    if isinstance(expected, dict):
        if not isinstance(actual, dict):
            return [f"{path}: expected an object, got {json.dumps(actual)}"]
        diffs = []
        for k, v in expected.items():
            if k not in actual:
                if v in ("", None):
                    continue  # an empty expected value may be omitted
                diffs.append(f"{path}.{k}: missing (expected {json.dumps(v)})")
            else:
                diffs += same(v, actual[k], f"{path}.{k}")
        return diffs
    if isinstance(expected, list):
        if not isinstance(actual, list):
            return [f"{path}: expected a list, got {json.dumps(actual)}"]
        diffs, unmatched = [], list(actual)
        for item in expected:
            hit = next((a for a in unmatched if not same(item, a)), None)
            if hit is None:
                diffs.append(f"{path}: missing {json.dumps(item)}")
            else:
                unmatched.remove(hit)
        diffs += [f"{path}: unexpected {json.dumps(a)}" for a in unmatched]
        return diffs
    if isinstance(expected, bool) or expected is None:
        return [] if expected == actual else [f"{path}: expected {json.dumps(expected)}, got {json.dumps(actual)}"]
    if isinstance(expected, (int, float)):
        try:
            ok = abs(float(actual) - expected) < 0.005
        except (TypeError, ValueError):
            ok = False
        return [] if ok else [f"{path}: expected {expected}, got {json.dumps(actual)}"]
    return [] if norm(expected) == norm(actual) else [f"{path}: expected {json.dumps(expected)}, got {json.dumps(actual)}"]


def check_data(app, checks):
    """Checks the app's data file. No checks means it must be unchanged from the seed."""
    try:
        data = read_data(app)
    except FileNotFoundError:
        return ["data file missing"]
    if not checks:
        return [] if data == read_seed(app) else ["data changed, but this case should not write anything"]
    diffs = []
    for c in checks:
        rows = data if c["list"] == "" else data[c["list"]]
        hits = [r for r in rows if all(r.get(k) == v for k, v in c["where"].items())]
        where = json.dumps(c["where"])
        if not hits:
            diffs.append(f"data: no row {where} in {c['list'] or 'records'}")
            continue
        diffs += same(c["fields"], hits[-1], f"data{where}")
    return diffs


def evaluate(case, event, data, app):
    expect = case["expect"]
    diffs = []
    if event != expect["status"]:
        err = data.get("error", {})
        diffs.append(f"status: expected {expect['status']}, got {event} {err.get('code', '')}: {err.get('message', '')}".strip())
    elif event == "succeeded":
        diffs += same(expect.get("output", {}), data.get("output"))
    elif "error_code" in expect:
        code = data.get("error", {}).get("code")
        if code != expect["error_code"]:
            diffs.append(f"error code: expected {expect['error_code']}, got {code}: {data.get('error', {}).get('message', '')}")
    diffs += check_data(app, case.get("data"))
    return diffs


# --------------------------------------------------------------------------- main
def load_suite(name):
    d = os.path.join(SUITES, name)
    with open(os.path.join(d, "suite.json"), encoding="utf-8") as f:
        suite = json.load(f)
    with open(os.path.join(d, "operations.json"), encoding="utf-8") as f:
        suite["operations"] = json.load(f)
    with open(os.path.join(d, "cases.json"), encoding="utf-8") as f:
        suite["cases"] = json.load(f)
    suite["name"] = name
    return suite


def sync(box, suite):
    program_id = suite["name"]
    # Preserve other installed programs on this dedicated evaluation machine.
    status, installed = box.call("GET", f"/v1/machines/{box.machine}/programs")
    if status != 200:
        sys.exit(f"could not read machine programs: {status} {installed}")
    installed[program_id] = {
        # Windows venv python.exe may redirect to the base interpreter. These Tk
        # fixtures use only the standard library, so launch the actual UI host directly.
        "executable": getattr(sys, "_base_executable", sys.executable),
        "args": [os.path.join(ROOT, suite["app"]["script"])],
        "working_directory": os.path.dirname(os.path.join(ROOT, suite["app"]["script"])),
        "window_title_contains": suite["app"]["window_title_contains"],
        "startup_timeout_ms": 30000,
    }
    status, body = box.call("PUT", f"/v1/machines/{box.machine}/programs", installed)
    if status != 200:
        sys.exit(f"machine program sync failed: {status} {body}")
    status, body = box.call("PUT", f"/v1/projects/{suite['project']}/programs", {
        program_id: {"description": suite["app"]["description"]}
    })
    if status != 200:
        sys.exit(f"project program sync failed: {status} {body}")
    ops = []
    for op in suite["operations"]:
        op = dict(op)
        op["instructions"] = suite["context"] + "\n\n" + op["instructions"]
        ops.append(op)
    status, body = box.call("PUT", f"/v1/projects/{suite['project']}/operations", ops)
    if status != 200:
        sys.exit(f"sync of {suite['project']} failed: {status} {body}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("suites", nargs="*", help="suite names (default: all)")
    ap.add_argument("-k", action="append", default=[], help="only cases whose name contains this (repeatable)")
    ap.add_argument("--repeat", type=int, default=1)
    ap.add_argument("--cold-start", action="store_true", help="leave the target closed; the AI must open it")
    ap.add_argument("--configure-only", action="store_true", help="sync test program installations and operations without running jobs; run on the target machine")
    ap.add_argument("--url", default=os.getenv("SONIC_URL", "http://127.0.0.1:8080"))
    ap.add_argument("--machine", default=os.getenv("SONIC_MACHINE", "dentist-front-desk"))
    ap.add_argument("--api-key", default=os.getenv("SONIC_API_KEY"), help="defaults to SONIC_API_KEY")
    args = ap.parse_args()

    if not args.api_key:
        sys.exit("set SONIC_API_KEY (or --api-key) to an API key from the compute-box dashboard")
    box = Box(args.url, args.machine, args.api_key)
    try:
        status, body = box.call("GET", "/v1/machines")
    except OSError as e:
        sys.exit(f"compute-box not reachable at {args.url}: {e}")
    if not any(m["name"] == args.machine and m["online"] for m in body["machines"]):
        sys.exit(f"machine {args.machine} is not connected")

    names = args.suites or sorted(os.listdir(SUITES))
    results, started, stop = [], time.time(), False
    for name in names:
        if stop:
            break
        suite = load_suite(name)
        sync(box, suite)
        if args.configure_only:
            print(f"configured {name} on {box.machine}")
            continue
        cases = [c for c in suite["cases"] if not args.k or any(k.lower() in c["name"].lower() for k in args.k)]
        for case in cases * args.repeat:
            start_app(suite["app"], cold=args.cold_start)
            t0 = time.time()
            event, data = box.run_job(suite["project"], case["operation"], case["input"])
            secs = time.time() - t0
            diffs = evaluate(case, event, data, suite["app"])
            r = {"suite": name, "case": case["name"], "passed": not diffs, "diffs": diffs,
                 "job_id": data.get("job_id"), "steps": data.get("steps", 0), "seconds": round(secs, 1),
                 "cost_usd": data.get("cost_usd", 0.0), "event": event,
                 "output": data.get("output"), "error": data.get("error")}
            results.append(r)
            mark = "PASS" if r["passed"] else "FAIL"
            print(f"{mark}  {name:<10} {case['name']:<38} {r['steps']:>3} steps {secs:6.1f}s  "
                  f"${r['cost_usd']:.4f}  {r['job_id'] or ''}", flush=True)
            for d in diffs:
                print(f"        {d}", flush=True)
            if (data.get("error") or {}).get("code") == "budget_exceeded":
                print("budget reached: stopping")
                stop = True
                break
        stop_app(os.path.join(ROOT, suite["app"]["script"]))

    passed = sum(r["passed"] for r in results)
    if args.configure_only:
        return
    cost = sum(r["cost_usd"] for r in results)
    print(f"\n{passed}/{len(results)} passed, ${cost:.4f}, {time.time() - started:.0f}s")
    os.makedirs(RESULTS, exist_ok=True)
    out = os.path.join(RESULTS, datetime.now().strftime("%Y%m%d-%H%M%S") + ".json")
    with open(out, "w", encoding="utf-8") as f:
        json.dump(results, f, indent=2)
    print(f"report: {os.path.relpath(out, ROOT)}")
    sys.exit(0 if passed == len(results) else 1)


if __name__ == "__main__":
    main()
