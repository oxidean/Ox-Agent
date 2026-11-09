#!/usr/bin/env bash
# Oxidean helper: ensure watermarks-remover HTTP service for a run, then stop
# it only when this script started it AND no run still holds a lease. Never
# kill a pre-existing listener.
#
# Usage:
#   oxidean-watermarks-service.sh ensure          # start if needed; print STARTED|REUSED
#   oxidean-watermarks-service.sh teardown        # drop this run's lease; stop
#                                                 # only when no leases remain
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
#   WATERMARKS_RUN_TOKEN     lease identity for a run's ensure→teardown pair;
#                          default $PPID. Lease ownership binds to the
#                          calling shell's pid, so ensure and teardown must
#                          share BOTH token and parent shell; set the token
#                          explicitly when they run under different parents
#                          that share a supervisor, and never let two
#                          concurrent runs share one token.

set -euo pipefail

WM_URL="${WATERMARKS_SERVICE_URL:-http://127.0.0.1:8765}"
if [[ "$WM_URL" =~ ^[a-zA-Z]+://[^/]+:([0-9]+)(/.*)?$ ]]; then
  WM_PORT="${BASH_REMATCH[1]}"
else
  # Without an explicit port the health check would probe the scheme default
  # (80/443) while the server binds a different port: a guaranteed timeout.
  echo "error: WATERMARKS_SERVICE_URL must include an explicit port (got: $WM_URL)" >&2
  exit 2
fi
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OXIDEAN_ROOT="${OXIDEAN_ROOT:-$(cd "$SCRIPT_DIR/../../../.." && pwd)}"
CHECKOUT="${WATERMARKS_CHECKOUT:-$OXIDEAN_ROOT/tmp/watermarks-remover}"
PIN_TAG="${WATERMARKS_PIN_TAG:-v0.7.0}"
PIN_COMMIT="${WATERMARKS_PIN_COMMIT:-321d93d2efd6a8b26915c5eb5193d9d1701e2c4b}"
CLONE_URL="${WATERMARKS_CLONE_URL:-https://github.com/guillaumemeyer/watermarks-remover.git}"
STATE_DIR="${OXIDEAN_ROOT}/tmp"
STATE_FILE="${STATE_DIR}/watermarks-service.agent.json"
LOG_FILE="${STATE_DIR}/watermarks-service.agent.log"
LEASES_DIR="${STATE_DIR}/watermarks-service.leases"
ENSURE_LOCK="${STATE_DIR}/watermarks-service.ensure.lock"

health_ok() {
  curl -sf --max-time 2 "${WM_URL}/health" >/dev/null 2>&1
}

# Ownership is a three-state question: 0 = confirmed ours, 1 = confirmed
# gone (exited or the PID was recycled), 2 = alive but identity unverifiable.
# Callers must signal only on 0 and must keep ownership records on 2.
server_state() {
  local pid="$1" args=""
  if [[ -d "/proc/$pid" ]]; then
    if [[ -r "/proc/$pid/cmdline" ]]; then
      # stderr first: the process may exit between the -r test and the read,
      # and a failed "<" redirect reports before a later 2>/dev/null applies.
      args="$(tr '\0' ' ' 2>/dev/null <"/proc/$pid/cmdline" || true)"
      case "$args" in
        *service/scripts/server.py*) return 0 ;;
      esac
      return 1  # readable entry that isn't our server: gone or recycled PID
    fi
    # Unreadable cmdline: either the process just exited (entry now gone too)
    # or the kernel hides its details (hidepid, permissions). Only a still-
    # present-but-unreadable entry is genuinely unverifiable.
    [[ -d "/proc/$pid" ]] || return 1
    return 2
  fi
  # /proc the directory can exist while procfs sits unmounted (empty dirs in
  # minimal images); /proc/self only resolves on a live mount.
  [[ -e /proc/self ]] && return 1
  # No procfs (macOS, minimal images): ps identifies, kill -0 verifies liveness.
  if args="$(ps -p "$pid" -o args= 2>/dev/null)"; then
    case "$args" in
      *service/scripts/server.py*) return 0 ;;
      "") ;;                    # empty args: fall through to liveness check
      *) return 1 ;;            # alive, verifiably not ours: recycled PID
    esac
  fi
  kill -0 "$pid" 2>/dev/null && return 2
  return 1
}

# --- run leases -------------------------------------------------------------
# A run's lease records that it still needs the service. teardown drops its
# own lease and only stops an agent-started service when no leases remain —
# a second run's teardown must never kill the service out from under the run
# that started it (#47). Leases are reaped conservatively: an owner pid that
# is verifiably dead drops the lease, anything unverifiable keeps it.

run_token() {
  local raw="${WATERMARKS_RUN_TOKEN:-$PPID}"
  local t
  t="$(printf '%s' "$raw" | tr -cd 'a-zA-Z0-9._-')"
  case "$t" in
    "" | . | ..)
      # Path-unsafe or empty tokens must never reach the leases dir.
      t="run-$PPID"
      ;;
    *)
      if [[ "$t" != "$raw" ]]; then
        # Sanitization alone collides distinct tokens ("job/a" and "joba"):
        # suffix a digest of the raw token so the lease name stays
        # collision-resistant.
        local h
        h="$(python3 -c 'import hashlib,sys; print(hashlib.sha256(sys.argv[1].encode()).hexdigest()[:10])' "$raw")"
        t="${t}-${h}"
      fi
      ;;
  esac
  printf '%s' "$t"
}

# A pid alone is not an identity: the kernel recycles them. starttime (field
# 22 of /proc/<pid>/stat, jiffies since boot) is bound to one process
# lifetime — a recycled pid never reports the recorded value, so a mismatch
# is proof the lease owner is gone rather than merely unverifiable.
pid_start_time() {
  python3 - "$1" <<'PY' 2>/dev/null
import sys
try:
    with open("/proc/" + sys.argv[1] + "/stat") as fh:
        data = fh.read()
except OSError:
    sys.exit(1)
# comm may contain spaces/parens; fields after its closing ')' shift by two,
# so starttime (field 22) sits at index 19 of the remainder.
print(data.rsplit(")", 1)[1].split()[19])
PY
}

lease_file() {
  printf '%s/%s' "$LEASES_DIR" "$(run_token)"
}

write_lease() {
  mkdir -p "$LEASES_DIR"
  # Publish atomically (temp + rename): a torn write would leave a lease
  # whose owner_pid fails to parse — unverifiable, and therefore held
  # forever by the conservative reap. The dot-prefixed temp stays out of
  # the reap glob.
  local target tmp start
  target="$(lease_file)"
  tmp="$LEASES_DIR/.lease.tmp.$$"
  # The owning process is the caller's parent: the shell driving the run.
  # Subshells inherit $PPID, so `$(ensure)` and `run` see the same token.
  start="$(pid_start_time "$PPID" || true)"
  printf '{"owner_pid":%s,"owner_start":"%s"}\n' "$PPID" "$start" >"$tmp"
  mv -f "$tmp" "$target"
}

# Drop only the lease this run wrote. The path is keyed by token, but the
# record inside names the owner — deleting by name alone could remove a
# successor's lease that rewrote the same token, so ownership is verified
# against the recorded pid before unlinking.
drop_own_lease() {
  local lease owner
  lease="$(lease_file)"
  [[ -f "$lease" ]] || return 0
  owner="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("owner_pid") or "")' "$lease" 2>/dev/null || true)"
  [[ "$owner" == "$PPID" ]] || return 0
  rm -f "$lease"
}

# 0 = owner confirmed alive, 1 = confirmed dead, 2 = unverifiable.
lease_owner_state() {
  local owner="$1" recorded_start="${2:-}"
  [[ "$owner" =~ ^[0-9]+$ ]] || return 2
  # A recorded starttime upgrades identity from "pid exists" to "same
  # process": a live pid whose starttime differs was recycled after the
  # owner's death and the lease is confirmed-stale.
  if [[ -n "$recorded_start" && -e /proc/self ]]; then
    [[ -d "/proc/$owner" ]] || return 1
    local now
    if now="$(pid_start_time "$owner")"; then
      [[ "$now" == "$recorded_start" ]] && return 0
      return 1
    fi
    # Present but stat unreadable: identity can't be confirmed — keep.
    return 2
  fi
  if kill -0 "$owner" 2>/dev/null; then
    return 0
  fi
  # EPERM reports as failure but proves the pid exists — only an absent
  # /proc entry (on a live procfs) or a failed ps lookup means dead.
  if [[ -d "/proc/$owner" ]]; then
    return 0
  fi
  if [[ -e /proc/self ]]; then
    return 1
  fi
  # No procfs (macOS, minimal images): ps is the oracle — a nonzero exit on
  # `ps -p` is confirmed-absent, not merely unverifiable. Without ps there is
  # no oracle at all, so stay conservative.
  if command -v ps >/dev/null 2>&1; then
    if ps -p "$owner" >/dev/null 2>&1; then
      return 0
    fi
    return 1
  fi
  return 2
}

reap_leases() {
  local lease owner owner_start st
  for lease in "$LEASES_DIR"/*; do
    [[ -e "$lease" ]] || continue
    owner="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("owner_pid") or "")' "$lease" 2>/dev/null || true)"
    owner_start="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("owner_start") or "")' "$lease" 2>/dev/null || true)"
    st=0
    lease_owner_state "$owner" "$owner_start" || st=$?
    if [[ "$st" -eq 1 ]]; then
      rm -f "$lease"
    fi
  done
}

leases_held() {
  local lease
  for lease in "$LEASES_DIR"/*; do
    [[ -e "$lease" ]] && return 0
  done
  return 1
}

verify_checkout() {
  # A tag is a movable ref: clone it, then prove the commit is the one we
  # pinned before any code from the checkout runs on this host.
  if [[ ! -e "$CHECKOUT/.git" ]]; then
    return 0  # user-managed tree (vendored, not cloned) — trust it
  fi
  local actual
  actual="$(git -C "$CHECKOUT" rev-parse HEAD 2>/dev/null || true)"
  if [[ "$actual" != "$PIN_COMMIT" ]]; then
    echo "error: $CHECKOUT is at ${actual:-<unknown>}, expected $PIN_COMMIT ($PIN_TAG)" >&2
    echo "refusing to run unverified service code; update WATERMARKS_PIN_TAG and WATERMARKS_PIN_COMMIT together" >&2
    return 1
  fi
  # HEAD alone is not proof of the code that runs: a modified tracked file or
  # an untracked file (e.g. a dropped-in sitecustomize.py) changes what
  # `python3 service/scripts/server.py` executes while rev-parse still passes.
  # A failed `git status` must not read as clean, and -uall overrides any
  # status.showUntrackedFiles=no config that would hide dropped-in files.
  local porcelain
  if ! porcelain="$(git -C "$CHECKOUT" status --porcelain --untracked-files=all 2>/dev/null)"; then
    echo "error: cannot determine working-tree state of $CHECKOUT" >&2
    return 1
  fi
  if [[ -n "$porcelain" ]]; then
    echo "error: $CHECKOUT has a dirty working tree (modified or untracked files)" >&2
    echo "refusing to run unverified service code; reset the checkout to $PIN_TAG" >&2
    return 1
  fi
}

ensure_checkout() {
  if [[ -x "${CHECKOUT}/service/scripts/server.py" ]] || [[ -f "${CHECKOUT}/service/scripts/server.py" ]]; then
    verify_checkout
    return
  fi
  mkdir -p "$(dirname "$CHECKOUT")"
  if [[ -e "$CHECKOUT/.git" ]]; then
    echo "error: checkout at $CHECKOUT is incomplete (missing server.py)" >&2
    return 1
  fi
  echo "cloning watermarks-remover ${PIN_TAG} → ${CHECKOUT}" >&2
  git clone --depth 1 --branch "$PIN_TAG" \
    "$CLONE_URL" "$CHECKOUT" || return 1
  verify_checkout
}

write_state() {
  local started="$1" pid="$2"
  mkdir -p "$STATE_DIR"
  printf '{"started_by_us":%s,"pid":%s,"url":"%s","checkout":"%s"}\n' \
    "$started" "$pid" "$WM_URL" "$CHECKOUT" >"$STATE_FILE"
}

reuse_existing() {
  # Keep ownership if a prior ensure already started the server.
  if [[ -f "$STATE_FILE" ]]; then
    local started pid
    started="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("started_by_us", False))' "$STATE_FILE")"
    pid="$(python3 -c 'import json,sys; p=json.load(open(sys.argv[1])).get("pid"); print(p if p not in (None, "null") else "")' "$STATE_FILE")"
    if [[ "$started" == "True" || "$started" == "true" ]] && [[ -n "$pid" ]]; then
      local st=0
      server_state "$pid" || st=$?
      # Confirmed-gone is the only state that drops the claim: teardown
      # re-verifies before signalling, so keeping it when unverifiable is
      # safe, while losing it would orphan a server we still own.
      if [[ "$st" -ne 1 ]]; then
        echo "REUSED"
        return 0
      fi
    fi
  fi
  write_state false null
  echo "REUSED"
  return 0
}

# Serialize the whole ensure/teardown lifecycle on ENSURE_LOCK where flock
# exists (Linux). Lease-write + health-check + start must be atomic against
# teardown's check-and-stop: without one lock, a reuse decision can land
# between teardown's empty-lease check and the kill, pinning a lease on a
# service about to die. A stuck holder must not wedge later ops, so the
# wait is bounded and a timeout degrades to the unserialized behavior.
take_lifecycle_lock() {
  LOCK_FD=""
  command -v flock >/dev/null 2>&1 || return 0
  mkdir -p "$STATE_DIR"
  exec {LOCK_FD}>"$ENSURE_LOCK"
  if ! flock -w 60 "$LOCK_FD"; then
    echo "warning: timed out acquiring $ENSURE_LOCK; proceeding unserialized" >&2
    eval "exec ${LOCK_FD}>&-"
    LOCK_FD=""
  fi
}

ensure() {
  local LOCK_FD=""
  take_lifecycle_lock

  # The lease goes down before anything else: teardown only ever removes it.
  # Under the lock, the health check that follows can race nothing — a
  # teardown's check-and-stop is serialized behind us.
  write_lease

  if health_ok; then
    reuse_existing
    return
  fi

  # Bash clears errexit inside the $(ensure) command substitution, so a bare
  # `ensure_checkout` that fails would not abort: ensure would still launch the
  # (unverified or missing) server. Propagate explicitly — and release only
  # the lease this attempt wrote, verified by owner pid, since the traps
  # that would normally clean up are not yet installed.
  ensure_checkout || { drop_own_lease; return 1; }

  mkdir -p "$STATE_DIR"
  : >"$LOG_FILE"
  (
    cd "$CHECKOUT" || exit 1
    # The lock serializes lifecycle ops for ensure only — a long-lived
    # server inheriting the descriptor would pin it until exit and stall
    # every later ensure/teardown behind a dead-weight timeout.
    if [[ -n "$LOCK_FD" ]]; then eval "exec ${LOCK_FD}>&-"; fi
    # make serve blocks; run server directly so we own the PID
    exec python3 service/scripts/server.py --host 127.0.0.1 --port "$WM_PORT"
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
      drop_own_lease
      return 1
    fi
    sleep 0.25
    (( i++ )) || true
  done

  echo "error: timed out waiting for ${WM_URL}/health; see $LOG_FILE" >&2
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  write_state false null
  drop_own_lease
  return 1
}

teardown() {
  # Check-and-stop holds the same lifecycle lock as ensure, so a concurrent
  # ensure cannot file a lease and reuse the service in the gap between the
  # empty-check and the kill.
  local LOCK_FD=""
  take_lifecycle_lock

  # This run is done with the service: drop its lease (verified by owner pid,
  # never a bare name-delete), then reap leases whose owners died without
  # teardown. Any surviving lease means another run still needs the service —
  # leaving it running is the whole point (#47).
  drop_own_lease
  reap_leases
  if leases_held; then
    echo "LEFT_RUNNING"
    return 0
  fi
  if [[ ! -f "$STATE_FILE" ]]; then
    return 0
  fi
  local started pid
  started="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("started_by_us", False))' "$STATE_FILE")"
  pid="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("pid") or "")' "$STATE_FILE")"
  if [[ "$started" != "True" && "$started" != "true" ]]; then
    rm -f "$STATE_FILE"
    echo "LEFT_RUNNING"
    return 0
  fi
  # Stop first, then remove the state file: a signal arriving between the two
  # would otherwise leave the trap's teardown nothing to act on, stranding a
  # running server with no record.
  if [[ -n "$pid" && "$pid" != "None" && "$pid" != "null" ]]; then
    local st=0
    server_state "$pid" || st=$?
    case "$st" in
      0)
        kill "$pid" 2>/dev/null || true
        # `wait` only works on our own children; this server was started by
        # an earlier shell. Poll server_state until confirmed-gone (1); an
        # indeterminate (2) check counts as "not proven dead".
        local i=0
        while (( i < 40 )); do
          server_state "$pid" || st=$?
          [[ "$st" -eq 1 ]] && break
          sleep 0.25
          (( i++ )) || true
        done
        if [[ "$st" -ne 1 ]]; then
          echo "error: watermarks service pid $pid not confirmed stopped; state kept for retry" >&2
          return 1
        fi
        ;;
      1) ;;  # already confirmed gone
      2)
        echo "error: cannot verify watermarks service pid $pid identity; not signalled, state kept" >&2
        return 1
        ;;
    esac
  fi
  rm -f "$STATE_FILE"
  echo "STOPPED"
}

run_with_service() {
  local mode
  mode="$(ensure)"
  # ensure succeeded: the service may be ours to stop. Without a trap, an
  # interrupt skips teardown and leaves it running against a stale state file.
  trap 'teardown >/dev/null || true; exit 130' INT
  trap 'teardown >/dev/null || true; exit 143' TERM
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
    # ensure+teardown happen inside this one process, so the script's own
    # pid is a lease token unique to this invocation — two `run` commands
    # under one parent must never share the $PPID default and steal each
    # other's lease (#47).
    WATERMARKS_RUN_TOKEN="${WATERMARKS_RUN_TOKEN:-run-$$}"
    run_with_service "$@"
    ;;
  *)
    echo "usage: $0 {ensure|teardown|run -- <cmd...>}" >&2
    exit 2
    ;;
esac
