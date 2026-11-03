# Reef, ready to pull 🌊

GitHub Actions builds public images from the mirrored release source:

| Image | Role |
| --- | --- |
| `ghcr.io/reefhq/reef` | Central Reef, browser interface and local Docker host |
| `ghcr.io/reefhq/reef-agent` | Outbound remote agent and optional update worker |

Both support Linux `amd64` and `arm64`. Tags include `v0.32.0` and `0.32.0`,
the full source revision and `latest`. Use a version tag for predictable upgrades;
use an image digest if you need an immutable installation reference. `latest`
does not update a running container by itself.

**First public release: 0.32.0, published on November 3, 2026.** Both images
are accessible anonymously, with Linux amd64/arm64 manifests and matching original
source revisions verified. The installation commands below are ready to use.
For future versions, wait for the GitHub publication workflow to succeed; a tag
alone does not guarantee image readiness. Earlier versions were published only
in the existing GitLab registry.

## Install without cloning

Use a Linux Docker engine with Docker CLI, Compose v2 and either OpenSSL or `od`
for password generation. On macOS, Docker needs a local Linux runtime; Docker
Desktop is optional. Public GHCR images need no registry login.

Create a **new** dedicated `reef` directory under the parent containing the
applications you want Reef to manage, for example `/srv/docker/reef`. That parent
is mounted read/write at its identical absolute host path. Do not use `/reef` or
run these downloads over an existing installation. Paths with spaces or special
characters require manual configuration. Never mix this Compose project with
the source launcher's `.reef-local` installation or the existing CI deployment.

From that new directory, download the two files for the chosen release:

```sh
curl -fL --proto '=https' -o compose.yaml https://raw.githubusercontent.com/ReefHQ/reef/v0.32.0/examples/public/compose.yaml
curl -fL --proto '=https' -o prepare-installation.sh https://raw.githubusercontent.com/ReefHQ/reef/v0.32.0/scripts/prepare-installation.sh
sh prepare-installation.sh v0.32.0
docker compose config --quiet && docker compose pull && docker compose up -d
```

Open `http://127.0.0.1:8099`, sign in as **admin** and read the initial password:

```sh
cat secrets/password.txt
```

The preparation script creates `.env`, `config.json`, `state/` and
`secrets/password.txt` privately, preserves existing files and starts no services.
Keep that directory private and back it up. Changed account passwords are stored
as hashes in state; the bootstrap file does not reveal them.

Want LAN access from the start? Use `sh prepare-installation.sh v0.32.0 --lan`
instead. This sets `REEF_BIND_ADDRESS=0.0.0.0` in a **new** `.env`; browse to
`http://YOUR_SERVER_IP:8099`. Without the flag, a new installation binds to
`127.0.0.1`. Subsequent preparation preserves the saved bind address and version;
edit `.env` deliberately to change them, then recreate Reef. Set `REEF_PORT` if
8099 is already in use. Docker access gives Reef control over that host; keep
the UI on a trusted network or behind an authenticated HTTPS reverse proxy.

The default configuration disables remote enrollment. Follow the
[agent setup](AGENT_SETUP.md) for HTTPS and remote hosts. To pull private project
images, arrange host-local Docker credentials as described in
[configuration](CONFIGURATION.md); this public example adds no credential mount.

## Upgrade this installation

Wait for the target image to be published and let active operations finish.
Back up the private installation and read the changelog. Change only
`REEF_VERSION` in `.env` to the desired published `vMAJOR.MINOR.PATCH`, then:

```sh
docker compose config --quiet && docker compose pull reef && docker compose up -d --no-deps reef
docker compose logs --tail=30 reef
curl -fsS http://127.0.0.1:8099/healthz
```

Adapt the health-check address/port if changed. This recreates Reef; its accounts,
configuration and state persist. Project containers keep running. Avoid `down -v`
and preserve the installation's absolute path and Compose identity. Downgrading
is not guaranteed to understand newer state.

## Agents and their updates

The existing [agent workflow](AGENT_SETUP.md) still applies. Starting with 0.32.0,
`prepare-agent.sh` selects `ghcr.io/reefhq/reef-agent` when no agent image is saved;
an existing GitLab installation keeps its registry. The script creates an empty
private Docker configuration directory if needed for public installations; it
does not require login to pull public images. Host credentials remain necessary
for private application images.

To deliberately migrate an existing agent after public images are available:

```sh
REEF_AGENT_REPOSITORY=ghcr.io/reefhq/reef-agent sh prepare-agent.sh v0.32.0
```

Use the updated 0.32.0 preparation script. Preserve the CA, URL, Compose project
name and identity volume; review and recreate only the agent as the agent guide
describes. This does not migrate an already configured updater automatically.
If using the update overlay, update `REEF_AGENT_UPDATER_IMAGE` to
`ghcr.io/reefhq/reef-agent:v0.32.0` and recreate that service too. A pre-0.32 worker
accepts only GitLab images. The 0.32+ worker accepts the two official repositories
and keeps the installed agent's registry during later updates; arbitrary
repositories remain refused. Existing GitLab agents and workers keep working.

## Publication through GitHub Actions (maintainers)

GitLab continues to validate releases, build its private images, deploy the existing
installation and mirror the audited source/tag to GitHub. On a new `v*` tag in
`ReefHQ/reef`, **Publish public container images** builds and publishes the two
GHCR images for Linux amd64/arm64. The script checks a full stable version matching
`VERSION` and the checked-out tag. Jobs are serialized; all four builds/pushes must
succeed before version/revision/latest aliases are created. Retrying an older tag
repairs its version aliases without moving `latest` backward when newer stable
tags are present. `latest` is assigned only to the highest stable tag fetched by
checkout. This pipeline is independent of the GitLab production deployment.

The workflow uses GitHub's automatic `GITHUB_TOKEN`, limited to `contents: read`
and `packages: write`. **No GHCR username, PAT or activation variable is needed
in GitLab.** Leave the existing GitLab deployment and mirror credentials as they
are. An organization policy may require allowing GitHub Actions to create packages.
Publication runs only in the official repository on tags, never on pull requests
or forks. Checkout is pinned to an exact action revision and does not persist its
Git credentials; the temporary Docker authentication directory is removed afterward.

The two registries build independently, so byte-identical image digests are not
promised. They retain the same version and **original source revision**, which
Reef's agent updater verifies. The GitLab-to-GitHub mirror rewrites commit SHAs;
it replaces `.github/source-revision`'s source placeholder with the original
GitLab commit ID only for commits introducing that marker. Earlier public history
and tags remain unchanged. Public images use that original ID for build information
and `org.opencontainers.image.revision`, and record the rewritten GitHub ID in
`io.reef.public-revision`. A raw placeholder, absent metadata or invalid original
revision stops publication. Do not substitute `github.sha` for the original ID.

Each image includes source/version/revision/GPL metadata and the GPL text. The
public version tag provides corresponding source. No original private Git objects,
credentials or Codex session metadata are added to the mirror.

After the first successful workflow, open ReefHQ's **Packages**, select each
package, then **Package settings → Change visibility → Public**. Linking to the
public source repository does **not** make a package public. Verify both images
from an empty Docker configuration, without credentials, and check that
`docker buildx imagetools inspect` shows both Linux architectures before announcing
installation. Keep the publication ticket open until those anonymous reads and
the source tag are verified.

To retry a failed release, rerun its Actions workflow. The manual workflow can
also be dispatched against a tag containing this workflow; dispatching a branch
skips publication. Tags predating the workflow/metadata cannot be backfilled this
way. Do not move an existing release tag to publish a new revision.
Registry writes are not transactional: a later manifest failure can leave partial
aliases, so inspect both packages before retrying. A passing GitLab pipeline does
not prove that the separate GitHub publication succeeded.

See [GitHub's container publishing workflow](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)
and [package visibility documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).
