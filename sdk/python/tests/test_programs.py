import json
import unittest
import httpx

from sonic_screwdriver import Sonic, Machine, Program, InstalledProgram, SonicError


class ProgramSyncTests(unittest.IsolatedAsyncioTestCase):
    async def test_machine_and_project_sync_are_separate(self):
        requests = []

        def handle(request):
            requests.append((request.url.path, json.loads(request.content)))
            return httpx.Response(200, json={"created": []})

        transport = httpx.MockTransport(handle)
        machine = Machine("desk", programs={"records": InstalledProgram(
            executable=r"C:\Apps\records.exe", window_title_contains="Records", args=["a b", "x&y"]
        )})
        project = Sonic("clinic", machine=machine.name, programs={"records": Program("Patient records")})
        machine._client._http = httpx.AsyncClient(transport=transport, base_url="http://test")
        project._http = httpx.AsyncClient(transport=transport, base_url="http://test")
        try:
            await machine.sync()
            await project.sync(allow_empty=True)
            self.assertEqual([p for p, _ in requests], [
                "/v1/machines/desk/programs", "/v1/projects/clinic/programs", "/v1/projects/clinic/operations"
            ])
            self.assertEqual(requests[0][1]["records"]["args"], ["a b", "x&y"])
            self.assertNotIn("executable", requests[1][1]["records"])
            requests.clear()
            project.programs = None
            await project.sync(allow_empty=True)
            self.assertEqual(len(requests), 1)  # Existing clients preserve program config.
            project.programs = {}
            await project.sync(allow_empty=True)
            self.assertEqual(requests[1][1], {})  # Explicit clearing is distinct from omission.
        finally:
            await machine.aclose()
            await project.aclose()

    async def test_empty_machine_sync_requires_explicit_clear(self):
        machine = Machine("desk", programs={})
        with self.assertRaises(SonicError):
            await machine.sync()


if __name__ == "__main__":
    unittest.main()
