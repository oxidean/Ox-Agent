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
        # Above the kernel's maximum possible pid: deterministically absent.
        # 4_000_000 was not — 64-bit pid_max can reach 4_194_304.
        try:
            dead_pid = int(Path("/proc/sys/kernel/pid_max").read_text()) + 1
        except (OSError, ValueError):
            dead_pid = 1 << 22  # linux pid_max ceiling (also above macOS 99998)
        (self.leases_dir / "crashed").write_text(json.dumps({"owner_pid": dead_pid}))
        (self.leases_dir / "live").write_text(json.dumps({"owner_pid": os.getpid()}))

        done = self._call("teardown", token="finisher")

        self.assertEqual(done.stdout.strip(), "LEFT_RUNNING", done.stderr)
        self.assertEqual(self._lease_names(), ["live"], "dead-owner lease must be reaped")

    @unittest.skipUnless(os.path.exists("/proc/self"), "starttime identity needs procfs")
    def test_teardown_reaps_lease_with_recycled_owner_identity(self) -> None:
        """A live pid with a mismatched starttime is a recycled owner (#130)."""
        self.state_file.write_text(
            json.dumps({"started_by_us": False, "pid": None, "url": "", "checkout": ""})
        )
        self.leases_dir.mkdir()
        # owner_pid is alive (this test process) but the recorded starttime
        # can never match a real one — proof the recorded owner is gone.
        (self.leases_dir / "recycled").write_text(
            json.dumps({"owner_pid": os.getpid(), "owner_start": "1"})
        )

        done = self._call("teardown", token="finisher")

        self.assertEqual(done.stdout.strip(), "LEFT_RUNNING", done.stderr)
        self.assertEqual(self._lease_names(), [], "identity-mismatched lease must be reaped")

    def test_failed_ensure_releases_only_its_lease(self) -> None:
        """A failed start drops the lease it wrote — and only that one (#251)."""
        self.leases_dir.mkdir()
        (self.leases_dir / "other-run").write_text(
            json.dumps({"owner_pid": os.getpid()})
        )
        env = dict(self.env)
        # Port 1 can't serve /health and can't be bound: ensure reaches the
        # cold-start path, the clone fails, and the run must clean up.
        env["WATERMARKS_SERVICE_URL"] = "http://127.0.0.1:1"
        env["WATERMARKS_CHECKOUT"] = str(self.root / "missing-checkout")
        env["WATERMARKS_CLONE_URL"] = str(self.root / "no-such-repo.git")
        env["WATERMARKS_RUN_TOKEN"] = "failed-run"

        proc = subprocess.run(
            ["bash", str(SCRIPT), "ensure"],
            env=env,
            capture_output=True,
            text=True,
            timeout=30,
        )

        self.assertNotEqual(proc.returncode, 0, proc.stdout)
        self.assertEqual(
            self._lease_names(),
            ["other-run"],
            "failed ensure must drop its own lease and keep others'",
        )

    @unittest.skipUnless(os.path.exists("/proc/self"), "fd inspection needs procfs")
    def test_server_child_does_not_hold_ensure_lock(self) -> None:
        """The spawned server must not inherit the lifecycle lock fd (#266)."""
        fake = self.root / "svc" / "service" / "scripts"
        fake.mkdir(parents=True)
        (fake / "server.py").write_text(
            "import sys\n"
            "from http.server import BaseHTTPRequestHandler, HTTPServer\n"
            "port = int(sys.argv[sys.argv.index('--port') + 1])\n"
            "class H(BaseHTTPRequestHandler):\n"
            "    def do_GET(self):\n"
            "        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')\n"
            "    def log_message(self, *a):\n"
            "        pass\n"
            "HTTPServer(('127.0.0.1', port), H).serve_forever()\n"
        )
        # A fresh port nothing listens on: health fails, ensure cold-starts.
        import socket
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            free_port = s.getsockname()[1]
        env = dict(self.env)
        env["WATERMARKS_SERVICE_URL"] = f"http://127.0.0.1:{free_port}"
        env["WATERMARKS_CHECKOUT"] = str(self.root / "svc")

        started = subprocess.run(
            ["bash", str(SCRIPT), "ensure"],
            env=env,
            capture_output=True,
            text=True,
            timeout=30,
        )
        self.assertEqual(started.stdout.strip(), "STARTED", started.stderr)
        state = json.loads(self.state_file.read_text())
        server_pid = state["pid"]
        self.addCleanup(
            lambda: subprocess.run(
                ["kill", str(server_pid)], capture_output=True, timeout=5
            )
        )

        lock_path = str(self.state_dir / "watermarks-service.ensure.lock")
        held = {
            os.readlink(fd)
            for fd in Path(f"/proc/{server_pid}/fd").iterdir()
            if os.path.exists(fd)
        }
        self.assertNotIn(
            lock_path, held, "server inherited the ensure lock descriptor"
        )

        done = self._call("teardown")
        self.assertEqual(done.stdout.strip(), "STOPPED", done.stderr)

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
