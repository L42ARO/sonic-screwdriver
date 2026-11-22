"""Runner setup tests; never launch, stop, or reset a real application."""
import unittest
from unittest.mock import patch
import run as runner


class ColdStartTests(unittest.TestCase):
    def test_cold_case_resets_data_but_leaves_launch_to_model(self):
        app = {"script": "test-target/dentasoft/dentasoft.py", "data": "test-target/dentasoft/patients.json"}
        with patch.object(runner, "stop_app") as stop, \
             patch.object(runner.os.path, "exists", return_value=True), \
             patch.object(runner.os, "remove") as remove, \
             patch.object(runner.subprocess, "Popen") as spawn, \
             patch.object(runner.time, "sleep") as sleep:
            runner.start_app(app, cold=True)
            stop.assert_called_once()
            remove.assert_called_once()
            spawn.assert_not_called()
            sleep.assert_not_called()

    def test_sync_preserves_other_machine_programs_and_has_no_reset_argument(self):
        class Box:
            machine = "test-desk"
            calls = []

            def call(self, method, path, body=None):
                self.calls.append((method, path, body))
                return (200, {"other": {"executable": "existing.exe"}}) if method == "GET" else (200, {})

        box = Box()
        suite = runner.load_suite("dentasoft")
        runner.sync(box, suite)
        installed = box.calls[1][2]
        self.assertIn("other", installed)
        self.assertEqual(installed["dentasoft"]["executable"], getattr(runner.sys, "_base_executable", runner.sys.executable))
        self.assertEqual(len(installed["dentasoft"]["args"]), 1)
        self.assertNotIn("--reset", installed["dentasoft"]["args"])
        self.assertTrue(box.calls[2][1].endswith("/programs"))
        self.assertTrue(box.calls[3][1].endswith("/operations"))


if __name__ == "__main__":
    unittest.main()
