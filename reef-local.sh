#!/bin/sh
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
exec "$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)/scripts/local.sh" "$@"
