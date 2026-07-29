#!/usr/bin/env bash
# Regression test for tools/deploy/release_images.sh — the seam between the
# release pipeline's image-digests artifact and $ENVFILE.
#
# Everything here runs offline through --from/--print/--env-file: no GitHub CLI,
# no registry, no Docker. The --tag and --resolve paths are thin wrappers around
# the same validation, which is what these fixtures pin.
#
# RED (must be REJECTED — each one would deploy something unintended):
#   1. a tag-shaped reference instead of a digest (mutable: can be moved after
#      the Trivy scan that approved it)
#   2. a truncated / non-hex digest
#   3. an image from a different GHCR namespace
#   4. an artifact missing one of the four components (a pull-request run's
#      output, or a partially edited paste)
#
# GREEN (must be ACCEPTED):
#   5. a well-formed images.env merges into an env file, replacing pre-existing
#      image lines and preserving every other line byte for byte
#   6. the replaced digests land in <env-file>.prev-images (the rollback input)
#   7. the merge is idempotent and does not widen the env file's 0600 mode
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

script="tools/deploy/release_images.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "release_images_test: $1" >&2; exit 1; }

D1="sha256:1111111111111111111111111111111111111111111111111111111111111111"
D2="sha256:2222222222222222222222222222222222222222222222222222222222222222"
D3="sha256:3333333333333333333333333333333333333333333333333333333333333333"
D4="sha256:4444444444444444444444444444444444444444444444444444444444444444"
OLD="sha256:0000000000000000000000000000000000000000000000000000000000000000"

valid_images() {
  cat >"$1" <<EOF
CORE_IMAGE=ghcr.io/mhosseinab/market-ops-core@${D1}
LLM_IMAGE=ghcr.io/mhosseinab/market-ops-llm@${D2}
NGINX_IMAGE=ghcr.io/mhosseinab/market-ops-nginx@${D3}
GOOSE_IMAGE=ghcr.io/mhosseinab/market-ops-goose@${D4}
EOF
}

reject() {
  local label="$1" file="$2"
  if bash "$script" --from "$file" --print >/dev/null 2>&1; then
    fail "$label should have been REJECTED"
  fi
}

# 1. mutable tag instead of a digest
cat >"$work/tagged.env" <<EOF
CORE_IMAGE=ghcr.io/mhosseinab/market-ops-core:v0.1.0
LLM_IMAGE=ghcr.io/mhosseinab/market-ops-llm@${D2}
NGINX_IMAGE=ghcr.io/mhosseinab/market-ops-nginx@${D3}
GOOSE_IMAGE=ghcr.io/mhosseinab/market-ops-goose@${D4}
EOF
reject "a tag-pinned CORE_IMAGE" "$work/tagged.env"

# 2. truncated digest
valid_images "$work/short.env"
sed -i.bak "s|market-ops-llm@${D2}|market-ops-llm@sha256:abc123|" "$work/short.env"
reject "a truncated LLM_IMAGE digest" "$work/short.env"

# 3. foreign namespace
valid_images "$work/foreign.env"
sed -i.bak "s|ghcr.io/mhosseinab/market-ops-nginx|ghcr.io/someone-else/market-ops-nginx|" "$work/foreign.env"
reject "an NGINX_IMAGE from another namespace" "$work/foreign.env"

# 4. missing component (what a pull-request run would leave you with)
valid_images "$work/partial.env"
grep -v '^GOOSE_IMAGE=' "$work/partial.env" >"$work/partial.trimmed" && mv "$work/partial.trimmed" "$work/partial.env"
reject "an artifact with no GOOSE_IMAGE" "$work/partial.env"

# 5-7. the happy path against a realistic env file
valid_images "$work/images.env"
envfile="$work/prod.env"
cat >"$envfile" <<EOF
# comment that must survive
CORE_IMAGE=ghcr.io/mhosseinab/market-ops-core@${OLD}
LLM_IMAGE=ghcr.io/mhosseinab/market-ops-llm@${OLD}
NGINX_IMAGE=ghcr.io/mhosseinab/market-ops-nginx@${OLD}
GOOSE_IMAGE=ghcr.io/mhosseinab/market-ops-goose@${OLD}
POSTGRES_USER=market_ops
CONNECTOR_ENCRYPTION_KEY=not-a-real-key
EOF
chmod 600 "$envfile"

bash "$script" --from "$work/images.env" --env-file "$envfile" >/dev/null ||
  fail "a well-formed images.env should have been accepted"

grep -q "^CORE_IMAGE=ghcr.io/mhosseinab/market-ops-core@${D1}$" "$envfile" ||
  fail "CORE_IMAGE was not updated to the new digest"
grep -q "^GOOSE_IMAGE=ghcr.io/mhosseinab/market-ops-goose@${D4}$" "$envfile" ||
  fail "GOOSE_IMAGE was not updated to the new digest"
[ "$(grep -c '^CORE_IMAGE=' "$envfile")" -eq 1 ] ||
  fail "CORE_IMAGE was duplicated instead of replaced"
grep -q '^# comment that must survive$' "$envfile" ||
  fail "an unrelated comment line was dropped"
grep -q '^CONNECTOR_ENCRYPTION_KEY=not-a-real-key$' "$envfile" ||
  fail "an unrelated secret line was dropped"
if grep -q "${OLD}" "$envfile"; then
  fail "a superseded digest is still present in the env file"
fi

[ -f "${envfile}.prev-images" ] ||
  fail "no rollback snapshot was written next to the env file"
[ "$(grep -c "${OLD}" "${envfile}.prev-images")" -eq 4 ] ||
  fail "the rollback snapshot does not hold all four superseded digests"

# The snapshot must itself be a valid source, so rollback is one command.
bash "$script" --from "${envfile}.prev-images" --print >/dev/null ||
  fail "the rollback snapshot should be reusable as --from input"

# Idempotent re-run, and the 0600 mode must not widen.
bash "$script" --from "$work/images.env" --env-file "$envfile" >/dev/null ||
  fail "a second identical merge should still succeed"
[ "$(grep -c '^LLM_IMAGE=' "$envfile")" -eq 1 ] ||
  fail "a repeated merge duplicated LLM_IMAGE"
mode="$(stat -c '%a' "$envfile" 2>/dev/null || stat -f '%Lp' "$envfile")"
[ "$mode" = "600" ] ||
  fail "env file mode widened to $mode (expected 600)"

# A missing env file must fail closed rather than create a half-configured one.
if bash "$script" --from "$work/images.env" --env-file "$work/does-not-exist.env" >/dev/null 2>&1; then
  fail "writing to a nonexistent env file should have been REJECTED"
fi
if [ -f "$work/does-not-exist.env" ]; then
  fail "the script created an env file it should have refused to create"
fi

echo "release_images_test: OK — digest-only validation, in-place merge, rollback snapshot, and fail-closed paths verified"
