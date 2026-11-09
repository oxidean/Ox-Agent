"""Regression tests for oxidean-watermarks-service.sh run leases (#47).

Drives the real script via subprocess against a fake /health endpoint and a
sleepy stand-in for service/scripts/server.py so server_state's identity
check matches. Stdlib unittest only — run directly:
    python3 skills/remove-ai-marks/tests/test_watermarks_service.py
"""

from __future__ import annotations

import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

SCRIPT = (
    Path(__file__).resolve().parent.parent / "scripts" / "oxidean-watermarks-service.sh"
)


class _Health(BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802 - stdlib hook
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, *_args: object) -> None:
        pass


@unittest.skipUnless(os.name == "posix", "lease ownership uses POSIX pid checks")
@unittest.skipUnless(shutil.which("flock"), "flock required for the serialized path")
class WatermarksServiceLeaseTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        self.state_dir = self.root / "tmp"
        self.state_dir.mkdir()
        self.state_file = self.state_dir / "watermarks-service.agent.json"
        self.leases_dir = self.state_dir / "watermarks-service.leases"
        self.httpd = HTTPServer(("127.0.0.1", 0), _Health)
        self.port = self.httpd.server_address[1]
        self._thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self._thread.start()
        self.env = {
            **os.environ,
            "OXIDEAN_ROOT": str(self.root),
            "WATERMARKS_SERVICE_URL": f"http://127.0.0.1:{self.port}",
        }

    def tearDown(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()
        self._thread.join(timeout=5)
        self._tmp.cleanup()

    def _call(self, *args: str, token: str | None = None) -> subprocess.CompletedProcess[str]:
        env = dict(self.env)
        if token is not None:
            env["WATERMARKS_RUN_TOKEN"] = token
        return subprocess.run(
            ["bash", str(SCRIPT), *args],
            env=env,
            capture_output=True,
            text=True,
            timeout=30,
        )

    def _lease_names(self) -> list[str]:
        if not self.leases_dir.is_dir():
            return []
        return sorted(p.name for p in self.leases_dir.iterdir())

    def _spawn_fake_service(self) -> int:
        """A live process whose cmdline matches *service/scripts/server.py*."""
        fake = self.root / "svc" / "service" / "scripts"
        fake.mkdir(parents=True)
        (fake / "server.py").write_text("import time\ntime.sleep(300)\n")
        proc = subprocess.Popen(
            [sys.executable, str(fake / "server.py")],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        self.addCleanup(proc.kill)
        self._service_proc = proc
        time.sleep(0.2)
        return proc.pid

    def test_second_runs_teardown_leaves_first_runs_service(self) -> None:
        """Run B's teardown must not kill the service run A still needs (#47)."""
        service_pid = self._spawn_fake_service()
        self.state_file.write_text(
            json.dumps(
                {
                    "started_by_us": True,
                    "pid": service_pid,
                    "url": self.env["WATERMARKS_SERVICE_URL"],
                    "checkout": str(self.root / "x"),
                }
            )
        )

        a = self._call("ensure", token="run-a")
        self.assertEqual(a.stdout.strip(), "REUSED", a.stderr)
        b = self._call("ensure", token="run-b")
        self.assertEqual(b.stdout.strip(), "REUSED", b.stderr)
        self.assertEqual(self._lease_names(), ["run-a", "run-b"])

        # B finishing must not touch the service A still leases.
        b_done = self._call("teardown", token="run-b")
        self.assertEqual(b_done.stdout.strip(), "LEFT_RUNNING", b_done.stderr)
        self.assertEqual(self._lease_names(), ["run-a"])
        self.assertTrue(self.state_file.exists(), "A's lease keeps the ownership record")
        self.assertIsNone(
            self._service_proc.poll(),
            "service killed while run-a still held a lease",
        )

        # A finishing last is what actually stops it.
        a_done = self._call("teardown", token="run-a")
        self.assertEqual(a_done.stdout.strip(), "STOPPED", a_done.stderr)
        self.assertFalse(self.state_file.exists())
        self.assertEqual(self._lease_names(), [])
        # poll() reaps the child — a bare kill(pid, 0) reports zombies alive.
        deadline = time.time() + 5
        while self._service_proc.poll() is None and time.time() < deadline:
            time.sleep(0.1)
        self.assertIsNotNone(self._service_proc.poll(), "service never stopped")

    def test_teardown_reaps_lease_whose_owner_died(self) -> None:
        """A crashed run's lease is reaped, not held forever (#47)."""
        self.state_file.write_text(
            json.dumps({"started_by_us": False, "pid": None, "url": "", "checkout": ""})
        )
        self.leases_dir.mkdir()
        dead_pid = 4_000_000  # far above plausible pid_max
        (self.leases_dir / "crashed").write_text(json.dumps({"owner_pid": dead_pid}))
        (self.leases_dir / "live").write_text(json.dumps({"owner_pid": os.getpid()}))

        done = self._call("teardown", token="finisher")

        self.assertEqual(done.stdout.strip(), "LEFT_RUNNING", done.stderr)
        self.assertEqual(self._lease_names(), ["live"], "dead-owner lease must be reaped")

    def test_run_subcommand_uses_one_token(self) -> None:
        """`run` must ensure+teardown under a single lease identity."""
        proc = self._call(
            "run", "--", "bash", "-c", "cat \"$OXIDEAN_ROOT/tmp/watermarks-service.leases\"/* 2>/dev/null; true",
            token="one-shot",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        # After run returns, its lease must be gone.
        self.assertNotIn("one-shot", self._lease_names())


if __name__ == "__main__":
    unittest.main()
