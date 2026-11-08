#!/usr/bin/env bash
# Syncs canonical asset directories into the embedded tree consumed by
# internal/assets. Canonical sources of truth live at the repository root;
# the embedded tree is generated, never edited directly.
set -euo pipefail
cd "$(dirname "$0")/.."

for dir in skills schemas workflows pipelines lifecycle integrations; do
  if [ ! -d "$dir" ]; then
    echo "sync-assets: canonical directory $dir missing" >&2
    exit 1
  fi
  rm -rf "internal/assets/embedded/$dir"
  mkdir -p "internal/assets/embedded/$dir"
  (cd "$dir" && find . -type f -not -name '.*' -not -path '*/__pycache__/*' -not -name '*.pyc' -print0 | while IFS= read -r -d '' file; do
    mkdir -p "../internal/assets/embedded/$dir/$(dirname "$file")"
    cp "$file" "../internal/assets/embedded/$dir/$file"
  done)
done

# Record the canonical files carrying the executable bit. embed.FS drops
# file modes, so this manifest — embedded separately, never materialized —
# is the binary's only record of which assets must be restored 0755.
# Mode bits are not reliably observable on Windows checkouts; generate the
# manifest on POSIX only.
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) ;;
  *)
    {
      for dir in skills schemas workflows pipelines lifecycle integrations; do
        (cd "$dir" && find . -type f -perm -u+x -not -name '.*' -not -path '*/__pycache__/*' -not -name '*.pyc' | sed "s|^\\./|$dir/|")
      done
    } | LC_ALL=C sort > internal/assets/executables.txt
    ;;
esac

echo "sync-assets: embedded tree updated"
