#!/usr/bin/env bash
# Contract drift detection (issue #3).
#
# After `task contracts:generate`, the committed tree under contracts/ and gen/
# MUST exactly match HEAD. `git diff` alone reports NEITHER untracked files NOR
# index-only (staged) changes, so newly-generated or staged generated files can
# slip through while the gate exits 0. This checks all three dirty states and
# fails on any of them, naming which state drifted so the fix is obvious.
#
# Scoped strictly to contracts/ and gen/. `--exclude-standard` preserves
# .gitignore semantics so legitimately-ignored build caches (e.g.
# gen/python/.ruff_cache) never trip the gate.
#
# Run from the repo root (the `drift` task sets dir: {{.ROOT_DIR}}). The
# detection logic is exercised in isolation by drift-check_test.sh.
set -euo pipefail

paths=(contracts gen)
status=0

# 1. Unstaged tracked modifications.
if ! git diff --exit-code -- "${paths[@]}" >/dev/null; then
  echo "contract drift: UNSTAGED changes under contracts/ or gen/." >&2
  echo "  Regeneration changed committed files. Run 'task contracts:generate' and commit the result." >&2
  git diff --name-status -- "${paths[@]}" >&2
  status=1
fi

# 2. Staged / index-only changes (git diff misses these).
if ! git diff --cached --exit-code -- "${paths[@]}" >/dev/null; then
  echo "contract drift: STAGED (index) changes under contracts/ or gen/." >&2
  echo "  The staged gen/ tree does not match HEAD. Commit the regenerated tree in the same commit as the source change." >&2
  git diff --cached --name-status -- "${paths[@]}" >&2
  status=1
fi

# 3. Untracked, non-ignored files (git diff misses these too).
untracked="$(git ls-files --others --exclude-standard -- "${paths[@]}")"
if [ -n "$untracked" ]; then
  echo "contract drift: UNTRACKED generated files under contracts/ or gen/." >&2
  echo "  Regeneration produced files that are not committed. Add them (or .gitignore genuine caches):" >&2
  echo "$untracked" | sed 's/^/    /' >&2
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo "contract drift: clean — contracts/ and gen/ match HEAD."
fi

exit "$status"
