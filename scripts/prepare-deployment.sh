#!/bin/sh
# Initialize private files on the selected server; never starts Docker/services.
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
source_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
installation=$(CDPATH= cd -- "${1:-.}" && pwd -P)
parent=$(dirname "$installation")
if [ "$(basename "$installation")" != reef ] || [ "$parent" = / ]; then
 echo 'Run preparation in the dedicated reef folder beside the existing applications.' >&2;exit 1
fi
case "$installation" in *[!A-Za-z0-9/_.-]*) echo 'Prepare paths containing spaces/special characters manually in .env.' >&2;exit 1;; esac
if [ ! -f "$installation/compose.production.yaml" ] || [ ! -d "$installation/deploy-agent" ]; then
 echo 'Copy the production Compose and deploy-agent folder into reef first.' >&2;exit 1
fi
for path in "$installation/.env" "$installation/config.json" "$installation/state" "$installation/secrets" "$installation/secrets/password.txt"; do
 if [ -L "$path" ]; then echo 'Refusing a symlinked installation state/config path.' >&2;exit 1;fi
done
umask 077
lock=$installation/.reef-prepare.lock
mkdir "$lock" 2>/dev/null || { echo 'Preparation already running; inspect its lock before retrying.' >&2;exit 1; }
temporary=
cleanup(){ [ -z "$temporary" ] || rm -f "$temporary";rmdir "$lock"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
random_secret(){
 if command -v openssl >/dev/null 2>&1; then
  value=$(openssl rand -hex 32) || { echo 'Could not generate a private secret with OpenSSL.' >&2;exit 1; }
 elif command -v od >/dev/null 2>&1; then
  value=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
 else
  echo 'Secret generation requires openssl or od; install either and rerun preparation.' >&2;exit 1
 fi
 case "$value" in ''|*[!0-9a-f]*) echo 'Invalid private secret generated.' >&2;exit 1;; esac
 [ "${#value}" -eq 64 ] || { echo 'Could not generate a private secret.' >&2;exit 1; }
 printf '%s' "$value"
}
for dir in "$installation/state" "$installation/secrets"; do
 if [ ! -d "$dir" ]; then mkdir -m 700 "$dir";fi
done
if [ ! -e "$installation/secrets/password.txt" ]; then
 temporary=$(mktemp "$installation/secrets/.password.XXXXXX")
 random_secret > "$temporary";printf '\n' >> "$temporary"
 ln "$temporary" "$installation/secrets/password.txt";rm "$temporary";temporary=
fi
if [ ! -e "$installation/config.json" ]; then
 temporary=$(mktemp "$installation/.config.XXXXXX")
 printf '%s\n' '{"listen":"0.0.0.0:8099","state_dir":"/var/lib/reef","secure_cookie":false,"agents_enabled":false,"projects":[]}' > "$temporary"
 ln "$temporary" "$installation/config.json";rm "$temporary";temporary=
fi
if [ ! -e "$installation/.env" ]; then
 token=$(random_secret)
 temporary=$(mktemp "$installation/.env.XXXXXX")
 awk -v installation="$installation" -v parent="$parent" -v token="$token" '
 {gsub("/CANONICAL/ABSOLUTE/PATH/reef",installation);gsub("/CANONICAL/ABSOLUTE/PATH",parent)}
 $0=="DEPLOY_TOKEN=" {print "DEPLOY_TOKEN=" token;next}
 {print}
 ' "$source_root/.env.production.example" > "$temporary"
 ln "$temporary" "$installation/.env";rm "$temporary";temporary=
fi
printf 'Private configuration prepared in %s; existing files preserved.\n' "$installation"
printf 'Fill REGISTRY_USER and REGISTRY_PASSWORD in .env before starting services.\n'
printf 'LAN access uses the same server address as Flix, port 8099 by default. No service was started.\n'
