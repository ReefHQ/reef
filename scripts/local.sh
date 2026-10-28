#!/bin/sh
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
umask 077
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) target_os=darwin; target_arch=arm64 ;;
  Darwin/x86_64) target_os=darwin; target_arch=amd64 ;;
  Linux/aarch64|Linux/arm64) target_os=linux; target_arch=arm64 ;;
  Linux/x86_64) target_os=linux; target_arch=amd64 ;;
  *) printf '%s\n' 'Unsupported local launcher platform.' >&2; exit 1 ;;
esac
if [ "${1:-up}" = password ] && [ "$#" -eq 1 ] && [ -f "$root/.reef-local/password.txt" ]; then cat "$root/.reef-local/password.txt"; exit 0; fi
tools="$root/.reef-local/tools"
checksum=$(cat go.mod cmd/reef-local/main.go internal/local/*.go | cksum)
tool="$tools/reef-local-$target_os-$target_arch"
if [ ! -x "$tool" ] || [ ! -f "$tools/source.cksum" ] || [ "$(cat "$tools/source.cksum")" != "$checksum" ]; then
  if [ -n "${DOCKER_HOST:-}" ] || [ -n "${DOCKER_CONTEXT:-}" ]; then printf '%s\n' 'Unset DOCKER_HOST and DOCKER_CONTEXT; select a local Docker context.' >&2; exit 1; fi
  context=$(docker context show)
  endpoint=$(docker context inspect "$context" --format '{{.Endpoints.docker.Host}}')
  case "$endpoint" in
    unix:///*) ;;
    *) printf '%s\n' 'Refusing a non-local Docker endpoint. Select a local Unix-socket context.' >&2; exit 1 ;;
  esac
  socket_path=${endpoint#unix://}
  case "$socket_path" in
    /|//*|*/../*|*/./*|*/..|*/.|*'?'*|*'#'*) printf '%s\n' 'Invalid local Docker socket path.' >&2; exit 1 ;;
  esac
  identity=$(docker --context "$context" info --format '{{.OSType}}|{{.ID}}')
  engine_type=${identity%%|*}
  engine_id=${identity#*|}
  [ "$engine_type" = linux ] && [ -n "$engine_id" ] || { printf '%s\n' 'A reachable Linux Docker engine with a stable ID is required.' >&2; exit 1; }
  mkdir -p "$tools"
  docker --context "$context" run --rm --network none --label io.reef.tooling=launcher \
    -e CGO_ENABLED=0 -e GOOS="$target_os" -e GOARCH="$target_arch" \
    -v "$root/go.mod:/src/go.mod:ro" -v "$root/cmd/reef-local:/src/cmd/reef-local:ro" \
    -v "$root/internal/local:/src/internal/local:ro" -v "$tools:/out" \
    -w /src golang:1.27-alpine go build -buildvcs=false -trimpath -o /out/reef-local ./cmd/reef-local
  mv "$tools/reef-local" "$tool"
  printf '%s\n' "$checksum" > "$tools/source.cksum"
fi
exec "$tool" -root "$root" "$@"
