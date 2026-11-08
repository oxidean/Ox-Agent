"""Regression tests for common.backup_path permission handling.

Stdlib unittest only — run directly:
    python3 skills/clean-user-facing-text/tests/test_common_backup.py
"""

from __future__ import annotations

import os
import stat
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "scripts"))

import common  # noqa: E402


@unittest.skipUnless(os.name == "posix", "mode bits are advisory on Windows")
class BackupPathModeTest(unittest.TestCase):
    def setUp(self) -> None:
        # umask is process-wide and applied by os.open on top of the passed
        # mode. Pin a known umask so a strict test-runner umask can neither
        # false-fail the preserve test nor mask a fallback regression.
        self._old_umask = os.umask(0o022)

    def tearDown(self) -> None:
        os.umask(self._old_umask)

    def test_backup_mode_fallback_is_owner_only_when_stat_fails(self) -> None:
        """src.stat() OSError must not fall back to the umask default (0644)."""
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "input.txt"
            src.write_bytes(b"payload")
            os.chmod(src, 0o644)

            real_stat = Path.stat

            def fake_stat(self: Path, *args: object, **kwargs: object) -> os.stat_result:
                if self == src:
                    raise OSError("simulated stat failure")
                return real_stat(self, *args, **kwargs)

            with mock.patch.object(Path, "stat", fake_stat):
                bak, created = common.backup_path(src)

            self.assertTrue(created)
            self.assertEqual(bak.read_bytes(), b"payload")
            mode = stat.S_IMODE(os.stat(bak).st_mode)
            self.assertEqual(mode & 0o077, 0, f"backup {bak} is group/other-accessible: {mode:o}")

    def test_backup_preserves_source_mode(self) -> None:
        """Regression anchor for #28: a 0644 source keeps a 0644 backup."""
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "input.txt"
            src.write_bytes(b"payload")
            os.chmod(src, 0o644)

            bak, created = common.backup_path(src)

            self.assertTrue(created)
            self.assertEqual(stat.S_IMODE(os.stat(bak).st_mode), 0o644)


if __name__ == "__main__":
    unittest.main()
