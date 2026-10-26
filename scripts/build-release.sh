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
auth=$(mktemp -d)
export DOCKER_CONFIG=$auth
cleanup(){ rm -rf "$auth"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
sh "$script_dir/ensure-buildx.sh"
printf '%s' "$CI_JOB_TOKEN" | docker login "$CI_REGISTRY" --username gitlab-ci-token --password-stdin
# Use the existing daemon's BuildKit rather than a nested BuildKit/runc runtime.
# Build one platform at a time so classic Docker image stores remain supported.
docker buildx inspect default
for target in runtime agent; do
 image=$CI_REGISTRY_IMAGE
 [ "$target" = runtime ] || image=$image/agent
 for architecture in amd64 arm64; do
  reference=$image:$CI_COMMIT_SHA-$architecture
  docker buildx build --builder default --platform "linux/$architecture" --target "$target" \
   --provenance=false --load --build-arg "APP_REVISION=$CI_COMMIT_SHA" --tag "$reference" .
  docker push "$reference"
 done
done
# Only publish version/SHA/latest indexes once all four platform images exist.
for image in "$CI_REGISTRY_IMAGE" "$CI_REGISTRY_IMAGE/agent"; do
 docker buildx imagetools create --tag "$image:$CI_COMMIT_TAG" --tag "$image:$CI_COMMIT_SHA" --tag "$image:latest" \
  "$image:$CI_COMMIT_SHA-amd64" "$image:$CI_COMMIT_SHA-arm64"
done
