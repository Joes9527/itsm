import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import unittest


SCRIPT = Path(__file__).parents[1] / "wsl-stack.py"


class WslStackTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.state = Path(self.temp.name)
        (self.state / "config").mkdir()
        (self.state / "evidence").mkdir()
        (self.state / "logs").mkdir()
        self.children = []

    def tearDown(self):
        for child in self.children:
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    child.kill()
            if child.stdout:
                child.stdout.close()
        self.temp.cleanup()

    def run_stack(self, action, service="itsm", expected=0):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--state", str(self.state), action, service],
            text=True,
            capture_output=True,
            timeout=10,
        )
        self.assertEqual(expected, result.returncode, result.stdout + result.stderr)
        return result.stdout + result.stderr

    def write_recipe(self, **updates):
        recipe = {
            "ready": True,
            "cwd": str(self.state),
            "argv": [sys.executable, "-c", "import time; time.sleep(60)"],
            "env": {"TEST_PRIVATE_VALUE": "do-not-print"},
        }
        recipe.update(updates)
        (self.state / "config" / "itsm-launch.json").write_text(json.dumps(recipe))
        return recipe

    def test_nominal_start_status_stop_preserves_recorded_identity(self):
        self.write_recipe()

        started = self.run_stack("start")
        record = json.loads((self.state / "evidence" / "itsm-process.json").read_text())
        self.assertIn("itsm: started", started)
        self.assertEqual(self.state.resolve(), Path(record["cwd"]).resolve())
        self.assertTrue(record["start_ticks"])
        self.assertIn(f"running (PID {record['pid']})", self.run_stack("status"))
        self.assertIn("itsm: stopped", self.run_stack("stop"))

    def test_start_fails_closed_when_configured_port_has_untracked_owner(self):
        listener = subprocess.Popen(
            [sys.executable, "-c", "import socket,time; s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind(('127.0.0.1',0)); print(s.getsockname()[1],flush=True); s.listen(); time.sleep(60)"],
            stdout=subprocess.PIPE,
            text=True,
        )
        self.children.append(listener)
        port = int(listener.stdout.readline())
        self.write_recipe(port=port)

        output = self.run_stack("start", expected=1)

        self.assertIn(f"port {port} is occupied", output)
        self.assertIn(str(listener.pid), output)
        self.assertFalse((self.state / "evidence" / "itsm-process.json").exists())

    def test_status_reports_artifact_drift_for_running_process(self):
        artifact = self.state / "server.py"
        artifact.write_text("import time; time.sleep(60)\n")
        expected_hash = hashlib.sha256(artifact.read_bytes()).hexdigest()
        self.write_recipe(
            argv=[sys.executable, str(artifact)],
            artifact_sha256=expected_hash,
        )
        self.run_stack("start")
        artifact.write_text("import time; time.sleep(61)\n")

        output = self.run_stack("status", expected=1)

        self.assertIn("running", output)
        self.assertIn("artifact_sha256 drift", output)
        self.assertIn("configured", output)
        self.assertIn("actual", output)

    def test_start_rejects_build_id_drift(self):
        (self.state / "BUILD_ID").write_text("actual-build\n")
        self.write_recipe(build_id="configured-build")

        status = self.run_stack("status", expected=1)
        output = self.run_stack("start", expected=1)

        self.assertIn("stopped with build_id drift", status)
        self.assertIn("build_id drift", output)
        self.assertIn("configured configured-build", output)
        self.assertIn("actual actual-build", output)
        self.assertFalse((self.state / "evidence" / "itsm-process.json").exists())

    def test_start_rejects_config_drift_while_recorded_process_is_alive(self):
        self.write_recipe()
        self.run_stack("start")
        record_path = self.state / "evidence" / "itsm-process.json"
        record = json.loads(record_path.read_text())
        self.write_recipe(argv=[sys.executable, "-c", "import time; time.sleep(61)"])

        output = self.run_stack("start", expected=1)

        self.assertIn(f"running (PID {record['pid']})", output)
        self.assertIn("runtime configuration drift", output)

    def test_pid_reuse_mismatch_is_fail_closed_for_status_and_stop(self):
        child = subprocess.Popen(
            [sys.executable, "-c", "import time; time.sleep(60)"], cwd=self.state
        )
        self.children.append(child)
        record = {
            "pid": child.pid,
            "start_ticks": "1",
            "cwd": str(self.state),
            "argv": [sys.executable],
        }
        (self.state / "evidence" / "itsm-process.json").write_text(json.dumps(record))
        self.write_recipe()

        status = self.run_stack("status", expected=1)
        stopped = self.run_stack("stop", expected=1)

        self.assertIn("identity mismatch", status)
        self.assertIn(f"actual PID {child.pid}", status)
        self.assertIn("refusing to stop", stopped)
        self.assertIsNone(child.poll())

    def test_status_reports_actual_port_owner_when_recorded_pid_is_stale(self):
        listener = subprocess.Popen(
            [sys.executable, "-c", "import socket,time; s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind(('127.0.0.1',0)); print(s.getsockname()[1],flush=True); s.listen(); time.sleep(60)"],
            stdout=subprocess.PIPE,
            text=True,
        )
        self.children.append(listener)
        port = int(listener.stdout.readline())
        self.write_recipe(port=port)
        (self.state / "evidence" / "itsm-process.json").write_text(json.dumps({
            "pid": 99999999,
            "start_ticks": "1",
            "cwd": str(self.state),
        }))

        output = self.run_stack("status", expected=1)

        self.assertIn("recorded PID 99999999 is gone", output)
        self.assertIn(f"port {port}", output)
        self.assertIn(str(listener.pid), output)


if __name__ == "__main__":
    unittest.main()
