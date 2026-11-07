#!/usr/bin/env bash
# Verifies the embedded asset tree matches the canonical directories exactly.
# Run by CI; contributors fix drift with scripts/sync-assets.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

status=0
for dir in skills schemas workflows pipelines lifecycle integrations; do
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
if [ "$status" -eq 0 ]; then
  echo "check-assets: embedded tree matches canonical directories"
fi
exit "$status"
