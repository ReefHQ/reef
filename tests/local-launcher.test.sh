#!/bin/sh
# Exercise the shell bootstrap with fake Docker/uname; never start real services.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
cat > "$work/bin/uname" <<'MOCK'
#!/bin/sh
case "$1" in -s) printf '%s\n' "$TEST_SYSTEM";; -m) printf 'x86_64\n';; esac
MOCK
cat > "$work/bin/docker" <<'MOCK'
#!/bin/sh
set -eu
[ "${LANG:-}" = C ] && [ "${LC_ALL:-}" = C ] && [ "${LANGUAGE:-}" = en ] || { echo 'Docker did not receive the English locale' >&2;exit 1; }
printf '%s\n' "$*" >> "$TEST_DOCKER_LOG"
case "$*" in
 'context show') printf 'fixture-context\n';;
 'context inspect '*) printf '%s\n' "$TEST_ENDPOINT";;
 *' info --format '*) printf '%s\n' "$TEST_INFO";;
 *' run '*)
  output=''
  for argument do case "$argument" in *:/out) output=${argument%:/out};; esac;done
  [ -n "$output" ]
  cat > "$output/reef-local" <<'TOOL'
#!/bin/sh
printf '%s\n' "$*" > "$TEST_LAUNCHER_ARGS"
TOOL
  chmod 700 "$output/reef-local"
  ;;
 *) echo 'Unexpected fixture Docker command' >&2;exit 1;;
esac
MOCK
chmod 700 "$work/bin/uname" "$work/bin/docker"
export PATH="$work/bin:$PATH"
unset DOCKER_HOST DOCKER_CONTEXT
scenario(){
 name=$1;export TEST_SYSTEM=$2 TEST_ENDPOINT=$3 TEST_INFO=$4
 fixture=$work/$name
 mkdir -p "$fixture/scripts" "$fixture/cmd/reef-local" "$fixture/internal/local"
 cp "$root/scripts/local.sh" "$fixture/scripts/local.sh"
 cp "$root/go.mod" "$fixture/go.mod"
 cp "$root/cmd/reef-local/main.go" "$fixture/cmd/reef-local/main.go"
 cp "$root/internal/local/launcher.go" "$fixture/internal/local/launcher.go"
 export TEST_DOCKER_LOG=$fixture/docker-commands TEST_LAUNCHER_ARGS=$fixture/invocation
}
for platform in Linux Darwin; do
 scenario "$platform-local" "$platform" unix:///var/run/docker.sock 'linux|fixture-engine'
 LANG=fr_FR.UTF-8 LC_ALL=fr_FR.UTF-8 LANGUAGE=fr sh "$fixture/scripts/local.sh" status
 grep -Fq -- "-root $fixture status" "$TEST_LAUNCHER_ARGS"
 grep -Fq ' run --rm --network none' "$TEST_DOCKER_LOG"
done
scenario rootless Linux unix:///run/user/1000/docker.sock 'linux|rootless-engine'
sh "$fixture/scripts/local.sh" status
test -s "$TEST_LAUNCHER_ARGS"
scenario colima Darwin unix:///Users/test/.colima/default/docker.sock 'linux|colima-engine'
sh "$fixture/scripts/local.sh" status
test -s "$TEST_LAUNCHER_ARGS"
scenario orbstack Darwin unix:///Users/test/.orbstack/run/docker.sock 'linux|orbstack-engine'
sh "$fixture/scripts/local.sh" status
test -s "$TEST_LAUNCHER_ARGS"
for endpoint in ssh://remote tcp://127.0.0.1:2375 unix://remote/var/run/docker.sock; do
 scenario rejected-endpoint Linux "$endpoint" 'linux|fixture-engine'
 if sh "$fixture/scripts/local.sh" status > "$work/rejected" 2>&1;then echo 'Nonlocal endpoint accepted';exit 1;fi
 test ! -e "$TEST_LAUNCHER_ARGS"
 if grep -Fq ' run ' "$TEST_DOCKER_LOG";then echo 'Compiler contacted rejected engine';exit 1;fi
done
for identity in 'windows|fixture-engine' 'linux|'; do
 scenario rejected-engine Darwin unix:///var/run/docker.sock "$identity"
 if sh "$fixture/scripts/local.sh" status > "$work/rejected" 2>&1;then echo 'Unsupported engine accepted';exit 1;fi
 test ! -e "$TEST_LAUNCHER_ARGS"
done
printf 'PASS: Linux/macOS local Docker bootstrap without Desktop checks; remote and incompatible engines rejected.\n'
