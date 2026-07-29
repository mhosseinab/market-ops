#!/usr/bin/env bash
# tools/deploy/release_images.sh — put the release pipeline's digest-pinned image
# references into the production environment file.
#
# `.github/workflows/release.yml` is the ONLY producer of production images. On a
# strict-semver `v*` tag reachable from origin/main it builds core/llm/nginx/goose
# for amd64+arm64, blocks on HIGH/CRITICAL Trivy findings, pushes immutable tags,
# and uploads an `image-digests-<version>` artifact containing one file:
#
#     CORE_IMAGE=ghcr.io/<owner>/market-ops-core@sha256:...
#     LLM_IMAGE=ghcr.io/<owner>/market-ops-llm@sha256:...
#     NGINX_IMAGE=ghcr.io/<owner>/market-ops-nginx@sha256:...
#     GOOSE_IMAGE=ghcr.io/<owner>/market-ops-goose@sha256:...
#
# Those four lines are exactly what `deploy/compose.prod.yml` requires. This
# script moves them into `$ENVFILE` without a copy-paste step, and refuses
# anything that is not a `@sha256:` digest — a mutable tag is not a deployable
# reference (DEPLOYMENT.md §6.1, §10 step 2).
#
# Sources (pick one):
#   --tag vX.Y.Z      download the artifact from the successful release run for
#                     that tag with `gh` (needs the GitHub CLI, authenticated)
#   --from FILE       use an images.env already downloaded from the Actions UI
#                     (no GitHub CLI needed — the air-gapped/manual path)
#   --resolve vX.Y.Z  ask GHCR to resolve the published tag to its index digest
#                     with `docker buildx imagetools` (no GitHub CLI needed;
#                     requires registry read access from this host)
#
# Actions:
#   (default)         merge the four lines into --env-file, keeping every other
#                     line untouched, and save the values being replaced to
#                     <env-file>.prev-images so a rollback is one command
#   --print           write the four lines to stdout and change nothing
#   --check           re-read --env-file and prove every pinned digest still
#                     exists in the registry (run before `task prod:up`)
#
# Examples:
#   tools/deploy/release_images.sh --tag v0.1.0 --env-file /etc/market-ops/prod.env
#   tools/deploy/release_images.sh --from ~/Downloads/images.env --env-file "$ENVFILE"
#   tools/deploy/release_images.sh --check --env-file "$ENVFILE"
#   tools/deploy/release_images.sh --from "$ENVFILE.prev-images" --env-file "$ENVFILE"
#
# Exit status is 0 only when every one of the four references is present, well
# formed, and (for --check) resolvable. There is no partial success: a half
# written env file would deploy a mixed release.
set -euo pipefail

readonly VARS=(CORE_IMAGE LLM_IMAGE NGINX_IMAGE GOOSE_IMAGE)
readonly COMPONENTS=(core llm nginx goose)
# Owner of both the GitHub repository and the GHCR namespace. Override for a
# fork with MARKET_OPS_REPO=<owner>/<repo>.
readonly DEFAULT_REPO="mhosseinab/market-ops"
readonly REGISTRY="ghcr.io"

usage() {
  cat <<'USAGE'
Put the release pipeline's digest-pinned image references into the production
environment file consumed by deploy/compose.prod.yml.

Sources (pick exactly one):
  --tag vX.Y.Z       download the image-digests artifact from the successful
                     "container release" run for that tag (needs `gh`)
  --from FILE        read an images.env downloaded by hand from the Actions UI
  --resolve vX.Y.Z   resolve the published tag to its index digest via
                     `docker buildx imagetools` (needs registry read access)

Actions:
  (default)          merge CORE_IMAGE/LLM_IMAGE/NGINX_IMAGE/GOOSE_IMAGE into
                     --env-file, saving the replaced values to
                     <env-file>.prev-images for rollback
  --print            print the four lines, write nothing
  --check            verify the digests already in --env-file still exist

Options:
  --env-file PATH    production env file (default: $ENVFILE)
  --repo OWNER/NAME  source repository and GHCR namespace
                     (default: mhosseinab/market-ops, or $MARKET_OPS_REPO)

Every reference must be `ghcr.io/<owner>/market-ops-<component>@sha256:<64 hex>`.
Mutable tags are rejected: compose.prod.yml deploys by digest only.
USAGE
}

die() {
  echo "release_images: $1" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 ||
    die "$1 is required for this mode but is not installed.${2:+ $2}"
}

mode=""
source_tag=""
source_file=""
env_file="${ENVFILE:-}"
action="write"
repo="${MARKET_OPS_REPO:-$DEFAULT_REPO}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag) [ "$#" -ge 2 ] || die "--tag needs a value"; mode="tag"; source_tag="$2"; shift 2 ;;
    --from) [ "$#" -ge 2 ] || die "--from needs a value"; mode="file"; source_file="$2"; shift 2 ;;
    --resolve) [ "$#" -ge 2 ] || die "--resolve needs a value"; mode="resolve"; source_tag="$2"; shift 2 ;;
    --env-file) [ "$#" -ge 2 ] || die "--env-file needs a value"; env_file="$2"; shift 2 ;;
    --repo) [ "$#" -ge 2 ] || die "--repo needs a value"; repo="$2"; shift 2 ;;
    --print) action="print"; shift ;;
    --check) action="check"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument '$1' (see --help)" ;;
  esac
done

owner="${repo%%/*}"
if [ -z "$owner" ] || [ "$owner" = "$repo" ]; then
  die "--repo must be OWNER/NAME, got '$repo'"
fi

# ── validation ───────────────────────────────────────────────────────────────
# One shape is accepted: the exact GHCR repository for the component, pinned by a
# full 64-hex sha256 digest. Tags are mutable and are rejected on purpose, as is
# any image from another namespace.
assert_digest_ref() {
  local var="$1" component="$2" value="$3"
  local expected="${REGISTRY}/${owner}/market-ops-${component}"
  [ -n "$value" ] || die "$var is empty."
  case "$value" in
    *@sha256:*) : ;;
    *) die "$var is not digest-pinned: '$value'. compose.prod.yml deploys by digest only — a tag can be moved after it was scanned." ;;
  esac
  local ref_repo="${value%@*}" ref_digest="${value#*@}"
  [ "$ref_repo" = "$expected" ] ||
    die "$var points at '$ref_repo', expected '$expected'."
  printf '%s' "$ref_digest" | grep -Eq '^sha256:[0-9a-f]{64}$' ||
    die "$var has a malformed digest: '$ref_digest' (expected sha256: + 64 hex)."
}

# ── source: a local images.env (or any file with the four assignments) ───────
read_images_file() {
  local file="$1" i var component line value
  [ -f "$file" ] || die "no such file: $file"
  for i in "${!VARS[@]}"; do
    var="${VARS[$i]}"
    component="${COMPONENTS[$i]}"
    # Last assignment wins, matching how an env file is read.
    line="$(grep -E "^[[:space:]]*(export[[:space:]]+)?${var}=" "$file" | tail -n 1 || true)"
    [ -n "$line" ] || die "$file has no ${var} line. Is this the image-digests artifact from a PUBLISHING release run? A pull-request run builds and scans but publishes nothing, so it uploads no digests."
    value="${line#*=}"
    # Strip surrounding quotes and trailing whitespace/CR.
    value="${value%$'\r'}"
    value="$(printf '%s' "$value" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/")"
    assert_digest_ref "$var" "$component" "$value"
    RESOLVED[$var]="$value"
  done
}

# ── source: the GitHub Actions artifact for a released tag ───────────────────
download_artifact() {
  local tag="$1" tmp run_id
  need gh "Use --resolve ${tag} (registry lookup) or --from <images.env> (manual download) instead."
  printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
    die "'$tag' is not a strict-semver release tag; release.yml only publishes for vMAJOR.MINOR.PATCH[-prerelease]."
  run_id="$(gh run list --repo "$repo" --workflow release.yml --branch "$tag" \
    --json databaseId,conclusion --jq 'map(select(.conclusion == "success")) | first | .databaseId' 2>/dev/null || true)"
  if [ -z "$run_id" ] || [ "$run_id" = "null" ]; then
    die "no successful 'container release' run found for tag $tag in $repo. Push the tag and let the run finish before deploying it."
  fi
  tmp="$(mktemp -d)"
  # shellcheck disable=SC2064  # expand tmp now, not at trap time
  trap "rm -rf '$tmp'" EXIT
  gh run download "$run_id" --repo "$repo" --name "image-digests-${tag}" --dir "$tmp" >/dev/null ||
    die "could not download artifact image-digests-${tag} from run $run_id. Artifacts expire after 90 days; use --resolve $tag to read the digests back from GHCR instead."
  read_images_file "$tmp/images.env"
}

# ── source: resolve the published tag straight from the registry ─────────────
resolve_from_registry() {
  local tag="$1" i var component ref digest
  need docker "The registry lookup needs 'docker buildx imagetools'."
  for i in "${!VARS[@]}"; do
    var="${VARS[$i]}"
    component="${COMPONENTS[$i]}"
    ref="${REGISTRY}/${owner}/market-ops-${component}:${tag}"
    digest="$(docker buildx imagetools inspect "$ref" --format '{{.Manifest.Digest}}' 2>/dev/null || true)"
    if [ -z "$digest" ]; then
      # Older buildx without the --format template: fall back to the plain
      # inspect output, whose first Digest: line is the index digest.
      digest="$(docker buildx imagetools inspect "$ref" 2>/dev/null |
        sed -n 's/^Digest:[[:space:]]*//p' | head -n 1 || true)"
    fi
    [ -n "$digest" ] ||
      die "could not resolve $ref. Log in first (docker login ${REGISTRY}) and confirm the release run published this tag."
    RESOLVED[$var]="${REGISTRY}/${owner}/market-ops-${component}@${digest}"
    assert_digest_ref "$var" "$component" "${RESOLVED[$var]}"
  done
}

# ── action: prove every pinned digest still exists ───────────────────────────
check_env_file() {
  local file="$1" i var
  read_images_file "$file"
  need docker "The existence check needs 'docker buildx imagetools'."
  for i in "${!VARS[@]}"; do
    var="${VARS[$i]}"
    docker buildx imagetools inspect "${RESOLVED[$var]}" >/dev/null 2>&1 ||
      die "${var} is not present in the registry: ${RESOLVED[$var]}. Do not start the stack with an unresolvable digest."
    echo "  ${var} resolved"
  done
  echo "release_images: all four digests in $file exist in ${REGISTRY}"
}

# ── action: merge the four lines into the env file ───────────────────────────
write_env_file() {
  local file="$1" tmp prev i var mode_bits
  [ -f "$file" ] ||
    die "env file '$file' does not exist. Create it first: cp deploy/.env.prod.example \"$file\" && chmod 600 \"$file\" (keep it OUTSIDE the checkout)."

  prev="${file}.prev-images"
  # Preserve the outgoing digests before overwriting them — this file is the
  # rollback input (DEPLOYMENT.md §11): --from "$ENVFILE.prev-images".
  if grep -Eq '^[[:space:]]*(export[[:space:]]+)?(CORE|LLM|NGINX|GOOSE)_IMAGE=' "$file"; then
    if ! grep -Eq 'sha256:[0-9a-f]{64}' "$file"; then
      echo "release_images: current $file has no digest-pinned images yet; not writing a rollback snapshot." >&2
    else
      ( umask 077; grep -E '^[[:space:]]*(export[[:space:]]+)?(CORE|LLM|NGINX|GOOSE)_IMAGE=' "$file" >"$prev" )
      echo "release_images: previous digests saved to $prev"
    fi
  fi

  tmp="$(mktemp)"
  # Copy the file through, dropping the old *_IMAGE assignments; everything else
  # (secrets, comments, ordering) is preserved byte for byte.
  grep -Ev '^[[:space:]]*(export[[:space:]]+)?(CORE|LLM|NGINX|GOOSE)_IMAGE=' "$file" >"$tmp" || true
  {
    echo ""
    echo "# Digest-pinned images written by tools/deploy/release_images.sh"
    for i in "${!VARS[@]}"; do
      var="${VARS[$i]}"
      echo "${var}=${RESOLVED[$var]}"
    done
  } >>"$tmp"

  # Keep the destination's permissions; never widen them.
  mode_bits="$(stat -c '%a' "$file" 2>/dev/null || stat -f '%Lp' "$file")"
  chmod "$mode_bits" "$tmp"
  mv "$tmp" "$file"
  echo "release_images: wrote 4 digest-pinned image references to $file"
}

# ── main ─────────────────────────────────────────────────────────────────────
declare -A RESOLVED=()

if [ "$action" = "check" ]; then
  [ -z "$mode" ] || die "--check reads the env file; do not combine it with --tag/--from/--resolve."
  [ -n "$env_file" ] || die "--check needs --env-file PATH (or ENVFILE in the environment)."
  check_env_file "$env_file"
  exit 0
fi

case "$mode" in
  tag) download_artifact "$source_tag" ;;
  file) read_images_file "$source_file" ;;
  resolve) resolve_from_registry "$source_tag" ;;
  "") usage >&2; die "pick a source: --tag vX.Y.Z, --from FILE, or --resolve vX.Y.Z." ;;
esac

if [ "$action" = "print" ]; then
  for var in "${VARS[@]}"; do
    echo "${var}=${RESOLVED[$var]}"
  done
  exit 0
fi

[ -n "$env_file" ] || die "--env-file PATH is required (or set ENVFILE). Use --print to inspect the values without writing them."
write_env_file "$env_file"
for var in "${VARS[@]}"; do
  echo "  ${var}=${RESOLVED[$var]}"
done
echo "release_images: record these digests in the release handoff (DEPLOYMENT.md §12)."
