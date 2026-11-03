#!/bin/sh
# Public image installation: private files only, no Docker or network commands.
set -eu
export LANG=C LC_ALL=C LANGUAGE=en
umask 077
version=${1:-v0.32.0}
access=${2:-}
[ "$#" -le 2 ] || { echo 'Usage: sh prepare-installation.sh VERSION [--lan]' >&2; exit 1; }
case "$access" in '') bind_address=127.0.0.1;; --lan) bind_address=0.0.0.0;; *) echo 'Usage: sh prepare-installation.sh VERSION [--lan]' >&2; exit 1;; esac
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || { echo 'Pass a published stable version, e.g. v0.32.0.' >&2; exit 1; }
installation=$(pwd -P)
parent=$(dirname "$installation")
[ "$(basename "$installation")" = reef ] && [ "$parent" != / ] || { echo 'Run from a dedicated reef directory below your application directory.' >&2; exit 1; }
case "$installation" in *[!A-Za-z0-9/_.-]*) echo 'Prepare paths with spaces or special characters manually.' >&2; exit 1;; esac
[ -f compose.yaml ] && [ ! -L compose.yaml ] || { echo 'Download the public compose.yaml first.' >&2; exit 1; }
for path in .env config.json state secrets secrets/password.txt; do
 [ ! -L "$path" ] || { echo 'Refusing a symlinked installation path.' >&2; exit 1; }
done
for path in .env config.json secrets/password.txt; do
 [ ! -e "$path" ] || [ -f "$path" ] || { echo 'Existing configuration must be a regular file.' >&2; exit 1; }
done
for path in state secrets; do
 [ ! -e "$path" ] || [ -d "$path" ] || { echo 'Existing state and secrets must be directories.' >&2; exit 1; }
done
mkdir .reef-prepare.lock 2>/dev/null || { echo 'Preparation already running; inspect its lock before retrying.' >&2; exit 1; }
temporary=
cleanup(){ [ -z "$temporary" ] || rm -f "$temporary"; rmdir .reef-prepare.lock; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
for path in state secrets; do
 [ -d "$path" ] || mkdir -m 700 "$path"
done
if [ ! -e secrets/password.txt ]; then
 temporary=$(mktemp secrets/.password.XXXXXX)
 if command -v openssl >/dev/null 2>&1; then
  value=$(openssl rand -hex 32)
 elif command -v od >/dev/null 2>&1; then
  value=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
 else
  echo 'Secret generation requires openssl or od.' >&2; exit 1
 fi
 case "$value" in ''|*[!0-9a-f]*) echo 'Invalid generated secret.' >&2; exit 1;; esac
 [ "${#value}" -eq 64 ] || { echo 'Could not generate a private secret.' >&2; exit 1; }
 printf '%s\n' "$value" > "$temporary"
 ln "$temporary" secrets/password.txt
 rm "$temporary"; temporary=
fi
if [ ! -e config.json ]; then
 temporary=$(mktemp .config.XXXXXX)
 printf '%s\n' '{"listen":"0.0.0.0:8099","state_dir":"/var/lib/reef","secure_cookie":false,"agents_enabled":false,"projects":[]}' > "$temporary"
 ln "$temporary" config.json
 rm "$temporary"; temporary=
fi
if [ ! -e .env ]; then
 temporary=$(mktemp .env.XXXXXX)
 printf '%s\n' "REEF_VERSION=$version" "REEF_INSTALLATION_DIR=$installation" "REEF_PROJECT_ROOT=$parent" "REEF_BIND_ADDRESS=$bind_address" 'REEF_PORT=8099' > "$temporary"
 ln "$temporary" .env
 rm "$temporary"; temporary=
fi
printf 'Prepared %s; existing configuration and passwords preserved. No service was started.\n' "$installation"
printf '%s\n' 'Review .env and compose.yaml, then run: docker compose config --quiet && docker compose pull && docker compose up -d'
printf '%s\n' 'Sign in as admin; the initial password is in secrets/password.txt.'
printf '%s\n' '127.0.0.1 allows this machine only; 0.0.0.0 allows LAN access. Browse to the server IP, never 0.0.0.0.'
