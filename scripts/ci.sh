#!/bin/sh
# Shell runners use disposable containers, like the other local GitLab projects.
set -eu

cd "$(dirname "$0")/.."
case "${1:-}" in
  go)
    image=golang:1.27-bookworm
    checks='go test -race ./... && go vet ./... &&
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/reef-amd64 ./cmd/reef &&
      CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/reef-arm64 ./cmd/reef &&
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/reef-local ./cmd/reef-local'
    ;;
  frontend)
    image=node:24-bookworm-slim
    checks='node --test tests/theme.test.cjs tests/view-model.test.cjs tests/logs.test.cjs &&
      node --check web/model.js && node --check web/app.js &&
      node --check web/inventory.js && node --check web/theme.js && node --check web/logs.js'
    ;;
  *) echo 'Usage: sh scripts/ci.sh go|frontend' >&2; exit 2 ;;
esac

container="reef-quality-$1-${CI_JOB_ID:-$$}"
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker create --name "$container" --network none --workdir /src \
  --env GOTOOLCHAIN=local --env GOPROXY=off --env GOSUMDB=off \
  "$image" sh -ec "$checks"
# Copy only the required source; never copy .git or the private local runtime.
for source in go.mod cmd internal web tests; do
  docker cp "$source" "$container:/src/"
done
docker start --attach "$container"
