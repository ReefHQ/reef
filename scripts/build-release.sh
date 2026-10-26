#!/bin/sh
set -eu
: "${CI_REGISTRY:?}"
: "${CI_REGISTRY_IMAGE:?}"
: "${CI_JOB_TOKEN:?}"
: "${CI_COMMIT_SHA:?}"
version=$(sh scripts/read-version.sh)
test "${CI_COMMIT_TAG:-}" = "v$version" || { echo 'Release tag and VERSION differ.' >&2;exit 1; }
test "${CI_COMMIT_REF_PROTECTED:-}" = true || { echo 'Protected tag required.' >&2;exit 1; }
case "$CI_COMMIT_SHA" in ''|*[!0-9a-f]*) echo 'Invalid revision.' >&2;exit 1;; esac
test "${#CI_COMMIT_SHA}" -eq 40 || { echo 'Expected a full Git revision.' >&2;exit 1; }
builder="reef-release-${CI_JOB_ID:-$$}"
auth=$(mktemp -d)
export DOCKER_CONFIG=$auth
cleanup(){ docker buildx rm "$builder" >/dev/null 2>&1 || :;rm -rf "$auth"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
printf '%s' "$CI_JOB_TOKEN" | docker login "$CI_REGISTRY" --username gitlab-ci-token --password-stdin
docker buildx create --name "$builder" --driver docker-container --use
docker buildx inspect --bootstrap "$builder"
# Go cross-compiles on BUILDPLATFORM; runtime stages execute no foreign binaries.
for target in runtime agent; do
 image=$CI_REGISTRY_IMAGE
 [ "$target" = runtime ] || image=$image/agent
 docker buildx build --builder "$builder" --platform linux/amd64,linux/arm64 --target "$target" \
  --build-arg "APP_REVISION=$CI_COMMIT_SHA" --tag "$image:$CI_COMMIT_TAG" --tag "$image:$CI_COMMIT_SHA" --push .
done
# Deployments always use the immutable version tag; latest is updated only for stable releases.
for image in "$CI_REGISTRY_IMAGE" "$CI_REGISTRY_IMAGE/agent"; do
 docker buildx imagetools create --tag "$image:latest" "$image:$CI_COMMIT_TAG"
done
