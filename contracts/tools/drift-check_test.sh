#!/usr/bin/env bash
# Regression test for the contract drift gate (issue #3).
#
# The drift gate must fail when contracts/ or gen/ carry ANY of:
#   - unstaged tracked changes
#   - staged/index (cached) changes
#   - untracked non-ignored files
# ...and pass ONLY when those paths exactly match committed HEAD. It must NOT
# be tripped by legitimately-ignored build caches (e.g. gen/python/.ruff_cache).
#
# Hermetic: builds a throwaway git repo in a temp dir, exercises the real
# detection script (drift-check.sh), and removes every fixture on exit. No
# network, no real regeneration (the slow `task: generate` is out of scope here;
# this test covers the state-detection half of the gate, which is where the bug
# lived). Run via `task contracts:drift:test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DRIFT="${SCRIPT_DIR}/drift-check.sh"

sandbox="$(mktemp -d -t drift-check-test.XXXXXX)"
trap 'rm -rf "$sandbox"' EXIT

git init -q "$sandbox"
(
  cd "$sandbox"
  git config user.email test@example.invalid
  git config user.name "drift test"
  git config commit.gpgsign false
  mkdir -p contracts gen/go gen/python
  printf 'spec\n' > contracts/gateway.openapi.yaml
  printf 'package gen\n' > gen/go/types.gen.go
  # Mirror the real repo: ruff cache under gen/ is ignored, never committed.
  printf 'gen/python/.ruff_cache/\n' > .gitignore
  git add -A
  git commit -qm 'init committed contract tree'
)

fail=0

# Runs the detection script against the sandbox tree; returns its exit code.
run_gate() {
  ( cd "$sandbox" && bash "$DRIFT" ) >/dev/null 2>&1
}

assert_clean() { # $1 = label
  if run_gate; then
    echo "PASS: ${1} -> exit 0 (clean)"
  else
    echo "FAIL: ${1} -> expected exit 0, got non-zero"
    fail=1
  fi
}

assert_dirty() { # $1 = label
  if run_gate; then
    echo "FAIL: ${1} -> expected non-zero, got exit 0"
    fail=1
  else
    echo "PASS: ${1} -> non-zero (drift detected)"
  fi
}

# 0. Baseline clean tree passes.
assert_clean "clean tree"

# 1. Unstaged tracked modification.
printf 'drift\n' >> "$sandbox/gen/go/types.gen.go"
assert_dirty "unstaged tracked change"
( cd "$sandbox" && git checkout -q -- gen/go/types.gen.go )

# 2. Staged (index-only) change.
printf 'drift\n' >> "$sandbox/gen/go/types.gen.go"
( cd "$sandbox" && git add gen/go/types.gen.go )
assert_dirty "staged/cached change"
( cd "$sandbox" && git reset -q HEAD gen/go/types.gen.go && git checkout -q -- gen/go/types.gen.go )

# 3. Untracked newly-generated file.
printf 'package gen\n' > "$sandbox/gen/go/new_generated_file.go"
assert_dirty "untracked generated file"
rm -f "$sandbox/gen/go/new_generated_file.go"

# 4. False-positive guard: a genuinely ignored cache must NOT trip the gate.
mkdir -p "$sandbox/gen/python/.ruff_cache"
printf 'cache\n' > "$sandbox/gen/python/.ruff_cache/CACHEDIR.TAG"
assert_clean "ignored cache present"
rm -rf "$sandbox/gen/python/.ruff_cache"

# 5. Restored tree is clean again.
assert_clean "restored tree"

if [ "$fail" -ne 0 ]; then
  echo "drift-check regression: FAILURES above" >&2
  exit 1
fi
echo "drift-check regression: all scenarios passed"
