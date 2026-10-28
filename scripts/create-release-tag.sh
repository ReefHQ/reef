#!/bin/sh
set -eu
# Keep Reef command diagnostics independent of the host language.
export LANG=C LC_ALL=C LANGUAGE=en
: "${CI_JOB_TOKEN:?Release tagging runs only in GitLab CI}"
: "${CI_API_V4_URL:?}"
: "${CI_PROJECT_ID:?}"
: "${CI_COMMIT_SHA:?}"
: "${CI_REPOSITORY_URL:?}"
if [ "${CI_COMMIT_BRANCH:-}" != "${CI_DEFAULT_BRANCH:-master}" ] || [ "${CI_COMMIT_REF_PROTECTED:-}" != true ]; then
 echo 'Only the protected default branch can create release tags.' >&2;exit 1
fi
version=$(sh scripts/read-version.sh)
tag=v$version
head_commit=$(git rev-parse HEAD)
if [ "$head_commit" != "$CI_COMMIT_SHA" ]; then echo 'Pipeline and checkout revisions differ.' >&2;exit 1;fi
latest_refs=$(git ls-remote "$CI_REPOSITORY_URL" "refs/heads/$CI_DEFAULT_BRANCH")
latest=$(printf '%s\n' "$latest_refs" | cut -f1)
if [ "$latest" != "$head_commit" ]; then echo 'Skipping superseded source pipeline.';exit 0;fi
remote_refs=$(git ls-remote --tags "$CI_REPOSITORY_URL" "refs/tags/$tag" "refs/tags/$tag^{}")
if [ -n "$remote_refs" ]; then
 remote_commit=$(printf '%s\n' "$remote_refs" | awk 'NR==1{first=$1} /\^\{\}$/{peeled=$1} END{print peeled?peeled:first}')
 if [ "$remote_commit" != "$head_commit" ]; then echo "$tag already exists; bump VERSION to deliver another revision.";exit 0;fi
 echo "$tag already points to this revision; retrying its tag pipeline."
else
 if git show-ref --verify --quiet "refs/tags/$tag"; then
  test "$(git rev-list -n 1 "$tag")" = "$head_commit" || { echo 'Conflicting local tag.' >&2;exit 1; }
 else
  # This is an automated CI release identity, not the developer's signing key.
  git -c user.name='Reef Release Bot' -c user.email='release@reef.invalid' -c tag.gpgSign=false tag -a "$tag" -m "Reef $version"
 fi
 git push "$CI_REPOSITORY_URL" "refs/tags/$tag"
fi
response=$(mktemp)
trap 'rm -f "$response"' EXIT
# A CI_JOB_TOKEN push does not itself start a pipeline.
for delay in 0 2 4 8 16; do
 [ "$delay" -eq 0 ] || sleep "$delay"
 status=$(curl --silent --show-error --connect-timeout 10 --max-time 30 -o "$response" -w '%{http_code}' -X POST \
  --form "token=$CI_JOB_TOKEN" --form "ref=$tag" "$CI_API_V4_URL/projects/$CI_PROJECT_ID/trigger/pipeline") || status=000
 if [ "$status" = 201 ]; then echo "Triggered $tag pipeline.";exit 0;fi
 echo "Tag pipeline trigger returned HTTP $status; retrying."
done
# Do not echo arbitrary API bodies that could contain credentials.
echo 'Unable to trigger the tag pipeline.' >&2;exit 1
