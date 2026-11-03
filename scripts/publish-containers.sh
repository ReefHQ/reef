#!/bin/sh
# Run only against a version tag in the public GitHub mirror.
set -eu
export LANG=C LC_ALL=C LANGUAGE=en
[ "${GITHUB_REPOSITORY:-}" = ReefHQ/reef ] && [ "${GITHUB_REF_TYPE:-}" = tag ] || { echo 'Public Reef tag required.' >&2; exit 1; }
: "${REEF_REGISTRY_TOKEN:?GitHub Actions package token required}"
: "${GITHUB_ACTOR:?}"
version=$(sh scripts/read-version.sh)
tag=v$version
[ "${GITHUB_REF_NAME:-}" = "$tag" ] || { echo 'Release tag and VERSION differ.' >&2; exit 1; }
printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || { echo 'Stable release required.' >&2; exit 1; }
[ -f .github/source-revision ] && [ ! -L .github/source-revision ] || { echo 'Mirror source revision required.' >&2; exit 1; }
revision=$(cat .github/source-revision)
case "$revision" in ''|*[!0-9a-f]*) echo 'Invalid mirror source revision.' >&2; exit 1;; esac
[ "${#revision}" -eq 40 ] || { echo 'Expected a full original source revision.' >&2; exit 1; }
public_revision=$(git rev-parse HEAD)
[ "$public_revision" = "$(git rev-list -n 1 "$tag")" ] || { echo 'Checkout and release tag differ.' >&2; exit 1; }
auth=$(mktemp -d)
export DOCKER_CONFIG=$auth
trap 'rm -rf "$auth"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
docker buildx version
printf '%s' "$REEF_REGISTRY_TOKEN" | docker login ghcr.io --username "$GITHUB_ACTOR" --password-stdin
# Cross-compilation needs no emulated RUN steps: only the Go build stage executes
# on the runner platform; single-platform loads also support classic image stores.
docker buildx inspect default
for target in runtime agent; do
 image=ghcr.io/reefhq/reef
 [ "$target" = runtime ] || image=ghcr.io/reefhq/reef-agent
 for architecture in amd64 arm64; do
  reference=$image:$revision-$architecture
  docker buildx build --builder default --platform "linux/$architecture" --target "$target" \
   --provenance=false --load --build-arg "APP_VERSION=$version" --build-arg "APP_REVISION=$revision" \
   --label "io.reef.public-revision=$public_revision" --tag "$reference" .
  docker push "$reference"
 done
done
# A retry of an older tag may repair its version aliases, but never move latest
# backward. The serialized job checks all tags fetched by checkout.
newest=$(git tag --list 'v*' --sort=-version:refname | grep -E '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' | awk 'NR==1{print}')
for image in ghcr.io/reefhq/reef ghcr.io/reefhq/reef-agent; do
 set -- --tag "$image:$tag" --tag "$image:$version" --tag "$image:$revision"
 [ "$newest" != "$tag" ] || set -- "$@" --tag "$image:latest"
 docker buildx imagetools create "$@" "$image:$revision-amd64" "$image:$revision-arm64"
done
echo 'GHCR release published. Verify both package visibilities before announcing public installation.'
