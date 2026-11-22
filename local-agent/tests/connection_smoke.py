"""Local process integration test. No GUI input, desktop jobs, or model calls.

Build both binaries, then pass --box-exe and --agent-exe (absolute or relative paths).
All server data, credentials and connection names are temporary. No saved user settings
or startup registry entries are changed. The generated test token is never printed.
"""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request


def eventually(check, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if check():
                return
        except (OSError, ValueError):
            pass
        time.sleep(0.1)
    raise AssertionError("Timed out waiting for the local test connection")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--box-exe", type=Path, required=True)
    parser.add_argument("--agent-exe", type=Path, required=True)
    args = parser.parse_args()
    box_exe, agent_exe = args.box_exe.resolve(), args.agent_exe.resolve()
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        port = reservation.getsockname()[1]
    children = []
    with tempfile.TemporaryDirectory(prefix="sonic-connection-smoke-") as directory:
        root = Path(directory)
        env = dict(os.environ, SONIC_DATA_DIR=str(root / "data"), SONIC_BRAIN="scripted",
                   SONIC_ADDR=f"127.0.0.1:{port}", SONIC_AGENT_TOKEN="",
                   OPENAI_KEY="test-only-openai-key-must-never-be-printed")
        log = root / "box.log"
        with log.open("wb") as output:
            def start_box():
                child = subprocess.Popen([str(box_exe)], env=env, cwd=root,
                                         stdout=output, stderr=output,
                                         creationflags=subprocess.CREATE_NO_WINDOW)
                children.append(child)
                return child

            def connected():
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/v1/machines", timeout=1) as response:
                    return any(m["name"] == "setup-smoke-desk" and m["online"]
                               for m in json.load(response)["machines"])

            try:
                box = start_box()
                token_file = root / "data" / "agent-token"
                eventually(lambda: token_file.exists() and len(token_file.read_text().strip()) == 76)
                token = token_file.read_text().strip()
                agent_env = dict(env, SONIC_AGENT_TOKEN=token, SONIC_MACHINE="setup-smoke-desk",
                                 SONIC_AGENT_URL=f"http://127.0.0.1:{port}",
                                 SONIC_CONFIG_DIR=str(root / "agent"))
                agent = subprocess.Popen([str(agent_exe), "--no-overlay", "connect"], env=agent_env,
                                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                         creationflags=subprocess.CREATE_NO_WINDOW)
                children.append(agent)
                eventually(connected)
                repeated = subprocess.run([str(agent_exe)], env=agent_env, timeout=5,
                                          creationflags=subprocess.CREATE_NO_WINDOW)
                assert repeated.returncode == 0, "Second launch did not exit cleanly"
                assert agent.poll() is None, "Second launch stopped the original agent"
                box.terminate()
                box.wait(timeout=5)
                box = start_box()
                eventually(connected)
                assert token_file.read_text().strip() == token, "Server token changed on restart"
            finally:
                for child in reversed(children):
                    if child.poll() is None:
                        child.terminate()
                        child.wait(timeout=5)
        contents = log.read_text()
        assert "test-only-openai-key-must-never-be-printed" not in contents
        assert contents.count("Agent authentication token:") == 2
    print("PASS: local connection, repeated launch, server restart, stable token, automatic reconnection; no OpenAI key logged")


if __name__ == "__main__":
    main()
