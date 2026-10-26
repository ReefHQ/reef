#!/bin/sh
set -eu
: "${DEPLOY_WEBHOOK_URL:?}"
: "${DEPLOY_WEBHOOK_TOKEN:?}"
: "${HEALTHCHECK_URL:?}"
: "${CI_COMMIT_SHA:?}"
version=$(sh scripts/read-version.sh)
test "${CI_COMMIT_TAG:-}" = "v$version" || { echo 'Release tag and VERSION differ.' >&2;exit 1; }
test "${CI_COMMIT_REF_PROTECTED:-}" = true || { echo 'Protected tag required.' >&2;exit 1; }
# A synchronous webhook waits through Reef's graceful stop (up to 46 minutes).
# Do not retry this POST automatically: an unknown outcome must be inspected.
curl --fail-with-body --silent --show-error --connect-timeout 10 --max-time 3300 -X POST \
 -H "X-Deploy-Token: $DEPLOY_WEBHOOK_TOKEN" -H "X-Deploy-Version: $CI_COMMIT_TAG" \
 -H "X-Deploy-Revision: $CI_COMMIT_SHA" "$DEPLOY_WEBHOOK_URL"
response=$(mktemp)
trap 'rm -f "$response"' EXIT
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
 if curl --fail --silent --show-error --connect-timeout 5 --max-time 10 "$HEALTHCHECK_URL" -o "$response" &&
  grep -Fq '"status":"ok"' "$response" && grep -Fq "\"version\":\"$version\"" "$response" &&
  grep -Fq "\"revision\":\"$CI_COMMIT_SHA\"" "$response"; then
  echo "Verified Reef $CI_COMMIT_TAG at $CI_COMMIT_SHA.";exit 0
 fi
 echo "Health check retry $attempt: expected release not confirmed."
 sleep 5
done
echo 'Deployed Reef did not confirm the expected version and revision.' >&2;exit 1
