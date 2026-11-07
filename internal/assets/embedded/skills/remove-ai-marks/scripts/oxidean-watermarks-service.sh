#!/usr/bin/env bash
# Oxidean helper: ensure watermarks-remover HTTP service for a run, then stop
# it only if this script started it. Never kill a pre-existing listener.
#
# Usage:
#   oxidean-watermarks-service.sh ensure          # start if needed; print STARTED|REUSED
#   oxidean-watermarks-service.sh teardown        # stop only if we started
#   oxidean-watermarks-service.sh run -- cmd...   # ensure → cmd → teardown
#
# Env:
#   WATERMARKS_SERVICE_URL   default http://127.0.0.1:8765
#   OXIDEAN_ROOT            repo root (auto-detected from this script if unset)
#   WATERMARKS_CHECKOUT      default $OXIDEAN_ROOT/tmp/watermarks-remover
#   WATERMARKS_PIN_TAG       default v0.7.0 (used when cloning)
#   WATERMARKS_PIN_COMMIT    commit v0.7.0 must resolve to (tags are movable;
#                          the clone is verified against this before use)
#   WATERMARKS_CLONE_URL     clone source (default upstream GitHub repo)

set -euo pipefail

WM_URL="${WATERMARKS_SERVICE_URL:-http://127.0.0.1:8765}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OXIDEAN_ROOT="${OXIDEAN_ROOT:-$(cd "$SCRIPT_DIR/../../../.." && pwd)}"
CHECKOUT="${WATERMARKS_CHECKOUT:-$OXIDEAN_ROOT/tmp/watermarks-remover}"
PIN_TAG="${WATERMARKS_PIN_TAG:-v0.7.0}"
PIN_COMMIT="${WATERMARKS_PIN_COMMIT:-321d93d2efd6a8b26915c5eb5193d9d1701e2c4b}"
CLONE_URL="${WATERMARKS_CLONE_URL:-https://github.com/guillaumemeyer/watermarks-remover.git}"
STATE_DIR="${OXIDEAN_ROOT}/tmp"
STATE_FILE="${STATE_DIR}/watermarks-service.agent.json"
LOG_FILE="${STATE_DIR}/watermarks-service.agent.log"

health_ok() {
  curl -sf --max-time 2 "${WM_URL}/health" >/dev/null 2>&1
}

verify_checkout() {
  # A tag is a movable ref: clone it, then prove the commit is the one we
  # pinned before any code from the checkout runs on this host.
  if [[ ! -d "$CHECKOUT/.git" ]]; then
    return 0  # user-managed tree (vendored, not cloned) — trust it
  fi
  local actual
  actual="$(git -C "$CHECKOUT" rev-parse HEAD 2>/dev/null || true)"
  if [[ "$actual" != "$PIN_COMMIT" ]]; then
    echo "error: $CHECKOUT is at ${actual:-<unknown>}, expected $PIN_COMMIT ($PIN_TAG)" >&2
    echo "refusing to run unverified service code; update WATERMARKS_PIN_TAG and WATERMARKS_PIN_COMMIT together" >&2
    return 1
  fi
}

ensure_checkout() {
  if [[ -x "${CHECKOUT}/service/scripts/server.py" ]] || [[ -f "${CHECKOUT}/service/scripts/server.py" ]]; then
    verify_checkout
    return
  fi
  mkdir -p "$(dirname "$CHECKOUT")"
  if [[ -d "$CHECKOUT/.git" ]]; then
    echo "error: checkout at $CHECKOUT is incomplete (missing server.py)" >&2
    return 1
  fi
  echo "cloning watermarks-remover ${PIN_TAG} → ${CHECKOUT}" >&2
  git clone --depth 1 --branch "$PIN_TAG" \
    "$CLONE_URL" "$CHECKOUT"
  verify_checkout
}

write_state() {
  local started="$1" pid="$2"
  mkdir -p "$STATE_DIR"
  printf '{"started_by_us":%s,"pid":%s,"url":"%s","checkout":"%s"}\n' \
    "$started" "$pid" "$WM_URL" "$CHECKOUT" >"$STATE_FILE"
}

ensure() {
  if health_ok; then
    # Keep ownership if a prior ensure in this run already started the server.
    if [[ -f "$STATE_FILE" ]]; then
      local started pid
      started="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("started_by_us", False))' "$STATE_FILE")"
      pid="$(python3 -c 'import json,sys; p=json.load(open(sys.argv[1])).get("pid"); print(p if p not in (None, "null") else "")' "$STATE_FILE")"
      if [[ "$started" == "True" || "$started" == "true" ]] && [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        echo "REUSED"
        return 0
      fi
    fi
    write_state false null
    echo "REUSED"
    return 0
  fi

  ensure_checkout

  mkdir -p "$STATE_DIR"
  : >"$LOG_FILE"
  (
    cd "$CHECKOUT"
    # make serve blocks; run server directly so we own the PID
    exec python3 service/scripts/server.py --host 127.0.0.1 --port 8765
  ) >>"$LOG_FILE" 2>&1 &
  local pid=$!
  write_state true "$pid"

  local i=0
  while (( i < 40 )); do
    if health_ok; then
      echo "STARTED"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "error: watermarks service exited before becoming healthy; see $LOG_FILE" >&2
      write_state false null
      return 1
    fi
    sleep 0.25
    (( i++ )) || true
  done

  echo "error: timed out waiting for ${WM_URL}/health; see $LOG_FILE" >&2
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  write_state false null
  return 1
}

teardown() {
  if [[ ! -f "$STATE_FILE" ]]; then
    return 0
  fi
  local started pid
  started="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("started_by_us", False))' "$STATE_FILE")"
  pid="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("pid") or "")' "$STATE_FILE")"
  rm -f "$STATE_FILE"
  if [[ "$started" != "True" && "$started" != "true" ]]; then
    echo "LEFT_RUNNING"
    return 0
  fi
  if [[ -n "$pid" && "$pid" != "None" && "$pid" != "null" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  echo "STOPPED"
}

run_with_service() {
  local mode
  mode="$(ensure)"
  local ec=0
  "$@" || ec=$?
  teardown >/dev/null || true
  return "$ec"
}

cmd="${1:-}"
case "$cmd" in
  ensure) ensure ;;
  teardown) teardown ;;
  run)
    shift
    if [[ "${1:-}" == "--" ]]; then shift; fi
    if [[ $# -lt 1 ]]; then
      echo "usage: $0 run -- <command...>" >&2
      exit 2
    fi
    run_with_service "$@"
    ;;
  *)
    echo "usage: $0 {ensure|teardown|run -- <cmd...>}" >&2
    exit 2
    ;;
esac
