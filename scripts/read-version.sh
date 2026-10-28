#!/bin/sh
# Release delivery currently accepts stable vMAJOR.MINOR.PATCH tags only.
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
version=$(cat "${1:-$root/VERSION}")
case "$version" in ''|*[!0-9.]*) echo 'A stable VERSION (MAJOR.MINOR.PATCH) is required for delivery.' >&2;exit 1;; esac
if ! printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
 echo 'Invalid stable VERSION.' >&2;exit 1
fi
printf '%s\n' "$version"
