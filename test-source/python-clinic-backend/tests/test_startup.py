import json
import os
import unittest

import httpx

from dentasoft import dental_clinic, front_desk
from integrations.sonic import lifespan
from sonic_screwdriver import SonicError


class StartupTests(unittest.IsolatedAsyncioTestCase):
    def clients(self, handler):
        transport = httpx.MockTransport(handler)
        front_desk._client._http = httpx.AsyncClient(transport=transport, base_url="http://test")
        dental_clinic._http = httpx.AsyncClient(transport=transport, base_url="http://test")

    async def test_startup_syncs_machine_before_project_and_operations(self):
        requests = []

        def handle(request):
            requests.append((request.url.path, json.loads(request.content)))
            return httpx.Response(200, json={})

        self.clients(handle)
        async with lifespan(None):
            self.assertEqual([path for path, _ in requests], [
                f"/v1/machines/{front_desk.name}/programs",
                f"/v1/projects/{dental_clinic.project}/programs",
                f"/v1/projects/{dental_clinic.project}/operations",
            ])
            installation = requests[0][1]["dentasoft"]
            # By default the agent's own copy, resolved on its machine: no paths from here.
            if not os.getenv("DENTASOFT_SCRIPT") and not os.getenv("DENTASOFT_EXECUTABLE"):
                self.assertEqual(installation["executable"], r"%SONIC_AGENT_DIR%\demo\DentaSoft.exe")
            self.assertNotIn("--reset", installation.get("args", []))
            self.assertEqual(dental_clinic.machine, front_desk.name)
        self.assertIsNone(front_desk._client._http)
        self.assertIsNone(dental_clinic._http)

    async def test_failed_machine_sync_prevents_backend_startup(self):
        requests = []

        def handle(request):
            requests.append(request.url.path)
            return httpx.Response(503, json={"error": {"code": "unreachable", "message": "box unavailable"}})

        self.clients(handle)
        with self.assertRaises(SonicError):
            async with lifespan(None):
                self.fail("Backend started without syncing its machine")
        self.assertEqual(len(requests), 1)
        self.assertIsNone(front_desk._client._http)
        self.assertIsNone(dental_clinic._http)
