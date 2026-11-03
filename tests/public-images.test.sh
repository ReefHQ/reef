#!/bin/sh
# Synthetic public Git history and fake Docker only; no registry or real token.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
work=$(CDPATH= cd -- "$work" && pwd -P)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/source/scripts" "$work/source/.github" "$work/apps/reef"
cp "$root/scripts/publish-containers.sh" "$root/scripts/read-version.sh" "$work/source/scripts/"
printf '0.32.0\n' > "$work/source/VERSION"
revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
printf '%s\n' "$revision" > "$work/source/.github/source-revision"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=Fixture GIT_COMMITTER_NAME=Fixture GIT_AUTHOR_EMAIL=test@example.invalid GIT_COMMITTER_EMAIL=test@example.invalid
unset GIT_DIR GIT_WORK_TREE
git init -q --initial-branch=master "$work/source"
git -C "$work/source" add .
git -C "$work/source" -c commit.gpgsign=false commit -qm 'Synthetic public release'
git -C "$work/source" -c tag.gpgsign=false tag v0.32.0
public_revision=$(git -C "$work/source" rev-parse HEAD)
cat > "$work/bin/docker" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$PUBLIC_TEST_LOG"
case "$1" in
 login) cat > /dev/null;;
 buildx) case "$*" in *'--platform linux/arm64 '*) [ "${PUBLIC_FAIL_BUILD:-false}" != true ] || exit 1;; esac;;
 push) case "$2" in *reef-agent:*arm64) [ "${PUBLIC_FAIL_PUSH:-false}" != true ] || exit 1;; esac;;
esac
MOCK
chmod 700 "$work/bin/docker"
export PATH="$work/bin:$PATH" PUBLIC_TEST_LOG="$work/commands"
export GITHUB_REPOSITORY=ReefHQ/reef GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.32.0 GITHUB_ACTOR=fixture REEF_REGISTRY_TOKEN=fixture-only-token
unset PUBLIC_FAIL_BUILD PUBLIC_FAIL_PUSH
: > "$PUBLIC_TEST_LOG"
(cd "$work/source"; sh scripts/publish-containers.sh)
test "$(grep -c '^buildx build .*--platform linux/' "$PUBLIC_TEST_LOG")" -eq 4
test "$(grep -c '^push ghcr.io/reefhq/' "$PUBLIC_TEST_LOG")" -eq 4
test "$(grep -c '^buildx imagetools create' "$PUBLIC_TEST_LOG")" -eq 2
test "$(grep -c -- "--build-arg APP_REVISION=$revision" "$PUBLIC_TEST_LOG")" -eq 4
test "$(grep -c -- "--label io.reef.public-revision=$public_revision" "$PUBLIC_TEST_LOG")" -eq 4
if grep -Eq 'registry.gitlab|compose|prune|fixture-only-token' "$PUBLIC_TEST_LOG"; then echo 'Unexpected registry access, secret or service mutation'; exit 1; fi
for image in reef reef-agent; do
 grep -Fq -- "--tag ghcr.io/reefhq/$image:v0.32.0 --tag ghcr.io/reefhq/$image:0.32.0 --tag ghcr.io/reefhq/$image:$revision --tag ghcr.io/reefhq/$image:latest ghcr.io/reefhq/$image:$revision-amd64 ghcr.io/reefhq/$image:$revision-arm64" "$PUBLIC_TEST_LOG"
done
for failure in build push; do
 : > "$PUBLIC_TEST_LOG"
 case "$failure" in build) export PUBLIC_FAIL_BUILD=true;; push) export PUBLIC_FAIL_PUSH=true;; esac
 if (cd "$work/source"; sh scripts/publish-containers.sh) > "$work/failure" 2>&1; then echo 'Failed platform release accepted'; exit 1; fi
 if grep -q '^buildx imagetools create' "$PUBLIC_TEST_LOG"; then echo 'Incomplete public release exposed'; exit 1; fi
 unset PUBLIC_FAIL_BUILD PUBLIC_FAIL_PUSH
done
for failure in fork branch mismatch missing-token marker checkout; do
 : > "$PUBLIC_TEST_LOG"
 case "$failure" in
  fork) export GITHUB_REPOSITORY=Other/reef;;
  branch) export GITHUB_REF_TYPE=branch;;
  mismatch) export GITHUB_REF_NAME=v0.31.0;;
  missing-token) unset REEF_REGISTRY_TOKEN;;
  marker) printf 'generated-by-reef-mirror\n' > "$work/source/.github/source-revision";;
  checkout) git -C "$work/source" -c commit.gpgsign=false commit --allow-empty -qm 'Wrong checkout';;
 esac
 if (cd "$work/source"; sh scripts/publish-containers.sh) > "$work/failure" 2>&1; then echo 'Invalid public release admitted'; exit 1; fi
 test ! -s "$PUBLIC_TEST_LOG"
 export GITHUB_REPOSITORY=ReefHQ/reef GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v0.32.0 REEF_REGISTRY_TOKEN=fixture-only-token
 printf '%s\n' "$revision" > "$work/source/.github/source-revision"
 # Only synthetic repository changes are restored, never the source checkout.
 git -C "$work/source" checkout -q v0.32.0
done
# Retrying an old version cannot replace latest after a newer stable tag exists.
git -C "$work/source" -c tag.gpgsign=false tag v0.33.0
: > "$PUBLIC_TEST_LOG"
(cd "$work/source"; sh scripts/publish-containers.sh)
if grep -q ':latest' "$PUBLIC_TEST_LOG"; then echo 'Old retry moved latest backward'; exit 1; fi
test "$(grep -c '^buildx imagetools create' "$PUBLIC_TEST_LOG")" -eq 2
: > "$PUBLIC_TEST_LOG"
cp "$root/examples/public/compose.yaml" "$work/apps/reef/compose.yaml"
(cd "$work/apps/reef"; sh "$root/scripts/prepare-installation.sh" v0.32.0 --lan)
grep -Fxq 'REEF_BIND_ADDRESS=0.0.0.0' "$work/apps/reef/.env"
grep -Fxq "REEF_PROJECT_ROOT=$work/apps" "$work/apps/reef/.env"
grep -Eq '^[0-9a-f]{64}$' "$work/apps/reef/secrets/password.txt"
for path in .env config.json secrets/password.txt; do
 cp "$work/apps/reef/$path" "$work/$(basename "$path").before"
 test "$(find "$work/apps/reef/$path" -perm 600 | wc -l | tr -d ' ')" -eq 1
done
(cd "$work/apps/reef"; sh "$root/scripts/prepare-installation.sh" v0.33.0)
for path in .env config.json secrets/password.txt; do cmp "$work/apps/reef/$path" "$work/$(basename "$path").before"; done
test ! -s "$PUBLIC_TEST_LOG"
mkdir -p "$work/local/reef" "$work/unsafe/reef"
cp "$root/examples/public/compose.yaml" "$work/local/reef/compose.yaml"
cp "$root/examples/public/compose.yaml" "$work/unsafe/reef/compose.yaml"
(cd "$work/local/reef"; sh "$root/scripts/prepare-installation.sh" v0.32.0)
grep -Fxq 'REEF_BIND_ADDRESS=127.0.0.1' "$work/local/reef/.env"
ln -s "$work/apps/reef/state" "$work/unsafe/reef/state"
if (cd "$work/unsafe/reef"; sh "$root/scripts/prepare-installation.sh" v0.32.0); then echo 'Symlinked state accepted'; exit 1; fi
test ! -e "$work/unsafe/reef/secrets/password.txt"
# Fresh public agents need no login; an existing GitLab installation keeps its registry.
mkdir -p "$work/agent/reef-agent"
printf 'COMPOSE_PROJECT_NAME=reef-agent\nREEF_SERVER=https://reef.example.invalid\n' > "$work/agent/reef-agent/.env"
cp "$root/examples/agent/compose.updates.yaml" "$work/agent/reef-agent/compose.updates.yaml"
(cd "$work/agent/reef-agent"; DOCKER_CONFIG="$work/empty-auth" sh "$root/scripts/prepare-agent.sh" v0.32.0 --enable-updates)
grep -Fxq 'REEF_AGENT_IMAGE=ghcr.io/reefhq/reef-agent:v0.32.0' "$work/agent/reef-agent/.env"
grep -Fxq 'REEF_AGENT_UPDATER_IMAGE=ghcr.io/reefhq/reef-agent:v0.32.0' "$work/agent/reef-agent/.env"
test -d "$work/empty-auth"
echo 'PASS: GitHub tag publication, original revision compatibility, fail-closed platform aliases, latest retry guard, private public installation and agent preparation.'
