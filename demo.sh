#!/bin/sh
set -eu
cd "$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
if command -v go >/dev/null 2>&1; then
  exec go run ./cmd/reef -demo "$@"
fi
printf '%s\n' 'The optional offline preview requires Go 1.23+. For real Docker mode, use ./reef-local.sh up.' >&2
exit 1
