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
echo "sync-assets: embedded tree updated"
