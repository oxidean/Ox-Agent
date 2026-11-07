#!/usr/bin/env bash
# Verifies:
#   1. the embedded asset tree matches the canonical directories exactly
#   2. no generated Python cache files sit in the embedded tree (`go:embed
#      all:` would ship them and Materialize would write them into projects)
#   3. the dogfooded .ox-agent tree carries every canonical file unchanged
# Run by CI; contributors fix drift with scripts/sync-assets.sh and
# `ox-agent skills update` (or `init --repair --sync`).
set -euo pipefail
cd "$(dirname "$0")/.."

status=0
asset_dirs="skills schemas workflows pipelines lifecycle integrations"

for dir in $asset_dirs; do
  if [ ! -d "$dir" ]; then
    echo "check-assets: canonical directory $dir missing" >&2
    status=1
    continue
  fi
  if [ ! -d "internal/assets/embedded/$dir" ]; then
    echo "check-assets: embedded tree missing $dir (run scripts/sync-assets.sh)" >&2
    status=1
    continue
  fi
  if ! diff -r --exclude='.*' --exclude='__pycache__' --exclude='*.pyc' "$dir" "internal/assets/embedded/$dir" > /dev/null 2>&1; then
    echo "check-assets: drift between $dir and internal/assets/embedded/$dir:" >&2
    diff -r --exclude='.*' --exclude='__pycache__' --exclude='*.pyc' "$dir" "internal/assets/embedded/$dir" | head -30 >&2 || true
    status=1
  fi
done

for dir in internal/assets/embedded/*/; do
  name=$(basename "$dir")
  case "$name" in
    skills|schemas|workflows|pipelines|lifecycle|integrations) ;;
    *) echo "check-assets: embedded directory $name has no canonical source" >&2; status=1 ;;
  esac
done

# The canonical side may hold caches after local runs (sync-assets strips
# them on copy); the embedded side must never contain them.
cache_hits="$(find internal/assets/embedded \( -name '__pycache__' -o -name '*.pyc' \) -print)"
if [ -n "$cache_hits" ]; then
  echo "check-assets: generated Python cache files in embedded tree (run scripts/sync-assets.sh):" >&2
  echo "$cache_hits" >&2
  status=1
fi

# Dogfood drift: every canonical asset file must reach .ox-agent unchanged.
# One-directional on purpose: .ox-agent may legitimately carry extras
# (README.md, remote or project-local skills), but a canonical file missing
# or different there means the installed toolkit drifts from source.
if [ -d .ox-agent ]; then
  for dir in $asset_dirs; do
    [ -d "$dir" ] || continue
    while IFS= read -r -d '' f; do
      rel="${f#./}"
      if [ ! -f ".ox-agent/$dir/$rel" ]; then
        echo "check-assets: .ox-agent/$dir/$rel missing (run: ox-agent skills update)" >&2
        status=1
      elif ! cmp -s "$dir/$rel" ".ox-agent/$dir/$rel"; then
        echo "check-assets: .ox-agent/$dir/$rel differs from canonical (run: ox-agent skills update)" >&2
        status=1
      fi
    done < <(cd "$dir" && find . -type f -not -name '.*' -not -name '*.pyc' -not -path '*/__pycache__/*' -print0)
  done
fi

if [ "$status" -eq 0 ]; then
  echo "check-assets: embedded tree matches canonical directories; .ox-agent is in sync"
fi
exit "$status"
