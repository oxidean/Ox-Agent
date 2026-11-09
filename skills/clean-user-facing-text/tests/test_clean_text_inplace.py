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
    def test_failed_write_keeps_the_backup_and_exits_cleanly(self) -> None:
        """A failed in-place rewrite must exit 2 and KEEP the backup (#53).

        The fd-bound rewrite is not atomic — the target may be truncated —
        so the .bak this run created is the only intact copy of the original
        and must survive the failure (contrast the atomic temp+replace path,
        where a failed write leaves the original intact and the .bak dead).
        """
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "input.txt"
            src.write_text(DIRTY, encoding="utf-8")

            argv = ["clean_text.py", str(src), "--in-place"]
            with (
                mock.patch.object(sys, "argv", argv),
                mock.patch.object(
                    clean_text,
                    "rewrite_file_fd",
                    side_effect=OSError("simulated write failure"),
                ),
            ):
                rc = clean_text.main()

            self.assertEqual(rc, 2)
            baks = list(Path(tmp).glob("*.bak*"))
            self.assertEqual(len(baks), 1, "the backup must be retained on write failure")
            self.assertEqual(baks[0].read_text(encoding="utf-8"), DIRTY)


@unittest.skipUnless(
    os.name == "posix",
    "the parent-dir swap simulation renames a dir holding an open fd — "
    "Windows refuses that outright, which is also why the race cannot "
    "occur there in this form",
)
class InPlaceBindingTest(unittest.TestCase):
    def test_write_stays_bound_to_the_inode_that_was_read(self) -> None:
        """A parent-dir swap after backup must not redirect the overwrite.

        Resolve+open pins the target's inode; if the resolved parent is then
        swapped for a fresh directory, the rewrite must still land on the
        file that was read and backed up — never on the unbacked-up file now
        occupying the path (#53).
        """
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp) / "d"
            parent.mkdir()
            src = parent / "input.txt"
            src.write_text(DIRTY, encoding="utf-8")

            real_backup_path = clean_text.backup_path
            swapped_parent = Path(tmp) / "d-moved"

            def swap_parent_after_backup(target: Path, data: bytes | None = None):
                result = real_backup_path(target, data=data)
                parent.rename(swapped_parent)
                parent.mkdir()
                (parent / "input.txt").write_text("different file\n", encoding="utf-8")
                return result

            argv = ["clean_text.py", str(src), "--in-place"]
            with (
                mock.patch.object(sys, "argv", argv),
                mock.patch.object(clean_text, "backup_path", swap_parent_after_backup),
            ):
                rc = clean_text.main()

            self.assertEqual(rc, 0)
            self.assertEqual(
                (parent / "input.txt").read_text(encoding="utf-8"),
                "different file\n",
                "the unbacked-up file now at the path must not be overwritten",
            )
            self.assertEqual(
                (swapped_parent / "input.txt").read_text(encoding="utf-8"),
                "clean line with invisible\n",
                "the inode that was read must receive the cleaned text",
            )
            self.assertEqual(
                (swapped_parent / "input.txt.bak").read_text(encoding="utf-8"),
                DIRTY,
                "the backup of the bound inode travels with it",
            )


@unittest.skipUnless(os.name == "posix", "hard links are POSIX-specific")
class InPlaceHardlinkTest(unittest.TestCase):
    def test_in_place_refuses_hard_linked_input(self) -> None:
        """--in-place must not rewrite other names sharing the inode (#53)."""
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "input.txt"
            src.write_text(DIRTY, encoding="utf-8")
            alias = Path(tmp) / "alias.txt"
            os.link(src, alias)

            proc = subprocess.run(
                [sys.executable, str(SCRIPT), str(alias), "--in-place"],
                capture_output=True,
                text=True,
            )

            self.assertEqual(proc.returncode, 2, proc.stderr)
            self.assertIn("hard-linked", proc.stderr)
            self.assertNotIn("Traceback", proc.stderr)
            self.assertEqual(src.read_text(encoding="utf-8"), DIRTY)
            self.assertEqual(alias.read_text(encoding="utf-8"), DIRTY)


@unittest.skipUnless(os.name == "posix", "fcntl.flock is POSIX-only")
class InPlaceLockContentionTest(unittest.TestCase):
    def test_in_place_refuses_when_another_run_holds_the_lock(self) -> None:
        """A contended in-place lock must refuse, never interleave (#53)."""
        import fcntl

        import common

        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "input.txt"
            src.write_text(DIRTY, encoding="utf-8")
            holder = os.open(src, os.O_RDWR)
            try:
                fcntl.flock(holder, fcntl.LOCK_EX)
                argv = ["clean_text.py", str(src), "--in-place"]
                with (
                    mock.patch.object(sys, "argv", argv),
                    mock.patch.object(common, "_INPLACE_LOCK_SECONDS", 0.2),
                ):
                    with self.assertRaises(SystemExit) as cm:
                        clean_text.main()
                self.assertEqual(cm.exception.code, 2)
                self.assertEqual(
                    src.read_text(encoding="utf-8"),
                    DIRTY,
                    "a refused run must not touch the contended file",
                )
            finally:
                fcntl.flock(holder, fcntl.LOCK_UN)
                os.close(holder)


class InPlaceResolveFailureTest(unittest.TestCase):
    def test_unresolvable_path_exits_cleanly_without_traceback(self) -> None:
        """A resolve() failure must be a clean exit 2, not a traceback (#53).

        Python <3.10 raises RuntimeError on symlink loops and every runtime
        can raise OSError for unresolvable paths — both must be reported.
        """
        argv = ["clean_text.py", "loop.txt", "--in-place"]
        for exc in (RuntimeError("Symlink loop"), OSError("cannot resolve")):
            with self.subTest(exc=type(exc).__name__):
                with (
                    mock.patch.object(sys, "argv", argv),
                    mock.patch.object(Path, "resolve", side_effect=exc),
                ):
                    rc = clean_text.main()
                self.assertEqual(rc, 2)


if __name__ == "__main__":
    unittest.main()
