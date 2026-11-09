#!/usr/bin/env python3
"""Strip invisible Unicode / normalize space homoglyphs (Layer A)."""

from __future__ import annotations

import argparse
import contextlib
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import (
    backup_path,
    cleaned_path,
    eprint,
    read_text_input,
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

    # Resolve the in-place target up front: the read, the backup, and the
    # overwrite must all hit the same file even when the input is (or
    # becomes) a symlink — otherwise a link swap between read and write
    # would clean one file and overwrite another (#46).
    in_place_src: Path | None = None
    if args.in_place:
        if args.path in (None, "-"):
            eprint("--in-place requires a file path")
            return 2
        in_place_src = Path(args.path).resolve()

    text = read_text_input(
        str(in_place_src) if in_place_src is not None else args.path,
        allow_binary=args.force_text,
    )
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
            created_bak, _ = backup_path(src)
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
