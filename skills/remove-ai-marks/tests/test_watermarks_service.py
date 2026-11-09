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

    def _call(
        self,
        *args: str,
        token: str | None = None,
        env_extra: dict | None = None,
    ) -> subprocess.CompletedProcess[str]:
        env = dict(self.env)
        if token is not None:
            env["WATERMARKS_RUN_TOKEN"] = token
        if env_extra:
            env.update(env_extra)
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
        # A real exited child is a deterministically dead owner — unlike
        # pid_max+1, which can stay alive when the limit was lowered under
        # running processes.
        dead = subprocess.Popen([sys.executable, "-c", "pass"])
        dead.wait()
        dead_pid = dead.pid
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

    def test_shared_parent_workflows_hold_distinct_leases(self) -> None:
        """Two runs under one parent + default token: distinct holds (#53).

        Each ensure files its own hold under the shared token dir and each
        teardown consumes exactly one — the first teardown must not delete
        the sibling workflow's claim.
        """
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

        a = self._call("ensure")
        self.assertEqual(a.stdout.strip(), "REUSED", a.stderr)
        b = self._call("ensure")
        self.assertEqual(b.stdout.strip(), "REUSED", b.stderr)
        # Both calls share the test process as parent → shared token dir.
        tok_dir = self.leases_dir / str(os.getpid())
        self.assertTrue(tok_dir.is_dir(), "holds must live under a per-token dir")
        self.assertEqual(len(list(tok_dir.iterdir())), 2)

        first = self._call("teardown")
        self.assertEqual(first.stdout.strip(), "LEFT_RUNNING", first.stderr)
        self.assertEqual(len(list(tok_dir.iterdir())), 1, "one hold consumed")
        self.assertIsNone(self._service_proc.poll(), "service killed too early")

        second = self._call("teardown")
        self.assertEqual(second.stdout.strip(), "STOPPED", second.stderr)
        self.assertFalse(tok_dir.is_dir(), "empty token dir must be removed")

    def test_run_subcommand_uses_one_token(self) -> None:
        """`run` must ensure+teardown under a single lease identity."""
        proc = self._call(
            "run", "--", "bash", "-c",
            "cat \"$OXIDEAN_ROOT/tmp/watermarks-service.leases\"/*/hold-* 2>/dev/null; true",
            token="one-shot",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        # After run returns, its lease must be gone.
        self.assertNotIn("one-shot", self._lease_names())


class WatermarksLeaseHardeningTest(unittest.TestCase):
    """Second-pass findings on the lease/lock machinery (#53)."""

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

    def _call(
        self,
        *args: str,
        token: str | None = None,
        env_extra: dict | None = None,
    ) -> subprocess.CompletedProcess[str]:
        env = dict(self.env)
        if token is not None:
            env["WATERMARKS_RUN_TOKEN"] = token
        if env_extra:
            env.update(env_extra)
        return subprocess.run(
            ["bash", str(SCRIPT), *args],
            env=env,
            capture_output=True,
            text=True,
            timeout=30,
        )

    def _holds(self, token: str) -> list[str]:
        d = self.leases_dir / token
        if not d.is_dir():
            return []
        return sorted(p.name for p in d.iterdir())

    def test_ensure_migrates_live_legacy_file_lease(self) -> None:
        """A pre-holds lease FILE at the token path must not block ensure (#53)."""
        self.leases_dir.mkdir()
        (self.leases_dir / "tokA").write_text(json.dumps({"owner_pid": os.getpid()}))

        proc = self._call("ensure", token="tokA")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn(proc.stdout.strip(), ("STARTED", "REUSED"))
        tok_dir = self.leases_dir / "tokA"
        self.assertTrue(tok_dir.is_dir(), "legacy file must be migrated into a dir")
        names = self._holds("tokA")
        self.assertTrue(
            any(n.startswith("hold-legacy-") for n in names),
            f"migrated hold must preserve the legacy claim: {names}",
        )
        # The original owner (same parent pid) can still release its claim:
        # the first teardown consumes the fresh hold, leaving the migrated
        # legacy claim; the second consumes it and the dir is gone.
        self._call("teardown", token="tokA")
        names = self._holds("tokA")
        self.assertEqual(len(names), 1, f"legacy claim must survive: {names}")
        self.assertTrue(names[0].startswith("hold-legacy-"), names)
        proc = self._call("teardown", token="tokA")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertFalse(
            (self.leases_dir / "tokA").exists(), "all claims released"
        )

    def test_teardown_reaps_abandoned_tmp_hold(self) -> None:
        """A stale .hold.tmp.* must not pin the token dir forever (#53)."""
        tok_dir = self.leases_dir / "tokB"
        tok_dir.mkdir(parents=True)
        orphan = tok_dir / ".hold.tmp.4242"
        orphan.write_text("{")
        old = os.path.getmtime(orphan) - 120
        os.utime(orphan, (old, old))

        proc = self._call("teardown", token="tokB")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertFalse(tok_dir.exists(), "dir left with only stale tmp files must go")

    def test_named_hold_releases_exactly_it(self) -> None:
        """WATERMARKS_HOLD releases the ensure's own hold, not a sibling's (#53)."""
        self._call("ensure", token="tokC")
        self._call("ensure", token="tokC")
        holds = self._holds("tokC")
        self.assertEqual(len(holds), 2, holds)

        proc = self._call(
            "teardown", token="tokC", env_extra={"WATERMARKS_HOLD": holds[0]}
        )

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), "LEFT_RUNNING")
        self.assertEqual(self._holds("tokC"), [holds[1]])

        # A repeated teardown with the same env releases nothing more.
        proc = self._call(
            "teardown", token="tokC", env_extra={"WATERMARKS_HOLD": holds[0]}
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(self._holds("tokC"), [holds[1]])

        proc = self._call("teardown", token="tokC")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(self._holds("tokC"), [])

    def test_teardown_prefers_current_incarnation_hold(self) -> None:
        """A stale-incarnation hold under our pid is left for the reaper (#53)."""
        self._call("ensure", token="tokD")
        # Plant an older-sorting hold claiming our owner pid with a lifetime
        # that cannot be this incarnation.
        (self.leases_dir / "tokD" / "hold-00000000000000-stale").write_text(
            json.dumps({"owner_pid": os.getpid(), "owner_start": "stale-incarnation"})
        )

        proc = self._call("teardown", token="tokD")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        # The stdout verdict is LEFT_RUNNING either way (the stub service was
        # not started by us) — the regression signal is the lease state:
        # consuming the stale hold would leave the real one held behind.
        self.assertEqual(
            self._holds("tokD"),
            [],
            "stale-incarnation hold must be reaped and the real one consumed",
        )

    def test_ensure_reclaims_ownerless_stale_lock(self) -> None:
        """A lockdir whose holder died before writing owner must not stall (#53)."""
        lockdir = self.state_dir / "watermarks-service.ensure.lock.d"
        lockdir.mkdir()
        old = lockdir.stat().st_mtime - 10
        os.utime(lockdir, (old, old))

        proc = self._call("ensure", env_extra={"WATERMARKS_NO_FLOCK": "1"})

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn(proc.stdout.strip(), ("STARTED", "REUSED"))

    def test_ensure_reclaims_dead_owner_lock(self) -> None:
        """A lockdir held by a confirmed-dead pid is reclaimed, not waited out (#53)."""
        dead = subprocess.Popen(["bash", "-c", "exit 0"])
        dead.wait()
        lockdir = self.state_dir / "watermarks-service.ensure.lock.d"
        lockdir.mkdir()
        (lockdir / "owner").write_text(f"{dead.pid}\n")

        proc = self._call("ensure", env_extra={"WATERMARKS_NO_FLOCK": "1"})

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn(proc.stdout.strip(), ("STARTED", "REUSED"))


if __name__ == "__main__":
    unittest.main()
