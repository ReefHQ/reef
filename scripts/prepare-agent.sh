#!/bin/sh
# Run on the agent host in its existing dedicated reef-agent installation.
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
installation=$(pwd -P)
[ "$(basename "$installation")" = reef-agent ] || { echo 'Run from the dedicated reef-agent folder.' >&2;exit 1; }
parent=$(dirname "$installation")
[ "$parent" != / ] || { echo 'The installation parent cannot be the filesystem root.' >&2;exit 1; }
version=${1:-v0.6.0}
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || { echo 'Pass a published stable version, e.g. v0.6.0.' >&2;exit 1; }
docker_config=${DOCKER_CONFIG:-${HOME:?}/.docker}
[ -d "$docker_config" ] || { echo 'Run docker login on this host before preparation.' >&2;exit 1; }
docker_config=$(CDPATH= cd -- "$docker_config" && pwd -P)
for path in "$installation" "$parent" "$docker_config"; do
 case "$path" in *[!A-Za-z0-9/_.-]*) echo 'Configure paths with spaces/special characters manually.' >&2;exit 1;; esac
done
[ -f .env ] && [ ! -L .env ] || { echo 'Existing private .env with REEF_SERVER is required.' >&2;exit 1; }
grep -Eq '^REEF_SERVER=https://[^[:space:]]+$' .env || { echo 'Set the HTTPS origin in REEF_SERVER.' >&2;exit 1; }
umask 077
lock=.reef-agent-prepare.lock
mkdir "$lock" 2>/dev/null || { echo 'Preparation is already running.' >&2;exit 1; }
temporary=
cleanup(){ [ -z "$temporary" ] || rm -f "$temporary";rmdir "$lock"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
backup=.env.before-remote-parity
[ ! -L "$backup" ] || { echo 'Refusing a symlinked backup.' >&2;exit 1; }
[ -e "$backup" ] || cp -p .env "$backup"
temporary=$(mktemp .env.agent.XXXXXX)
cp -p .env "$temporary"
awk -v version="$version" -v installation="$installation" -v parent="$parent" -v docker_config="$docker_config" '
 /^REEF_AGENT_IMAGE=/ {print "REEF_AGENT_IMAGE=registry.gitlab.renaudbaivier.com/reef/reef/agent:" version;seen_image=1;next}
 /^REEF_AGENT_INSTALLATION_DIR=/ {seen_installation=1}
 /^REEF_AGENT_PROJECT_ROOT=/ {seen_parent=1}
 /^REEF_AGENT_DOCKER_CONFIG_DIR=/ {seen_docker=1}
 {print}
 END {
 if(!seen_image)print "REEF_AGENT_IMAGE=registry.gitlab.renaudbaivier.com/reef/reef/agent:" version;
 if(!seen_installation)print "REEF_AGENT_INSTALLATION_DIR=" installation;
 if(!seen_parent)print "REEF_AGENT_PROJECT_ROOT=" parent;
 if(!seen_docker)print "REEF_AGENT_DOCKER_CONFIG_DIR=" docker_config;
 }
' .env > "$temporary"
mv "$temporary" .env;temporary=
printf 'Agent configuration prepared for %s. Existing URL, identity and credentials preserved.\n' "$version"
printf 'Default project root: %s (mounted read/write). No service was started.\n' "$parent"
