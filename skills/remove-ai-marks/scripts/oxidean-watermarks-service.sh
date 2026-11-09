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
#   WATERMARKS_RUN_TOKEN     lease identity for a run's ensure→teardown
#                          pair; default $PPID. Ownership binds to the
#                          parent shell's pid, so ensure and teardown must
#                          share BOTH token and parent — a matching token
#                          across different parents cannot release the
#                          lease (owner check fails) and the service keeps
#                          running while the original owner lives. For
#                          one-shot workflows prefer `run --` which binds
#                          the lease to this helper process; concurrent
#                          workflows under one parent get distinct holds
#                          under the shared token (each teardown consumes
#                          exactly one), but distinct tokens stay safer.
#   WATERMARKS_HOLD        exact hold name (hold-<time>-<pid>) for teardown
#                          to release; ensure prints it on stderr as
#                          "lease-hold=<name>". Set it when several workflows
#                          share a parent AND token: a repeated teardown then
#                          finds its own hold already gone and releases
#                          nothing — without it, an extra teardown silently
#                          consumes a sibling's hold.
#   LEASE_OWNER_PID        override for the recorded lease owner pid (the
#                          `run` subcommand binds holds to the helper pid).
#   WATERMARKS_NO_FLOCK    when set, skip flock(1) and hold the lock via a
#                          python3 helper instead — testing/debug escape
#                          hatch.
#   WATERMARKS_LOCK_SECONDS  cap on waiting for the lifecycle lock
#                          (default 60); a timed-out wait fails the call.
#   WATERMARKS_SERVER_API_KEY  when set, sent as a bearer token on health
#                          checks so an authenticated service answers them.

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
  # The pinned service rejects unauthenticated requests when its API key
  # is configured — a bare probe would read healthy-as-down and ensure
  # would cold-start against a live, authenticated service (#53).
  if [[ -n "${WATERMARKS_SERVER_API_KEY:-}" ]]; then
    curl -sf --max-time 2       -H "Authorization: Bearer ${WATERMARKS_SERVER_API_KEY}"       "${WM_URL}/health" >/dev/null 2>&1
  else
    curl -sf --max-time 2 "${WM_URL}/health" >/dev/null 2>&1
  fi
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
  # kill -0 fails on ESRCH (truly gone) and EPERM (alive but unsignalable —
  # another user's pid under hidepid). Only ESRCH may read as gone.
  if kill -0 "$pid" 2>/dev/null; then
    return 2
  fi
  if owner_confirmed_absent "$pid"; then
    return 1
  fi
  return 2
}

# --- process-identity helpers ----------------------------------------------
# A pid alone is not an identity: the kernel recycles them, and kill -0
# conflates "no such process" (ESRCH) with "not yours to signal" (EPERM).

# 0 = confirmed absent (ESRCH — nothing answers the pid), 1 = present or
# merely inaccessible. Under hidepid an accessible /proc entry may be
# missing for a live foreign pid, so absence there is not proof of death.
owner_confirmed_absent() {
  local owner="$1" kerr
  if kill -0 "$owner" 2>/dev/null; then
    return 1
  fi
  kerr="$(LC_ALL=C kill -0 "$owner" 2>&1 >/dev/null || true)"
  case "$kerr" in
    *"o such process"*|*"o such pid"*) return 0 ;;
    *"ot permitted"*|*"ermission denied"*|*EPERM*) return 1 ;;
  esac
  # Ambiguous wording (non-English locale, unusual kill): fall back to the
  # filesystem oracles.
  if [[ -e /proc/self ]]; then
    [[ -d "/proc/$owner" ]] && return 1
    return 0
  fi
  command -v ps >/dev/null 2>&1 || return 1
  ps -p "$owner" >/dev/null 2>&1 && return 1
  return 0
}

# An exited-but-unreaped owner still answers kill -0 and reports its old
# starttime — only the stat state field proves it is gone for good.
owner_is_zombie() {
  local st
  st="$(python3 - "$1" <<'PY' 2>/dev/null
import sys
try:
    with open("/proc/" + sys.argv[1] + "/stat") as fh:
        data = fh.read()
except OSError:
    sys.exit(1)
print(data.rsplit(")", 1)[1].split()[0])
PY
)" || return 1
  [[ "$st" == "Z" ]]
}

# A lifetime identity stronger than a bare pid: procfs starttime (field 22
# of /proc/<pid>/stat, jiffies since boot) on Linux, `ps -o lstart=` on
# hosts without procfs. A recycled pid never reports the recorded value.
owner_lifetime_id() {
  local pid="$1"
  if [[ -e /proc/self ]]; then
    python3 - "$pid" <<'PY' 2>/dev/null
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
    return
  fi
  command -v ps >/dev/null 2>&1 || return 1
  # lstart renders in the caller's TZ: two helpers under different TZ values
  # would record different identities for the SAME live owner, and teardown
  # could then reap that owner's lease as "recycled". Pin UTC+C locale so the
  # identity is timezone-independent.
  TZ=UTC LC_ALL=C ps -o lstart= -p "$pid" 2>/dev/null | tr -s ' '
}

# The pre-UTC form leases recorded before the TZ pin — only ever compared
# against already-recorded values so a live pre-fix owner is not misread as
# a recycled pid. Never used when writing new records.
owner_lifetime_id_legacy() {
  local pid="$1"
  # procfs starttime is already TZ-independent: on hosts with procfs the
  # legacy form could never have been recorded, so there is nothing to match.
  [[ -e /proc/self ]] && return 1
  command -v ps >/dev/null 2>&1 || return 1
  ps -o lstart= -p "$pid" 2>/dev/null | tr -s ' '
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
    "" | . | .. | .*)
      # Path-unsafe, empty, or leading-dot tokens must never reach the
      # leases dir — a dotfile lease would be invisible to the reap glob
      # and could neither be reaped nor counted by leases_held.
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


# One token owns a directory of holds — a semaphore, not a file: each
# ensure call adds exactly one hold, each teardown consumes exactly one.
# Two workflows sharing a parent (and therefore the default token) then
# get distinct holds, so the first teardown can no longer delete the
# lease out from under the second (#53). A workflow that ensures twice
# without tearing down leaks the extra hold until its owner dies and the
# reaper collects it — leakage beats killing a live run's service.
lease_dir() {
  printf '%s/%s' "$LEASES_DIR" "$(run_token)"
}

# A lease written before the holds layout is a single FILE at the token
# path — where the per-token dir must now live. migrate moves a live (or
# unverifiable) claim into the dir as a hold so its owner can still release
# it and the reaper still sees it; a confirmed-dead one is removed in place.
# Callers run under the lifecycle lock, so this is already serialized.
migrate_legacy_lease() {
  local legacy staging f owner st
  legacy="$(lease_dir)"
  # Recover a migration killed mid-move first: its staging dir can still
  # hold the claim file — put it back at the token path before proceeding.
  for staging in "${legacy}".migrate.*; do
    [[ -d "$staging" ]] || continue
    for f in "$staging"/hold-*; do
      [[ -e "$f" && ! -e "$legacy" ]] && mv "$f" "$legacy" 2>/dev/null
    done
    rm -rf "$staging" 2>/dev/null || true
  done
  [[ -f "$legacy" ]] || return 0
  owner="$(lease_field "$legacy" owner_pid)"
  st=0
  lease_owner_state "$owner" "$(lease_field "$legacy" owner_start)" || st=$?
  if [[ "$st" -eq 1 ]]; then
    rm -f "$legacy"
    return 0
  fi
  # Live/unverifiable: keep the claim. Park the file aside, create the dir,
  # and move the record in as a hold — owner identity travels with it.
  staging="${legacy}.migrate.$$"
  if mkdir "$staging" 2>/dev/null \
    && mv "$legacy" "$staging/hold-legacy-${owner:-unknown}" 2>/dev/null \
    && mv "$staging" "$legacy" 2>/dev/null; then
    return 0
  fi
  # Put back whatever moved rather than strand the claim.
  if [[ -e "$staging/hold-legacy-${owner:-unknown}" && ! -e "$legacy" ]]; then
    mv "$staging/hold-legacy-${owner:-unknown}" "$legacy" 2>/dev/null || true
  fi
  rmdir "$staging" 2>/dev/null || true
  return 1
}

lease_field() {
  python3 -c 'import json,sys; v=json.load(open(sys.argv[1])).get(sys.argv[2]); print("" if v is None else v)' "$1" "$2" 2>/dev/null || true
}

lease_owner() {
  # The lease's owning process: LEASE_OWNER_PID when the caller set it
  # (`run` binds holds to the helper process itself), else the parent —
  # the shell driving the workflow, stable across ensure and teardown.
  printf '%s' "${LEASE_OWNER_PID:-$PPID}"
}

WROTE_HOLD=""

write_lease() {
  local dir tmp hold owner start
  migrate_legacy_lease || true
  dir="$(lease_dir)"
  owner="$(lease_owner)"
  mkdir -p "$dir"
  # Publish atomically (temp + rename): a torn write would leave a hold
  # whose owner_pid fails to parse — unverifiable, and therefore held
  # forever by the conservative reap. A failed write must abort the
  # ensure: publishing a partial or empty hold is worse than failing.
  # ${BASHPID:-$$}: macOS ships Bash 3.2, which has no BASHPID — under
  # set -u a bare expansion would abort ensure outright. $$ is unique per
  # helper process and safe here (the calls are never made in a subshell).
  tmp="$dir/.hold.tmp.${BASHPID:-$$}"
  start="$(owner_lifetime_id "$owner" || true)"
  if ! printf '{"owner_pid":%s,"owner_start":"%s"}\n' "$owner" "$start" >"$tmp"; then
    rm -f "$tmp"
    echo "error: cannot write lease into $dir" >&2
    return 1
  fi
  # hold-<time>-<pid> sorts oldest-first in the drop glob below.
  hold="$dir/hold-$(date +%s%N 2>/dev/null || date +%s)-${BASHPID:-$$}"
  if ! mv -f "$tmp" "$hold"; then
    rm -f "$tmp"
    echo "error: cannot publish lease into $dir" >&2
    return 1
  fi
  WROTE_HOLD="$hold"
  # Channel for callers juggling several workflows under one parent: capture
  # this and export WATERMARKS_HOLD=<name> so the matching teardown releases
  # exactly this hold instead of consuming an arbitrary sibling hold.
  printf 'lease-hold=%s\n' "$(basename "$hold")" >&2
}

# Drop the hold this process's ensure just wrote — used by ensure's own
# failure paths, where the target is known exactly rather than consumed
# oldest-first.
drop_wrote_lease() {
  [[ -n "$WROTE_HOLD" && -e "$WROTE_HOLD" ]] && rm -f "$WROTE_HOLD"
  rmdir "$(lease_dir)" 2>/dev/null || true
}

# Consume exactly one of this run's outstanding holds. Holds under a shared
# token are fungible: a teardown that consumes one hold leaves the remaining
# runs' claims intact, so an interleaved workflow's teardown can never
# delete a sibling's claim (#53). A successor's hold under the same token is
# never taken — ownership is verified against the recorded pid before
# unlinking, and a hold whose recorded lifetime is a DIFFERENT incarnation
# of our pid is a stale claim left for the reaper, not ours to consume.
drop_own_lease() {
  local dir owner hold o os_ now legacy_now named picked
  migrate_legacy_lease || true
  dir="$(lease_dir)"
  owner="$(lease_owner)"
  # WATERMARKS_HOLD names the exact hold this run's ensure published (it is
  # printed on ensure's stderr as lease-hold=<name>). With it set, a repeated
  # teardown finds its own hold already gone and releases nothing — it can
  # no longer eat a sibling workflow's hold under a shared token (#53).
  if [[ -n "${WATERMARKS_HOLD:-}" ]]; then
    case "$WATERMARKS_HOLD" in
      hold-*[!/]*) ;;
      *)
        echo "error: WATERMARKS_HOLD must be a hold-* name (got: $WATERMARKS_HOLD)" >&2
        return 1
        ;;
    esac
    named="$dir/$WATERMARKS_HOLD"
    if [[ -f "$named" ]] && [[ "$(lease_field "$named" owner_pid)" == "$owner" ]]; then
      rm -f "$named"
    fi
    rmdir "$dir" 2>/dev/null || true
    return 0
  fi
  [[ -d "$dir" ]] || return 0
  now="$(owner_lifetime_id "$owner" 2>/dev/null)" || now=""
  legacy_now=""
  picked=""
  for hold in "$dir"/hold-*; do
    [[ -e "$hold" ]] || continue
    o="$(lease_field "$hold" owner_pid)"
    [[ "$o" == "$owner" ]] || continue
    os_="$(lease_field "$hold" owner_start)"
    if [[ -n "$now" && "$os_" == "$now" ]]; then
      picked="$hold"
      break
    fi
    if [[ -n "$os_" && "$os_" != "$now" ]]; then
      # A recorded lifetime that differs from the owner's current one is a
      # stale incarnation — unless it is the pre-UTC legacy form of the same
      # live owner. Either way it is only consumed when it provably matches.
      if [[ -z "$legacy_now" ]]; then
        legacy_now="$(owner_lifetime_id_legacy "$owner" 2>/dev/null)" || legacy_now=""
      fi
      if [[ -n "$legacy_now" && "$os_" == "$legacy_now" ]]; then
        picked="$hold"
        break
      fi
      continue
    fi
    # Unverifiable (empty owner_start, or our own oracle is silent): keep as
    # fallback rather than taking it eagerly ahead of a provable match.
    [[ -z "$picked" ]] && picked="$hold"
  done
  [[ -n "$picked" ]] && rm -f "$picked"
  rmdir "$dir" 2>/dev/null || true
}

# 0 = owner confirmed alive, 1 = confirmed dead, 2 = unverifiable.
lease_owner_state() {
  local owner="$1" recorded_start="${2:-}"
  [[ "$owner" =~ ^[0-9]+$ ]] || return 2
  # A recorded lifetime id upgrades identity from "pid exists" to "same
  # process": a live pid whose identity differs was recycled after the
  # owner's death and the lease is confirmed-stale.
  if [[ -n "$recorded_start" ]]; then
    local now=""
    now="$(owner_lifetime_id "$owner" 2>/dev/null)" || now=""
    if [[ -n "$now" ]]; then
      if [[ "$now" != "$recorded_start" ]]; then
        # Maybe a lease recorded before the TZ pin — the same live owner can
        # legitimately carry the caller-locale lstart. Only when neither the
        # canonical nor the legacy form matches is the pid truly recycled.
        local legacy_now=""
        legacy_now="$(owner_lifetime_id_legacy "$owner" 2>/dev/null)" || legacy_now=""
        if [[ -z "$legacy_now" || "$legacy_now" != "$recorded_start" ]]; then
          return 1
        fi
      fi
      # Same lifetime, but an unreaped zombie is still gone — check the
      # stat state field, not just existence.
      owner_is_zombie "$owner" && return 1
      return 0
    fi
    # Identity oracle silent: only a confirmed-absent pid counts as dead —
    # under hidepid a live foreign pid keeps its lease.
    owner_confirmed_absent "$owner" && return 1
    return 2
  fi
  if kill -0 "$owner" 2>/dev/null; then
    owner_is_zombie "$owner" && return 1
    return 0
  fi
  # kill -0 conflates ESRCH (dead) with EPERM (alive, not ours — e.g.
  # hidepid). Only a confirmed-absent verdict may reap.
  if owner_confirmed_absent "$owner"; then
    return 1
  fi
  return 2
}

reap_leases() {
  local dir hold owner owner_start st tmp
  for dir in "$LEASES_DIR"/*/; do
    [[ -d "$dir" ]] || continue
    # Abandoned publication temporaries: an ensure killed between the tmp
    # write and the rename leaves .hold.tmp.* behind, which would pin the
    # token dir (and therefore the service) forever. A real publish moves in
    # milliseconds — anything older than a minute cannot be in-flight.
    for tmp in "$dir".hold.tmp.*; do
      [[ -e "$tmp" ]] || continue
      if python3 -c 'import os,sys,time; sys.exit(0 if time.time()-os.stat(sys.argv[1]).st_mtime>60 else 1)' "$tmp" 2>/dev/null; then
        rm -f "$tmp"
      fi
    done
    for hold in "$dir"hold-*; do
      [[ -e "$hold" ]] || continue
      owner="$(lease_field "$hold" owner_pid)"
      owner_start="$(lease_field "$hold" owner_start)"
      st=0
      lease_owner_state "$owner" "$owner_start" || st=$?
      [[ "$st" -eq 1 ]] && rm -f "$hold"
    done
    rmdir "$dir" 2>/dev/null || true
  done
  # Pre-holds layout left single-file leases keyed by token; reap them
  # with the same owner rules.
  local legacy
  for legacy in "$LEASES_DIR"/*; do
    [[ -f "$legacy" ]] || continue
    owner="$(lease_field "$legacy" owner_pid)"
    owner_start="$(lease_field "$legacy" owner_start)"
    st=0
    lease_owner_state "$owner" "$owner_start" || st=$?
    [[ "$st" -eq 1 ]] && rm -f "$legacy"
  done
}

leases_held() {
  local entry
  for entry in "$LEASES_DIR"/*; do
    [[ -e "$entry" ]] && return 0
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

# Serialize the whole ensure/teardown lifecycle on ENSURE_LOCK. Lease-write
# + health-check + start must be atomic against teardown's check-and-stop:
# without one lock, a reuse decision can land between teardown's
# empty-lease check and the kill, pinning a lease on a service about to
# die. flock(1) does the job on Linux; where it is absent a python3 helper
# holds the SAME kernel lock — python is a hard dependency of this script,
# so the fallback adds no new requirement. Kernel locking needs no reclaim
# protocol: a dead holder's descriptor releases itself, so there is no
# lockdir, no owner file, and no stale path a waiter could move (#53).
# A timed-out wait FAILS the operation: proceeding unserialized reopens
# the check-and-stop gap the lock exists to close. The wait is bounded by
# WATERMARKS_LOCK_SECONDS (default 60).
take_lifecycle_lock() {
  LOCK_FD=""
  LOCK_HELPER_PID=""
  mkdir -p "$STATE_DIR"
  if [[ -z "${WATERMARKS_NO_FLOCK:-}" ]] && command -v flock >/dev/null 2>&1; then
    exec {LOCK_FD}>"$ENSURE_LOCK"
    if flock -w "${WATERMARKS_LOCK_SECONDS:-60}" "$LOCK_FD"; then
      return 0
    fi
    eval "exec ${LOCK_FD}>&-"
    LOCK_FD=""
    echo "error: timed out acquiring $ENSURE_LOCK" >&2
    return 1
  fi
  # The helper flocks the lock file, signals readiness on a flag file,
  # then waits for SIGTERM — or exits itself when this shell dies, so a
  # killed ensure/teardown can never pin the lock behind a corpse.
  local ready="${ENSURE_LOCK}.ready.$$"
  rm -f "$ready"
  python3 - "$ENSURE_LOCK" "$ready" "$$" "${WATERMARKS_LOCK_SECONDS:-60}" <<'PYLOCK' &
import fcntl, os, signal, sys, time

path, ready, ppid_s = sys.argv[1], sys.argv[2], sys.argv[3]
budget = float(sys.argv[4])
fd = os.open(path, os.O_CREAT | os.O_RDWR, 0o600)
deadline = time.monotonic() + budget
while True:
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        break
    except OSError:
        if time.monotonic() >= deadline:
            sys.exit(3)
        time.sleep(0.1)
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
with open(ready, "w") as r:
    r.write("locked\n")
    os.fsync(r.fileno())
# Watchdog: the invoking shell's death must drop the lock — an orphaned
# holder is an unreclaimable lock.
ppid = int(ppid_s)
while True:
    time.sleep(1)
    try:
        os.kill(ppid, 0)
    except OSError:
        sys.exit(0)
PYLOCK
  LOCK_HELPER_PID=$!
  local i=0
  while (( i < 1400 )); do
    if [[ -f "$ready" ]]; then
      rm -f "$ready"
      return 0
    fi
    if ! kill -0 "$LOCK_HELPER_PID" 2>/dev/null; then
      wait "$LOCK_HELPER_PID" 2>/dev/null
      LOCK_HELPER_PID=""
      rm -f "$ready"
      echo "error: timed out acquiring $ENSURE_LOCK" >&2
      return 1
    fi
    sleep 0.05
    (( i++ )) || true
  done
  kill "$LOCK_HELPER_PID" 2>/dev/null || true
  wait "$LOCK_HELPER_PID" 2>/dev/null || true
  LOCK_HELPER_PID=""
  rm -f "$ready"
  echo "error: lock helper did not report on $ENSURE_LOCK" >&2
  return 1
}

release_lifecycle_lock() {
  if [[ -n "${LOCK_HELPER_PID:-}" ]]; then
    kill "$LOCK_HELPER_PID" 2>/dev/null || true
    wait "$LOCK_HELPER_PID" 2>/dev/null || true
    LOCK_HELPER_PID=""
  fi
  if [[ -n "${LOCK_FD:-}" ]]; then
    eval "exec ${LOCK_FD}>&-"
    LOCK_FD=""
  fi
}

ensure() {
  local LOCK_FD="" LOCK_HELPER_PID=""
  take_lifecycle_lock || return 1
  local ec=0
  ensure_locked || ec=$?
  release_lifecycle_lock
  return "$ec"
}

ensure_locked() {
  # The hold goes down before anything else: teardown only ever consumes
  # it. Under the lock, the health check that follows can race nothing —
  # a teardown's check-and-stop is serialized behind us.
  write_lease || return 1

  if health_ok; then
    reuse_existing
    return
  fi

  # Bash clears errexit inside the $(ensure) command substitution, so a bare
  # `ensure_checkout` that fails would not abort: ensure would still launch the
  # (unverified or missing) server. Propagate explicitly — and release the
  # exact hold this attempt wrote, since the traps that would normally
  # clean up are not yet installed.
  ensure_checkout || { drop_wrote_lease; return 1; }

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
  if ! write_state true "$pid"; then
    # No state means teardown treats the new server as unmanaged and
    # leaves it running — stop it and release this attempt's hold
    # instead of reporting STARTED on an unrecorded server.
    echo "error: cannot record service state in $STATE_FILE" >&2
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    drop_wrote_lease
    return 1
  fi

  local i=0
  while (( i < 40 )); do
    if health_ok; then
      echo "STARTED"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "error: watermarks service exited before becoming healthy; see $LOG_FILE" >&2
      write_state false null
      drop_wrote_lease
      return 1
    fi
    sleep 0.25
    (( i++ )) || true
  done

  echo "error: timed out waiting for ${WM_URL}/health; see $LOG_FILE" >&2
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  write_state false null
  drop_wrote_lease
  return 1
}

teardown() {
  local LOCK_FD="" LOCK_HELPER_PID=""
  take_lifecycle_lock || return 1
  local ec=0
  teardown_locked || ec=$?
  release_lifecycle_lock
  return "$ec"
}

teardown_locked() {
  # Check-and-stop holds the same lifecycle lock as ensure, so a concurrent
  # ensure cannot file a hold and reuse the service in the gap between the
  # empty-check and the kill.

  # This run is done with the service: drop its lease (verified by owner pid,
  # never a bare name-delete), then reap leases whose owners died without
  # teardown. Any surviving lease means another run still needs the service —
  # leaving it running is the whole point (#47).
  # An invalid WATERMARKS_HOLD is a caller error, not a released hold —
  # surfacing 0/LEFT_RUNNING here would leave the real hold pinning the
  # service while the caller believes it was dropped (#53).
  drop_own_lease || return 1
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
    # ensure+teardown happen inside this one process: the script's own pid
    # is a lease token unique to this invocation AND the correct owner —
    # the hold lives and dies with the helper, not whoever launched it, so
    # a backgrounded `run` keeps its claim after the launcher exits and a
    # killed helper's claim is reaped (#53).
    WATERMARKS_RUN_TOKEN="${WATERMARKS_RUN_TOKEN:-run-$$}"
    export WATERMARKS_RUN_TOKEN
    # The owner override stays private to this helper: exported, it would
    # leak into the payload's environment, and a backgrounded child
    # workflow's ensure would write holds owned by a pid that exits with
    # this process — reaped while still in use (#53).
    LEASE_OWNER_PID=$$
    run_with_service "$@"
    ;;
  *)
    echo "usage: $0 {ensure|teardown|run -- <cmd...>}" >&2
    exit 2
    ;;
esac
