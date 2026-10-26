#!/bin/sh
# Isolated fake Docker/HTTP commands and local Git repositories only.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
work=$(CDPATH= cd -- "$work" && pwd -P)
trap 'rm -rf "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
mkdir "$work/bin" "$work/app"
export PATH="$work/bin:$PATH" TEST_LOG="$work/commands" TEST_HTTP="$work/http"
unset TEST_TRIGGER BAD_HEALTH FAIL_PULL WRONG_IMAGE_REVISION HEALTH_VERSION TEST_REQUIRE_BUILDX_BOOTSTRAP
export EXPECTED_REVISION=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
cat > "$work/bin/docker" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$TEST_LOG"
if [ "${TEST_REQUIRE_BUILDX_BOOTSTRAP:-false}" = true ]; then
 case "$1 ${2:-}" in
  'buildx version') [ -x "$DOCKER_CONFIG/cli-plugins/docker-buildx" ] || exit 1;;
  'cp '*) for argument do destination=$argument;done;printf '#!/bin/sh\nexit 0\n' > "$destination";;
 esac
fi
case " $* " in
 *" login "*) cat >/dev/null;;
 *" image inspect "*) printf '%s\n' "${WRONG_IMAGE_REVISION:-$EXPECTED_REVISION}";;
 *" pull reef "*) [ "${FAIL_PULL:-false}" != true ] || exit 1;;
esac
MOCK
cat > "$work/bin/curl" <<'MOCK'
#!/bin/sh
printf 'request\n' >> "$TEST_HTTP"
output=''
previous=''
for argument do
 if [ "$previous" = -o ];then output=$argument;fi
 previous=$argument
done
if [ -n "${TEST_TRIGGER:-}" ];then printf '201';exit 0;fi
if [ -n "$output" ];then
 if [ "${BAD_HEALTH:-false}" = true ];then printf '{"status":"ok","version":"old","revision":"old"}' > "$output"
 else printf '{"status":"ok","version":"%s","revision":"%s"}' "${HEALTH_VERSION:-0.3.0}" "$EXPECTED_REVISION" > "$output";fi
fi
MOCK
printf '#!/bin/sh\nexit 0\n' > "$work/bin/sleep"
chmod 700 "$work/bin/"*
# Server preparation derives canonical paths, creates private files once and
# does not invoke Docker or replace existing credentials/configuration.
mkdir -p "$work/reef/deploy-agent"
cp "$root/compose.production.yaml" "$work/reef/compose.production.yaml"
sh "$root/scripts/prepare-deployment.sh" "$work/reef" > "$work/preparation"
grep -Fxq "REEF_INSTALLATION_DIR=$work/reef" "$work/reef/.env"
grep -Fxq "REEF_PROJECT_ROOT=$work" "$work/reef/.env"
grep -Eq '^DEPLOY_TOKEN=[0-9a-f]{64}$' "$work/reef/.env"
grep -Eq '^[0-9a-f]{64}$' "$work/reef/secrets/password.txt"
test "$(find "$work/reef/.env" -perm 600 | wc -l | tr -d ' ')" = 1
cp "$work/reef/.env" "$work/env-before"
cp "$work/reef/config.json" "$work/config-before"
cp "$work/reef/secrets/password.txt" "$work/password-before"
sh "$root/scripts/prepare-deployment.sh" "$work/reef" > "$work/preparation-again"
cmp "$work/env-before" "$work/reef/.env"
cmp "$work/config-before" "$work/reef/config.json"
cmp "$work/password-before" "$work/reef/secrets/password.txt"
test ! -e "$TEST_LOG"
mkdir -p "$work/other/reef/deploy-agent" "$work/outside-state"
cp "$root/compose.production.yaml" "$work/other/reef/compose.production.yaml"
ln -s "$work/outside-state" "$work/other/reef/state"
if sh "$root/scripts/prepare-deployment.sh" "$work/other/reef" > "$work/unsafe-preparation" 2>&1;then echo 'Symlinked preparation path accepted';exit 1;fi
test ! -e "$work/other/reef/.env"
export DEPLOY_TOKEN=fixture-token REGISTRY_URL=registry.example.invalid REGISTRY_USER=fixture REGISTRY_PASSWORD=fixture-password
export REEF_IMAGE=registry.example.invalid/reef COMPOSE_PROJECT_NAME=reef-fixture
export APP_ENV_FILE="$work/app/.env" APP_COMPOSE_FILE="$work/app/compose.yaml" DEPLOY_LOCK_DIR="$work/lock"
printf 'services: {}\n' > "$APP_COMPOSE_FILE"
reset_env(){ printf '# keep comment\nAPP_VERSION=v0.2.0\nOTHER_SETTING=preserve-me\n' > "$APP_ENV_FILE";: > "$TEST_LOG";: > "$TEST_HTTP"; }
reset_env
if sh "$root/deploy-agent/deploy.sh" wrong v0.3.0 "$EXPECTED_REVISION" > "$work/rejected" 2>&1;then echo 'Bad token accepted';exit 1;fi
test ! -s "$TEST_LOG"
if sh "$root/deploy-agent/deploy.sh" "$DEPLOY_TOKEN" 'v0.3.0;bad' "$EXPECTED_REVISION" > "$work/rejected" 2>&1;then echo 'Bad version accepted';exit 1;fi
test ! -s "$TEST_LOG"
sh "$root/deploy-agent/deploy.sh" "$DEPLOY_TOKEN" v0.3.0 "$EXPECTED_REVISION" > "$work/success"
grep -q '^APP_VERSION=v0.3.0$' "$APP_ENV_FILE"
grep -q '^OTHER_SETTING=preserve-me$' "$APP_ENV_FILE"
grep -q '^# keep comment$' "$APP_ENV_FILE"
grep -q 'up -d --no-deps --pull never reef$' "$TEST_LOG"
if grep -Eq ' down|prune|up .*deploy-agent' "$TEST_LOG";then echo 'Unrelated service mutation';exit 1;fi
reset_env
if FAIL_PULL=true sh "$root/deploy-agent/deploy.sh" "$DEPLOY_TOKEN" v0.3.0 "$EXPECTED_REVISION" > "$work/rejected" 2>&1;then echo 'Pull failure hidden';exit 1;fi
grep -q '^APP_VERSION=v0.2.0$' "$APP_ENV_FILE"
if grep -q 'up -d' "$TEST_LOG";then echo 'Pull failure still restarted service';exit 1;fi
reset_env
if WRONG_IMAGE_REVISION=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb sh "$root/deploy-agent/deploy.sh" "$DEPLOY_TOKEN" v0.3.0 "$EXPECTED_REVISION" > "$work/rejected" 2>&1;then echo 'Wrong image accepted';exit 1;fi
if grep -q 'up -d' "$TEST_LOG";then echo 'Wrong image restarted service';exit 1;fi
reset_env
if BAD_HEALTH=true sh "$root/deploy-agent/deploy.sh" "$DEPLOY_TOKEN" v0.3.0 "$EXPECTED_REVISION" > "$work/rejected" 2>&1;then echo 'Bad health accepted';exit 1;fi
grep -q '^APP_VERSION=v0.2.0$' "$APP_ENV_FILE"
reset_env
mkdir "$DEPLOY_LOCK_DIR"
if sh "$root/deploy-agent/deploy.sh" "$DEPLOY_TOKEN" v0.3.0 "$EXPECTED_REVISION" > "$work/rejected" 2>&1;then echo 'Concurrent deployment accepted';exit 1;fi
rmdir "$DEPLOY_LOCK_DIR"
test ! -s "$TEST_LOG"
# Check image publication without running Docker or contacting a registry.
export CI_REGISTRY=registry.example.invalid CI_REGISTRY_IMAGE=registry.example.invalid/reef CI_JOB_TOKEN=fixture-job-token
mkdir -p "$work/build-project/scripts"
cp "$root/scripts/ensure-buildx.sh" "$root/scripts/build-release.sh" "$root/scripts/deploy-release.sh" "$root/scripts/read-version.sh" "$work/build-project/scripts/"
printf '0.3.0\n' > "$work/build-project/VERSION"
export CI_COMMIT_TAG=v0.3.0 CI_COMMIT_SHA="$EXPECTED_REVISION" CI_COMMIT_REF_PROTECTED=true
(cd "$work/build-project";sh scripts/build-release.sh)
test "$(grep -c 'buildx build .*--platform linux/amd64,linux/arm64' "$TEST_LOG")" -eq 2
# Reproduce a runner that has Docker but no Buildx in its isolated config.
: > "$TEST_LOG"
(cd "$work/build-project";TEST_REQUIRE_BUILDX_BOOTSTRAP=true sh scripts/build-release.sh)
grep -q 'create --name reef-buildx-tool-' "$TEST_LOG"
grep -q 'cp .*docker-buildx' "$TEST_LOG"
test "$(grep -c 'buildx build .*--platform linux/amd64,linux/arm64' "$TEST_LOG")" -eq 2
grep -q 'rm -f reef-buildx-tool-' "$TEST_LOG"
export DEPLOY_WEBHOOK_URL=https://deploy.example.invalid/hooks/deploy DEPLOY_WEBHOOK_TOKEN=fixture-webhook HEALTHCHECK_URL=https://reef.example.invalid/healthz
(cd "$work/build-project";sh scripts/deploy-release.sh) > "$work/ci-deploy"
if (cd "$work/build-project";BAD_HEALTH=true sh scripts/deploy-release.sh) > "$work/rejected" 2>&1;then echo 'CI accepted wrong live build';exit 1;fi
# Annotated release tags are created once and never moved to a later commit.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME='Fixture' GIT_COMMITTER_NAME='Fixture' GIT_AUTHOR_EMAIL=test@example.invalid GIT_COMMITTER_EMAIL=test@example.invalid
unset GIT_DIR GIT_WORK_TREE
git init -q --bare --initial-branch=master "$work/origin.git"
git init -q --initial-branch=master "$work/source"
mkdir "$work/source/scripts"
cp "$root/scripts/create-release-tag.sh" "$root/scripts/read-version.sh" "$work/source/scripts/"
printf '0.3.0\n' > "$work/source/VERSION"
git -C "$work/source" add .
git -C "$work/source" -c commit.gpgsign=false commit -q -m 'Synthetic release'
git -C "$work/source" push -q "$work/origin.git" master
export CI_REPOSITORY_URL="$work/origin.git" CI_API_V4_URL=https://gitlab.example.invalid/api/v4 CI_PROJECT_ID=1 CI_COMMIT_BRANCH=master CI_DEFAULT_BRANCH=master TEST_TRIGGER=true
CI_COMMIT_SHA=$(git -C "$work/source" rev-parse HEAD);export CI_COMMIT_SHA
(cd "$work/source";sh scripts/create-release-tag.sh) > "$work/release-log" 2>&1
tag_commit=$(git -C "$work/origin.git" rev-parse 'v0.3.0^{}')
test "$tag_commit" = "$CI_COMMIT_SHA"
test "$(git -C "$work/origin.git" cat-file -t v0.3.0)" = tag
printf 'next\n' > "$work/source/next"
git -C "$work/source" add next
git -C "$work/source" -c commit.gpgsign=false commit -q -m 'Next source revision'
git -C "$work/source" push -q "$work/origin.git" master
CI_COMMIT_SHA=$(git -C "$work/source" rev-parse HEAD);export CI_COMMIT_SHA
(cd "$work/source";sh scripts/create-release-tag.sh) > "$work/existing-tag" 2>&1
test "$(git -C "$work/origin.git" rev-parse 'v0.3.0^{}')" = "$tag_commit"
printf '01.3.0\n' > "$work/bad-version"
if sh "$root/scripts/read-version.sh" "$work/bad-version" > "$work/rejected" 2>&1;then echo 'Invalid release accepted';exit 1;fi
echo 'PASS: idempotent private server preparation, release immutability, multiarch build, token/version/revision checks, serialized deployment, preservation after pull/health failures, and exact live build verification.'
