#!/bin/sh
# Supply the CLI plugin in this job's temporary Docker config, not on the runner.
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
: "${DOCKER_CONFIG:?Use an isolated job Docker configuration}"
if docker buildx version >/dev/null 2>&1; then exit 0;fi
case "$(uname -m)" in
 x86_64|amd64) architecture=amd64;;
 aarch64|arm64) architecture=arm64;;
 *) echo 'Unsupported runner architecture for Buildx.' >&2;exit 1;;
esac
helper="reef-buildx-tool-${CI_JOB_ID:-$$}"
cleanup(){ docker rm -f "$helper" >/dev/null 2>&1 || :; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
mkdir -p "$DOCKER_CONFIG/cli-plugins"
# Copy a static Linux plugin matching the runner CPU. The tool container never
# starts, receives no credentials and mounts no host files or Docker socket.
docker create --name "$helper" --platform "linux/$architecture" --entrypoint /bin/true docker:29.9.2-cli >/dev/null
docker cp "$helper:/usr/local/libexec/docker/cli-plugins/docker-buildx" "$DOCKER_CONFIG/cli-plugins/docker-buildx"
chmod 700 "$DOCKER_CONFIG/cli-plugins/docker-buildx"
docker buildx version
