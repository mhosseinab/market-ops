#!/usr/bin/env bash
set -euo pipefail

# Docker-free regression test for issue #166: the S32 integration entrypoints
# must NEVER destructively mutate the FIXED project file deploy/.env. A
# pre-existing deploy/.env must survive success, failure, and interruption
# byte-for-byte, and no test credential may be left in that path when none
# existed beforehand.
#
# Runs entirely offline. Each script under test is executed inside a throwaway
# SKELETON repo (its own ROOT_DIR, derived from BASH_SOURCE) with a STUB bin dir
# on PATH shadowing docker/pnpm/uv/go/curl/node, and an isolated TMPDIR so the
# private per-run `--env-file` the scripts now use lands somewhere we can prove
# is cleaned up. No real Docker, compose stack, or database is ever touched —
# mirroring tools/dev/db_reset_guard_test.sh, which is the wiring precedent for
# offline shell-tooling gates.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
src_dir="$repo_root/tools/integration"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

failures=0
fail() { echo "FAIL [$1]: $2" >&2; failures=$((failures + 1)); }

# Build a fresh skeleton repo containing the integration scripts and the empty
# directories they cd into, plus a stub bin dir. `docker_exit` controls the
# docker stub's exit code so we can exercise the failure path. Echoes the
# skeleton root on stdout.
make_skeleton() {
  local docker_exit="$1"
  local root
  root="$(mktemp -d "$work/skel.XXXXXX")"

  mkdir -p "$root/tools/integration" "$root/apps/web" "$root/services/core" \
           "$root/deploy" "$root/bin" "$root/tmp"
  cp "$src_dir"/run_all.sh "$src_dir"/run_killswitch_journey.sh \
     "$src_dir"/run_coldstart_llm_unhealthy_journey.sh \
     "$src_dir"/configure_cache.sh "$root/tools/integration/"
  # replay_adversarial.py is invoked (via a stubbed `uv`) but never read here.
  : >"$root/tools/integration/replay_adversarial.py"

  # docker stub: swallow every subcommand/arg. `ps -q` prints a stable token so
  # the cold-start container-identity comparison is well-defined.
  cat >"$root/bin/docker" <<STUB
#!/usr/bin/env bash
for a in "\$@"; do
  if [ "\$a" = "ps" ]; then echo "stub-container-id"; break; fi
done
exit $docker_exit
STUB

  # Every other external the scripts touch: a no-op that also prints a plausible
  # scalar so command substitutions (\$(...)) yield a non-empty value.
  local c
  for c in pnpm uv go node; do
    printf '#!/usr/bin/env bash\nexit 0\n' >"$root/bin/$c"
  done
  # curl is used with -w '%{http_code}' captured into shell vars; print 000 so
  # those captures are non-empty (the exact value is irrelevant to THIS test,
  # which asserts the env-file lifecycle, not journey pass/fail).
  printf '#!/usr/bin/env bash\necho 000\nexit 0\n' >"$root/bin/curl"

  chmod +x "$root/bin/"*
  echo "$root"
}

# Run one script inside a skeleton with the stub PATH and isolated TMPDIR.
# Sets globals: RUN_RC (exit code) and RUN_ROOT (skeleton root).
run_in_skeleton() {
  local script="$1" docker_exit="$2"
  RUN_ROOT="$(make_skeleton "$docker_exit")"
  local rc
  set +e
  env PATH="$RUN_ROOT/bin:$PATH" TMPDIR="$RUN_ROOT/tmp" \
    bash "$RUN_ROOT/tools/integration/$script" >/dev/null 2>&1
  rc=$?
  set -e
  RUN_RC=$rc
}

SENTINEL=$'# a developer\'s real deploy config\nDK_API_TOKEN=super-secret-do-not-erase\nREGION=ir-tehran\n'

# assert_survives SCRIPT DOCKER_EXIT EXPECT_RC_CHECK
# Places a sentinel deploy/.env, runs the script, asserts the file is unchanged
# byte-for-byte and that no private env file leaked into TMPDIR.
assert_survives() {
  local name="$1" script="$2" docker_exit="$3"
  RUN_ROOT="$(make_skeleton "$docker_exit")"
  printf '%s' "$SENTINEL" >"$RUN_ROOT/deploy/.env"

  set +e
  env PATH="$RUN_ROOT/bin:$PATH" TMPDIR="$RUN_ROOT/tmp" \
    bash "$RUN_ROOT/tools/integration/$script" >/dev/null 2>&1
  local rc=$?
  set -e

  if [ ! -f "$RUN_ROOT/deploy/.env" ]; then
    fail "$name" "pre-existing deploy/.env was DELETED (rc=$rc)"
  elif ! printf '%s' "$SENTINEL" | cmp -s - "$RUN_ROOT/deploy/.env"; then
    fail "$name" "pre-existing deploy/.env was MODIFIED (rc=$rc)"
  fi
  # No private compose env file may be left behind in the isolated TMPDIR.
  if ls "$RUN_ROOT/tmp"/market-ops-compose-env.* >/dev/null 2>&1; then
    fail "$name" "leaked a private compose env file in TMPDIR (rc=$rc)"
  fi
  if [ "$failures" -eq "${_before:-0}" ]; then echo "ok   [$name] (rc=$rc)"; fi
}

# assert_none_created SCRIPT DOCKER_EXIT
# With NO deploy/.env beforehand, asserts none is created (no test credential
# left in the fixed path) and no private env file leaks.
assert_none_created() {
  local name="$1" script="$2" docker_exit="$3"
  run_in_skeleton "$script" "$docker_exit"
  if [ -e "$RUN_ROOT/deploy/.env" ]; then
    fail "$name" "created deploy/.env where none existed (rc=$RUN_RC): $(cat "$RUN_ROOT/deploy/.env")"
  elif ls "$RUN_ROOT/tmp"/market-ops-compose-env.* >/dev/null 2>&1; then
    fail "$name" "leaked a private compose env file in TMPDIR (rc=$RUN_RC)"
  else
    echo "ok   [$name] (rc=$RUN_RC)"
  fi
}

run_case() { _before="$failures"; "$@"; }

# --- Acceptance test 1: survives SUCCESSFUL execution. ------------------------
# run_killswitch_journey.sh makes no value-dependent assertions, so with every
# external stubbed to exit 0 it runs to a genuine exit 0 — a real success path.
run_in_skeleton run_killswitch_journey.sh 0
if [ "$RUN_RC" -ne 0 ]; then
  fail "killswitch-success-path" "expected clean exit 0 with all stubs succeeding, got $RUN_RC"
else
  echo "ok   [killswitch-success-path] (rc=0)"
fi
run_case assert_survives "killswitch-sentinel-survives-success" run_killswitch_journey.sh 0

# --- Acceptance test 2: survives a FAILING command / interrupted child. -------
# docker stub exits 1 → `compose up` fails → set -e aborts the script mid-run.
run_case assert_survives "killswitch-sentinel-survives-failure" run_killswitch_journey.sh 1
run_case assert_survives "coldstart-sentinel-survives-failure" run_coldstart_llm_unhealthy_journey.sh 1
run_case assert_survives "coldstart-sentinel-survives-run"     run_coldstart_llm_unhealthy_journey.sh 0

# The orchestrator drives both children (reuse-the-owned-env-file path) and must
# preserve deploy/.env end-to-end on both the up-fails and up-succeeds paths.
run_case assert_survives "run_all-sentinel-survives-failure" run_all.sh 1
run_case assert_survives "run_all-sentinel-survives-run"     run_all.sh 0

# --- Acceptance test 3: no credential left when no file existed beforehand. ---
run_case assert_none_created "killswitch-none-created" run_killswitch_journey.sh 0
run_case assert_none_created "coldstart-none-created"  run_coldstart_llm_unhealthy_journey.sh 0
run_case assert_none_created "run_all-none-created"    run_all.sh 0

# --- Static guard: the destructive pattern must not reappear. -----------------
# No script may write or delete the FIXED project file deploy/.env (comments
# that merely name the path are fine; an actual redirection/removal is not).
for s in run_all.sh run_killswitch_journey.sh run_coldstart_llm_unhealthy_journey.sh; do
  if grep -Eq '(>|>>)[[:space:]]*deploy/\.env|rm[[:space:]]+[^#]*deploy/\.env' "$src_dir/$s"; then
    fail "static-no-destructive-$s" "script still writes or removes the fixed deploy/.env"
  else
    echo "ok   [static-no-destructive-$s]"
  fi
  if ! grep -q -- '--env-file' "$src_dir/$s"; then
    fail "static-uses-env-file-$s" "script no longer passes docker compose --env-file"
  fi
done

if [ "$failures" -ne 0 ]; then
  echo "env_preservation_test: $failures case(s) failed" >&2
  exit 1
fi
echo "env_preservation_test: all deploy/.env preservation cases passed"
