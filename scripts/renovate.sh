#!/bin/sh
# Scheduled dependency proposals in this GitLab project only.
set -eu
export LANG=C LC_ALL=C LANGUAGE=en
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
image=ghcr.io/renovatebot/renovate:44.258.0
if [ "${1:-}" = validate ]; then
 docker run --rm --network none --read-only --tmpfs /tmp:rw,nosuid,nodev,mode=1777 \
  --mount "type=bind,source=$root/renovate.json,target=/repo/renovate.json,readonly" \
  "$image" renovate-config-validator --strict --no-global /repo/renovate.json
 exit
fi
[ "$#" -eq 0 ] || { echo 'Usage: sh scripts/renovate.sh [validate]' >&2; exit 1; }
[ "${REEF_RENOVATE_ENABLED:-false}" = true ] || { echo 'Renovate is disabled.'; exit 0; }
[ "${CI_PIPELINE_SOURCE:-}" = schedule ] && [ "${CI_COMMIT_REF_PROTECTED:-}" = true ] &&
 [ "${CI_COMMIT_BRANCH:-}" = "${CI_DEFAULT_BRANCH:-master}" ] || { echo 'Protected default-branch schedule required.' >&2; exit 1; }
[ "${CI_PROJECT_PATH:-}" = reef/reef ] && [ "${CI_PROJECT_ID:-}" = 102 ] || { echo 'Renovate is restricted to the Reef source project.' >&2; exit 1; }
[ "${CI_DEBUG_TRACE:-false}" != true ] || { echo 'Refusing to run with CI debug tracing.' >&2; exit 1; }
: "${RENOVATE_TOKEN:?Configure a dedicated protected GitLab project access token}"
: "${GITHUB_COM_TOKEN:?Configure a protected read-only GitHub metadata token for action updates}"
: "${CI_API_V4_URL:?}"
case "$CI_API_V4_URL" in https://*) ;; *) echo 'An HTTPS GitLab endpoint is required.' >&2; exit 1;; esac
mode=${REEF_RENOVATE_DRY_RUN:-full}
case "$mode" in full|extract|lookup|off) ;; *) echo 'Invalid Renovate dry-run mode.' >&2; exit 1;; esac
# The full mode reads proposals but does not create branches, issues or MRs.
# Explicitly select off only after checking the first dry-run results.
export RENOVATE_TOKEN GITHUB_COM_TOKEN
# Pass credential names, never values, in the Docker command line.
# No Docker socket, source runtime, host credentials or project data are mounted.
set -- run --rm --read-only --cap-drop=ALL --security-opt no-new-privileges:true \
 --tmpfs /tmp:rw,nosuid,nodev,mode=1777 \
 --env RENOVATE_TOKEN --env GITHUB_COM_TOKEN \
 --env "RENOVATE_PLATFORM=gitlab" --env "RENOVATE_ENDPOINT=$CI_API_V4_URL" \
 --env RENOVATE_AUTODISCOVER=false --env RENOVATE_ONBOARDING=false \
 --env RENOVATE_REQUIRE_CONFIG=required \
 --env RENOVATE_BASE_DIR=/tmp/renovate --env RENOVATE_CACHE_DIR=/tmp/renovate/cache \
 --env RENOVATE_BINARY_SOURCE=global \
 --env RENOVATE_ALLOW_SCRIPTS=false --env RENOVATE_ALLOW_PLUGINS=false \
 --env 'RENOVATE_ALLOWED_COMMANDS=[]' \
 --env 'RENOVATE_FORCE={"automerge":false,"platformAutomerge":false}' \
 --env LOG_LEVEL=info
if [ "$mode" != off ]; then set -- "$@" --env "RENOVATE_DRY_RUN=$mode"; fi
docker "$@" "$image" reef/reef
