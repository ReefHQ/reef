#!/bin/sh
# Fake runner admission checks, then real offline extraction in a synthetic repo.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
work=$(mktemp -d)
container="reef-renovate-extraction-${CI_JOB_ID:-$$}"
created=false
cleanup(){
 if [ "$created" = true ]; then docker rm -f "$container" >/dev/null 2>&1 || true; fi
 rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
mkdir -p "$work/bin" "$work/fixture"
chmod 755 "$work" "$work/fixture"
cat > "$work/bin/docker" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$RENOVATE_TEST_LOG"
case " $* " in *' --mount '*|*' --volume '*|*' -v '*) echo 'Checkout bind mounts are unavailable on this runner' >&2; exit 1;; esac
case "$1" in inspect) printf '%s\n' "${RENOVATE_TEST_EXIT_CODE:-0}";; esac
MOCK
chmod 700 "$work/bin/docker"
export RENOVATE_TEST_LOG="$work/commands"
export REEF_RENOVATE_ENABLED=true CI_PIPELINE_SOURCE=schedule CI_COMMIT_REF_PROTECTED=true
export CI_DEFAULT_BRANCH=master CI_COMMIT_BRANCH=master CI_PROJECT_PATH=reef/reef CI_PROJECT_ID=102
export CI_API_V4_URL=https://gitlab.example.invalid/api/v4 CI_DEBUG_TRACE=false
export RENOVATE_TOKEN=fixture-gitlab-secret GITHUB_COM_TOKEN=fixture-github-secret
unset REEF_RENOVATE_DRY_RUN
fake_run(){ PATH="$work/bin:$PATH" sh "$root/scripts/renovate.sh"; }
: > "$RENOVATE_TEST_LOG"
PATH="$work/bin:$PATH" sh "$root/scripts/renovate.sh" validate
grep -Fq 'cp ' "$RENOVATE_TEST_LOG"
grep -Fq 'start --attach reef-renovate-validation-' "$RENOVATE_TEST_LOG"
grep -Fq 'rm -f reef-renovate-validation-' "$RENOVATE_TEST_LOG"
if RENOVATE_TEST_EXIT_CODE=1 PATH="$work/bin:$PATH" sh "$root/scripts/renovate.sh" validate > "$work/rejected" 2>&1; then echo 'Validator container failure was ignored'; exit 1; fi
: > "$RENOVATE_TEST_LOG"
fake_run
grep -Fq 'RENOVATE_DRY_RUN=full' "$RENOVATE_TEST_LOG"
grep -Fq 'RENOVATE_AUTODISCOVER=false' "$RENOVATE_TEST_LOG"
grep -Fq -- '--env RENOVATE_TOKEN --env GITHUB_COM_TOKEN' "$RENOVATE_TEST_LOG"
if grep -Eq 'fixture-gitlab-secret|fixture-github-secret|docker.sock|/root/.docker' "$RENOVATE_TEST_LOG"; then echo 'Runner exposed credentials or host resources'; exit 1; fi
: > "$RENOVATE_TEST_LOG"
REEF_RENOVATE_DRY_RUN=off fake_run
if grep -q 'RENOVATE_DRY_RUN=' "$RENOVATE_TEST_LOG"; then echo 'Real run retained an invalid dry-run value'; exit 1; fi
for failure in source protected branch project id trace endpoint mode gitlab-token github-token; do
 : > "$RENOVATE_TEST_LOG"
 if (
  case "$failure" in
   source) export CI_PIPELINE_SOURCE=merge_request_event;;
   protected) export CI_COMMIT_REF_PROTECTED=false;;
   branch) export CI_COMMIT_BRANCH=untrusted;;
   project) export CI_PROJECT_PATH=other/repo;;
   id) export CI_PROJECT_ID=999;;
   trace) export CI_DEBUG_TRACE=true;;
   endpoint) export CI_API_V4_URL=http://gitlab.example.invalid/api/v4;;
   mode) export REEF_RENOVATE_DRY_RUN=invalid;;
   gitlab-token) unset RENOVATE_TOKEN;;
   github-token) unset GITHUB_COM_TOKEN;;
  esac
  fake_run
 ) > "$work/rejected" 2>&1; then echo "Invalid Renovate run accepted: $failure"; exit 1; fi
 test ! -s "$RENOVATE_TEST_LOG"
done
: > "$RENOVATE_TEST_LOG"
REEF_RENOVATE_ENABLED=false fake_run
test ! -s "$RENOVATE_TEST_LOG"
# Real validator failure must propagate through create/copy/start and cleanup.
mkdir -p "$work/invalid/scripts"
cp "$root/scripts/renovate.sh" "$work/invalid/scripts/renovate.sh"
printf '{invalid json\n' > "$work/invalid/renovate.json"
invalid_job="invalid-$$"
if CI_JOB_ID="$invalid_job" sh "$work/invalid/scripts/renovate.sh" validate > "$work/invalid-result" 2>&1; then
 echo 'Invalid real Renovate configuration passed validation'; exit 1
fi
if docker inspect "reef-renovate-validation-$invalid_job" >/dev/null 2>&1; then
 echo 'Failed validator container was not cleaned up'; exit 1
fi
# Copy only dependency-bearing source files; never the real Git history/runtime.
for file in renovate.json Dockerfile deploy-agent/Dockerfile .github/workflows/*.yml scripts/ci.sh scripts/local.sh scripts/ensure-buildx.sh scripts/renovate.sh internal/app/volume_backups.go internal/app/*_integration_test.go examples/compose.install.yaml tests/renovate-extraction.cjs; do
 mkdir -p "$work/fixture/$(dirname "$file")"
 cp "$root/$file" "$work/fixture/$file"
done
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
unset GIT_DIR GIT_WORK_TREE
git init -q --initial-branch=master "$work/fixture"
git -C "$work/fixture" add .
git -C "$work/fixture" -c user.name=Fixture -c user.email=test@example.invalid -c commit.gpgsign=false commit -qm 'Synthetic dependency fixture'
image=$(awk -F= '/^image=/{print $2;exit}' "$root/scripts/renovate.sh")
docker create --name "$container" --cap-drop=ALL --security-opt no-new-privileges:true \
 --network none --tmpfs /tmp:rw,nosuid,nodev,mode=1777 \
 --env GITHUB_COM_TOKEN=fixture-offline-only --env LOG_LEVEL=info \
 --env RENOVATE_BINARY_SOURCE=global --env RENOVATE_BASE_DIR=/tmp/renovate --env RENOVATE_CACHE_DIR=/tmp/renovate/cache \
 --env RENOVATE_ALLOW_SCRIPTS=false --env RENOVATE_ALLOW_PLUGINS=false --env 'RENOVATE_ALLOWED_COMMANDS=[]' \
 --env 'RENOVATE_FORCE={"automerge":false,"platformAutomerge":false}' \
 "$image" sh -ec 'cp -R /fixture /tmp/fixture; cd /tmp/fixture; LOG_LEVEL=debug LOG_FORMAT=json renovate --platform=local --dry-run=extract --onboarding=false --require-config=required > /tmp/extract.log 2>&1; node tests/renovate-extraction.cjs /tmp/extract.log' >/dev/null
created=true
docker cp "$work/fixture" "$container:/fixture"
docker start --attach "$container"
status=$(docker inspect --format '{{.State.ExitCode}}' "$container")
[ "$status" = 0 ] || exit 1
echo 'PASS: protected scoped Renovate runner, credential handling, dry-run modes and real offline dependency extraction.'
