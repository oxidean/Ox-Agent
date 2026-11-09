"""Regression tests for clean_text.py --in-place failure paths (#46).

Stdlib unittest only — run directly:
    python3 skills/clean-user-facing-text/tests/test_clean_text_inplace.py
"""

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "scripts"))

import clean_text  # noqa: E402

SCRIPT = Path(__file__).resolve().parent.parent / "scripts" / "clean_text.py"
# U+200B ZERO WIDTH SPACE — always removed by the default layer-A scrub, so a
# seeded input is guaranteed "dirty" and the backup path is exercised.
DIRTY = "clean line​ with invisible\n"


@unittest.skipUnless(os.name == "posix", "symlink semantics are POSIX-specific")
class InPlaceSymlinkTest(unittest.TestCase):
    def test_in_place_cleans_symlink_target_and_backs_up_beside_it(self) -> None:
        """--in-place on a symlink must clean the target, not crash (#46)."""
        with tempfile.TemporaryDirectory() as tmp:
            real = Path(tmp) / "real.txt"
            real.write_text(DIRTY, encoding="utf-8")
            link = Path(tmp) / "link.txt"
            link.symlink_to(real)

            proc = subprocess.run(
                [sys.executable, str(SCRIPT), str(link), "--in-place"],
                capture_output=True,
                text=True,
            )

            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertNotIn("Traceback", proc.stderr)
            self.assertTrue(link.is_symlink(), "the link itself must remain a symlink")
            self.assertEqual(real.read_text(encoding="utf-8"), "clean line with invisible\n")
            bak = real.with_suffix(real.suffix + ".bak")
            self.assertTrue(bak.is_file(), f"backup must sit beside the target: {bak}")
            self.assertEqual(bak.read_text(encoding="utf-8"), DIRTY)
            self.assertFalse(
                link.with_suffix(link.suffix + ".bak").exists(),
                "no backup may be created next to the symlink",
            )


class InPlaceWriteFailureTest(unittest.TestCase):
    def test_failed_write_removes_the_backup_and_exits_cleanly(self) -> None:
        """A failed write must not leave an orphan .bak or a traceback (#46)."""
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "input.txt"
            src.write_text(DIRTY, encoding="utf-8")

            argv = ["clean_text.py", str(src), "--in-place"]
            with (
                mock.patch.object(sys, "argv", argv),
                mock.patch.object(
                    clean_text,
                    "write_text_output",
                    side_effect=OSError("simulated write failure"),
                ),
            ):
                rc = clean_text.main()

            self.assertEqual(rc, 2)
            self.assertEqual(src.read_text(encoding="utf-8"), DIRTY, "original must be intact")
            self.assertEqual(
                list(Path(tmp).glob("*.bak*")),
                [],
                "the backup created this run must be removed on write failure",
            )


if __name__ == "__main__":
    unittest.main()
