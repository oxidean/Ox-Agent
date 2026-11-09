#!/usr/bin/env python3
"""Strip invisible Unicode / normalize space homoglyphs (Layer A)."""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import (
    backup_path,
    cleaned_path,
    eprint,
    lock_inplace_fd,
    read_text_input,
    read_text_input_fd,
    rewrite_file_fd,
    write_text_output,
)
from text_unicode import clean_text


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("path", nargs="?", default="-", help="Input text file, or - for stdin")
    p.add_argument("-o", "--output", help="Output path (default: stdout or *.cleaned.*)")
    p.add_argument("--nfkc", action="store_true", help="Apply Unicode NFKC after scrub")
    p.add_argument(
        "--aggressive-homoglyphs",
        action="store_true",
        help="Map Cyrillic/fullwidth Latin confusables to ASCII Latin",
    )
    p.add_argument(
        "--no-normalize-spaces",
        action="store_true",
        help="Do not rewrite exotic spaces to U+0020",
    )
    p.add_argument(
        "--strip-emoji-glue",
        action="store_true",
        help="Paranoid: strip all load-bearing invisibles too (emoji glue, script joiners, flag tags, same-script fillers/selectors, orthographic Cf)",
    )
    p.add_argument(
        "--strip-bidi",
        action="store_true",
        help="Also strip legitimate RTL/LTR directional marks and isolates",
    )
    p.add_argument("--stats", action="store_true", help="Print stats JSON to stderr")
    p.add_argument(
        "--force-text",
        action="store_true",
        help="Clean even when the input looks like a binary container "
        "(this rewrites the bytes and will corrupt the file)",
    )
    p.add_argument(
        "--in-place",
        action="store_true",
        help="Overwrite input file (creates .bak backup)",
    )
    args = p.parse_args()

    # Resolve AND open the in-place target up front: the read, the backup,
    # and the overwrite must all stay bound to the same inode — otherwise a
    # link or parent-directory swap between read and write would clean one
    # file and overwrite another without backing it up (#46, #53).
    in_place_src: Path | None = None
    in_place_fd: int | None = None
    in_place_writable = True
    if args.in_place:
        if args.path in (None, "-"):
            eprint("--in-place requires a file path")
            return 2
        # resolve() raises on unreadable or looping links — a traceback
        # here would bypass the clean exit-2 contract (RuntimeError on
        # Python <3.10 for symlink loops, OSError elsewhere). O_RDWR binds
        # the fd to the resolved inode for both the read below and the
        # rewrite at the end.
        try:
            in_place_src = Path(args.path).resolve()
            # O_BINARY keeps the byte-exact contract on Windows, where a
            # text-mode descriptor would translate newlines on rewrite.
            in_place_fd = os.open(
                in_place_src, os.O_RDWR | getattr(os, "O_BINARY", 0)
            )
        except PermissionError:
            # A read-only target can still reach the no-op path: bind an
            # O_RDONLY descriptor for the read and only demand write access
            # when cleaning actually changes bytes (#53).
            try:
                in_place_fd = os.open(
                    in_place_src, os.O_RDONLY | getattr(os, "O_BINARY", 0)
                )
            except OSError as e:
                eprint(f"cannot open {args.path}: {e}")
                return 2
            in_place_writable = False
        except (OSError, RuntimeError) as e:
            eprint(f"cannot open {args.path}: {e}")
            return 2
        if in_place_writable:
            try:
                # Serialize against a concurrent cleaner on the same inode —
                # without it two truncate+rewrite passes can interleave and
                # leave bytes matching neither output (#53). Held for the
                # descriptor's lifetime so read, backup, and rewrite stay
                # inside the critical section.
                lock_inplace_fd(in_place_fd, str(in_place_src))
            except SystemExit:
                os.close(in_place_fd)
                raise

    if in_place_fd is not None:
        text = read_text_input_fd(
            in_place_fd, str(in_place_src), allow_binary=args.force_text
        )
    else:
        text = read_text_input(args.path, allow_binary=args.force_text)
    cleaned, stats = clean_text(
        text,
        nfkc=args.nfkc,
        aggressive_homoglyphs=args.aggressive_homoglyphs,
        normalize_spaces=not args.no_normalize_spaces,
        strip_emoji_glue=args.strip_emoji_glue,
        strip_bidi=args.strip_bidi,
    )

    out = args.output
    skip_write = False
    created_bak: Path | None = None
    if args.in_place:
        src = in_place_src
        if cleaned == text:
            # Nothing to change: skip the backup and the rewrite entirely so
            # repeated hook runs do not accumulate .bakN files until the
            # backup_path attempt cap refuses.
            skip_write = True
        else:
            if not in_place_writable:
                # Cleaning is needed but the target refused O_RDWR — refuse
                # before creating a backup that could never be needed (#53).
                eprint(f"cannot rewrite {in_place_src}: file is read-only")
                os.close(in_place_fd)
                return 2
            # The backup must carry the bytes this run read — re-reading
            # through the path can hit a parent-directory swap and store
            # the replacement's contents while the rewrite lands on the
            # original inode (#53). Read via the bound descriptor.
            try:
                os.lseek(in_place_fd, 0, os.SEEK_SET)
                with os.fdopen(os.dup(in_place_fd), "rb") as rf:
                    src_bytes = rf.read()
            except OSError as e:
                eprint(f"cannot read {in_place_src} for backup: {e}")
                os.close(in_place_fd)
                return 2
            created_bak, _ = backup_path(src, data=src_bytes)
            try:
                eprint(f"backup written to {created_bak}")
            except BaseException:
                # stderr broke before the overwrite even started: the
                # backup this run created is unused — remove it (#46).
                with contextlib.suppress(OSError):
                    created_bak.unlink()
                raise
        out = str(src)
    elif out is None and args.path not in (None, "-"):
        out = str(cleaned_path(Path(args.path)))

    if not skip_write:
        if in_place_fd is not None:
            rewrite_err: OSError | None = None
            try:
                rewrite_file_fd(
                    in_place_fd, cleaned.encode("utf-8", "surrogateescape")
                )
            except OSError as e:
                rewrite_err = e
            close_err: OSError | None = None
            try:
                os.close(in_place_fd)
            except OSError as e:
                # close surfaces deferred write-back errors (e.g. NFS):
                # the bytes on disk are uncertain, so this is not a quiet
                # success either.
                close_err = e
            if rewrite_err is not None:
                # The in-place rewrite is NOT atomic: the target may be
                # truncated or partial, so the .bak this run created is the
                # only intact copy of the original — keep it and say so.
                eprint(
                    f"cannot rewrite {in_place_src}: {rewrite_err}; "
                    f"original preserved in {created_bak}"
                )
                return 2
            if close_err is not None:
                # A rewrite was attempted: treat the file as suspect and
                # keep the backup rather than reporting a clean write.
                eprint(
                    f"error closing {in_place_src}: {close_err}; "
                    f"original preserved in {created_bak}"
                )
                return 2
        else:
            try:
                write_text_output(cleaned, out)
            except OSError as e:
                # The atomic write failed, so the original is intact and the
                # backup created this run is dead weight.
                if created_bak is not None:
                    with contextlib.suppress(OSError):
                        created_bak.unlink()
                eprint(f"cannot write output: {e}")
                return 2
    elif in_place_fd is not None:
        try:
            os.close(in_place_fd)
        except OSError as e:
            eprint(f"error closing {in_place_src}: {e}")
            return 2

    if args.stats:
        eprint(json.dumps(stats, indent=2, ensure_ascii=False))
    else:
        eprint(
            f"removed={stats['removed_count']} replaced={stats['replaced_count']} "
            f"len {stats['input_length']}->{stats['output_length']}"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
