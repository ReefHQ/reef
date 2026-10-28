#!/bin/sh
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
TOKEN=${1:-}
VERSION=${2:-}
REVISION=${3:-}
ENV_FILE=${APP_ENV_FILE:-/app/.env}
COMPOSE_FILE=${APP_COMPOSE_FILE:-/app/compose.production.yaml}
HEALTHCHECK_URL=${DEPLOY_HEALTHCHECK_URL:-http://reef:8099/healthz}
if [ -z "${DEPLOY_TOKEN:-}" ] || [ "$TOKEN" != "$DEPLOY_TOKEN" ]; then echo 'Unauthorized deployment.' >&2;exit 1;fi
if ! printf '%s\n' "$VERSION" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then echo 'Stable SemVer tag required.' >&2;exit 1;fi
case "$REVISION" in ''|*[!0-9a-f]*) echo 'Invalid revision.' >&2;exit 1;; esac
test "${#REVISION}" -eq 40 || { echo 'Full Git revision required.' >&2;exit 1; }
: "${REGISTRY_URL:?}" "${REGISTRY_USER:?}" "${REGISTRY_PASSWORD:?}" "${REEF_IMAGE:?}" "${COMPOSE_PROJECT_NAME:?}"
if [ ! -f "$ENV_FILE" ] || [ -L "$ENV_FILE" ] || [ ! -w "$ENV_FILE" ] || [ ! -w "$(dirname "$ENV_FILE")" ]; then echo 'Deployment .env and its directory must be writable regular paths.' >&2;exit 1;fi
[ -f "$COMPOSE_FILE" ] || { echo 'Deployment Compose file is unavailable.' >&2;exit 1; }
umask 077
lock=${DEPLOY_LOCK_DIR:-/tmp/reef-deploy.lock}
if ! mkdir "$lock" 2>/dev/null; then echo 'Another deployment is running; inspect before retrying.' >&2;exit 1;fi
work=
cleanup(){ [ -z "$work" ] || rm -rf "$work";rmdir "$lock"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
work=$(mktemp -d)
export APP_VERSION=$VERSION DOCKER_CONFIG=$work/auth
mkdir "$DOCKER_CONFIG"
printf '%s' "$REGISTRY_PASSWORD" | docker login "$REGISTRY_URL" --username "$REGISTRY_USER" --password-stdin
compose(){ docker compose --env-file "$ENV_FILE" --file "$COMPOSE_FILE" --project-name "$COMPOSE_PROJECT_NAME" "$@"; }
compose config --quiet
compose pull reef
actual=$(docker image inspect "$REEF_IMAGE:$VERSION" --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}')
if [ "$actual" != "$REVISION" ]; then echo 'Pulled image revision differs from the requested release; current service was not stopped.' >&2;exit 1;fi
compose up -d --no-deps --pull never reef
healthy=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
 if curl --fail --silent --show-error --connect-timeout 5 --max-time 10 "$HEALTHCHECK_URL" -o "$work/health.json" &&
  grep -Fq '"status":"ok"' "$work/health.json" && grep -Fq "\"version\":\"${VERSION#v}\"" "$work/health.json" &&
  grep -Fq "\"revision\":\"$REVISION\"" "$work/health.json"; then healthy=true;break;fi
 sleep 5
done
if [ "$healthy" != true ]; then echo 'Health check failed; saved APP_VERSION remains unchanged. Inspect the running container before retrying.' >&2;exit 1;fi
# Preserve all other settings and permissions; replace .env only after success.
temporary_env=$(mktemp "$(dirname "$ENV_FILE")/.reef-env.XXXXXX")
trap 'rm -f "$temporary_env";cleanup' EXIT
cp -p "$ENV_FILE" "$temporary_env"
awk -v version="$VERSION" '
 BEGIN {updated=0}
 /^[[:space:]]*APP_VERSION[[:space:]]*=/ {if(!updated){print "APP_VERSION=" version;updated=1};next}
 {print}
 END {if(!updated) print "APP_VERSION=" version}
' "$ENV_FILE" > "$work/env-content"
cat "$work/env-content" > "$temporary_env"
mv "$temporary_env" "$ENV_FILE"
printf 'Reef %s (%s) is healthy; APP_VERSION persisted.\n' "$VERSION" "$REVISION"
