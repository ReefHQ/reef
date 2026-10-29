#!/bin/sh
# Shell runners use disposable containers, like the other local GitLab projects.
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en

cd "$(dirname "$0")/.."
case "${1:-}" in
  go)
    image=golang:1.27-bookworm
    checks='go test -race ./... && go vet ./... &&
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/reef-amd64 ./cmd/reef &&
      CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/reef-arm64 ./cmd/reef &&
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/reef-local ./cmd/reef-local &&
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/reef-agent-amd64 ./cmd/reef-agent &&
      CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/reef-agent-arm64 ./cmd/reef-agent'
    ;;
  frontend)
    image=node:24-bookworm-slim
    checks='node scripts/check-version.cjs && node --test tests/*.test.cjs &&
      node --check web/operation-panels.js && node --check web/model.js && node --check web/app.js &&
      node --check web/builds.js && node --check web/users.js && node --check web/access.js && node --check web/schedules.js && node --check web/alerts.js && node --check web/history.js && node --check web/stats.js && node --check web/networks.js && node --check web/volume-backups.js && node --check web/volumes.js && node --check web/images.js && node --check web/image-updates.js && node --check web/navigation.js && node --check web/inventory.js && node --check web/theme.js && node --check web/logs.js && node --check web/console.js && node --check web/console-picker.js && node --check web/container-management.js && node --check web/container-details.js && node --check web/console-dock.js && node --check web/console-frame.js && node --check web/webhooks.js && node --check web/agent-diagnostics.js && node --check web/hosts.js'
    ;;
  *) echo 'Usage: sh scripts/ci.sh go|frontend' >&2; exit 2 ;;
esac

container="reef-quality-$1-${CI_JOB_ID:-$$}"
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker create --name "$container" --network none --workdir /src \
  --env GOTOOLCHAIN=local --env GOPROXY=off --env GOSUMDB=off \
  --env CI_COMMIT_TAG="${CI_COMMIT_TAG:-}" \
  "$image" sh -ec "$checks"
# Copy only the required source; never copy .git or the private local runtime.
for source in go.mod VERSION cmd internal web tests scripts; do
  docker cp "$source" "$container:/src/"
done
docker start --attach "$container"
