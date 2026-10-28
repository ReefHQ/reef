# Docker host agents

Upgraded agents provide container inventory, stdout/stderr logs, consoles, project
registration, Compose editing/actions and image checks/cleanup. The central Reef
owns browser authentication and the UI. Every filesystem access and Docker action
runs on the selected host, using the shared Reef backend. Agents keep an outbound
HTTPS connection; no incoming agent port or Docker TCP endpoint is needed.

## Central setup

Enable `agents_enabled: true` in central Reef's configuration and provide an HTTPS
origin reachable by agents, with a valid certificate or a trusted private CA.
Forward `/agent/v1/` POST requests and permit bodies up to 16 MiB. Commands use
three-second long polling and bounded JSON responses, so normal HTTP proxying is
sufficient; no WebSocket or request-streaming proxy configuration is required.
TLS certificate verification stays enabled. Agents refuse HTTP and redirects.

An agent-only HTTPS gateway may expose `/agent/v1/` and `/healthz` while the browser
UI continues using a private HTTP listener. `secure_cookie: true` is needed when
the browser UI itself uses HTTPS. Keep central machine endpoints separate from
browser credentials; never send the Reef UI password to an agent.

Upgrade central Reef before agents, preserving the identity volume, CA and
configuration. No new enrollment is required. Project/context mounts and Docker
registry credentials remain on each agent. Features are negotiated per connection,
never enabled from the version string alone.

| Capability | Agent version | Feature |
| --- | --- | --- |
| control-v1 | 0.6.0 | Projects, logs and consoles |
| images-v1 | 0.7.0 | Image inventory and operations |
| volumes-v1 | 0.8.0 | Volume management |
| images-batch-v1 | 0.8.2 | Image selections above 100 |
| builds-v1 | 0.9.0 | Project builds and default-builder cache |
| agent-update-v1 | 0.10.0 + optional updater setup | Agent updates from Reef |

Older agents keep their supported features and show an upgrade message for new
ones. The commands below install the version with all capabilities listed here.

## Upgrade the existing agent

Keep its current `COMPOSE_PROJECT_NAME`, `REEF_SERVER`, CA and **agent-state volume**.
Do not enroll again. Copy `examples/agent/compose.yaml` into the existing dedicated
`reef-agent` installation and `scripts/prepare-agent.sh` there as `prepare-agent.sh`.
The image is separate from the central service, with Linux amd64/arm64 variants:
`registry.gitlab.renaudbaivier.com/reef/reef/agent:v0.10.0` once published.

The existing host Docker login must be valid for this registry. From that host's
`reef-agent` folder, after the chosen version is published:

```sh
sh prepare-agent.sh v0.10.0
docker compose config --quiet
docker compose pull reef-agent
docker compose up -d --no-deps reef-agent
docker compose logs --tail=30 reef-agent
```

Preparation computes canonical host paths and adds missing variables to private
`.env`. It updates only the explicitly requested image version, preserves the
central URL and other settings, saves `.env.before-remote-parity` once and starts
no service. It does not create or reset identity data. Custom existing path
variables remain operator-controlled; review them before starting the service.

The example mounts the installation's parent read/write at the identical host
path and passes `REEF_INSTALLATION_DIR`. For `/srv/docker/reef-agent`, the
default authorized project root is `/srv/docker`, using the same rule as Local.
Only projects explicitly confirmed through **Manage this project** are managed.
The agent's own Compose project/installation cannot be adopted.

Host Docker login configuration is mounted read-only at `/root/.docker` so pulls
and registry checks use credentials available on that server. It can contain
secrets; keep it private. External credential helpers must also exist in the agent
image, or use credentials compatible with its Docker CLI. The central deployment
webhook's login is not automatically available on remote hosts.

For custom authorized roots/projects/console limits, mount a JSON configuration
and add `-config /etc/reef-agent/config.json` to the command. It uses Reef's config
schema. An explicit `allowed_project_roots` replaces the installation-parent
default (including `[]` to disable adoption); bind each root at its identical
absolute path. Managed state is always stored below the private agent state
volume, independent from any `state_dir` value in this file. The browser cannot
broaden these roots or add mounts.

## Update agents from Reef

After one-time preparation on each host, **Manage hosts → Update to VERSION**
updates that agent to the exact stable version and source revision of central
Reef. Upgrade central first. Confirmation names the host and old/new versions;
there is no automatic upgrade, downgrade or arbitrary image/version selector.
A development central build cannot initiate updates.

For the initial setup, wait for host operations to finish and disconnect its
consoles. Preserve the current Compose project, private `.env`, CA and named
`agent-state` volume. Copy the current `compose.yaml`, optional
`compose.updates.yaml` and `prepare-agent.sh` into the existing dedicated
`reef-agent` folder. Once v0.10.0 is published, run there:

```sh
sh prepare-agent.sh v0.10.0 --enable-updates
docker compose config --quiet
docker compose pull reef-agent agent-updater
docker compose up -d --no-deps reef-agent agent-updater
docker compose logs --tail=30 reef-agent agent-updater
```

The opt-in adds `COMPOSE_FILE=compose.yaml:compose.updates.yaml` and an independent
`REEF_AGENT_UPDATER_IMAGE` to `.env`. Existing conflicting Compose-file overrides
need manual review. The worker stays on its explicitly configured image while
updating agents, so it can observe the replacement and record its outcome.
Upgrade the worker manually when its own protocol or fixes require it; do not
bind its image variable to `REEF_AGENT_IMAGE`.

The optional worker opens no network port and uses no container network. It
mounts the dedicated installation read/write, Docker socket, host registry
configuration read-only, and a separate private `agent-updates` volume shared
with the agent. It does not mount the identity volume. Do not remove either
state volume. Its image, mounts and setup remain operator-controlled.

Updates require an idle host: active Compose/build/image/volume/cache operations,
image checks and console sessions block admission. While updating, Reef blocks
new host mutations/checks/consoles. Other hosts and managed application containers
continue running. The worker pulls the fixed official agent repository, verifies
its version/revision labels, checks the existing Compose project and identity
volume, and refuses configuration/container changes detected during download.
Only the `REEF_AGENT_IMAGE` line changes in the private `.env`; its other values
are preserved. The update uses explicit base/update Compose files and recreates
only `reef-agent`, without dependencies, builds, pruning or volume renewal.

A failed download or image validation leaves the current agent and `.env`
unchanged. After recreation begins, an error or missing reconnection is reported
as **unknown**. Inspect the host before retrying: the requested image may already
be configured/running. There is no automatic rollback or replay. A worker restart
also marks unfinished requests unknown rather than repeating them. The update
has a ten-minute deadline, including confirmation that the replacement container
uses the expected image and has reconnected to central Reef with the expected
version/revision. Outcomes persist in `agent-updates` and are visible after page
reload once the agent is connected. During disconnection, Reef keeps the last
known status; an unreachable agent needs host-side inspection.

The first supported setup uses the supplied Compose model, plain unquoted
`COMPOSE_PROJECT_NAME`/`REEF_AGENT_IMAGE` entries, a private regular `.env`,
regular Compose files without symlinked paths, one running `reef-agent` service
and its existing named `agent-state` volume. Custom image repositories, digest
references and alternative identity mounts require manual upgrades.

## First enrollment on a new host

Docker Engine and Compose v2 are required. Prepare a dedicated `reef-agent` folder,
copy the example Compose files and preparation script, and create `.env` from
`examples/agent/.env.example` with the actual HTTPS origin. Copy the trusted public
CA to `ca/ca.crt`; never copy the central CA's private key. Login to the registry
on that host, then run the preparation helper as above.

Once those files are ready, open **Manage hosts**, name the server and create an
enrollment token. It expires after ten minutes and can be redeemed once. Save it
using an editor into `secrets/enrollment-token` with file mode 0600 and directory
mode 0700; avoid shell history/process arguments. Start with the enrollment-only
override:

```sh
docker compose -f compose.yaml -f compose.enrollment.yaml up -d reef-agent
docker compose logs --tail=30 reef-agent
```

When enrolled and connected, recreate with only the base Compose file and remove
the temporary enrollment token. Keep the identity volume:

```sh
docker compose -f compose.yaml up -d --no-deps --force-recreate reef-agent
rm -f secrets/enrollment-token
```

Existing private identities are reused on startup and are not overwritten.
A failed/lost enrollment response needs a new token; revoke any incomplete host
entry first. Do not copy or share identities across machines. At most 64 central
host identities (including revoked) and 16 pending tokens are retained.

## Operations, consoles and failures

The host selector scopes project state, files, registrations, logs and image
checks. Editor drafts are kept independently by host. Consoles retain the host
chosen at creation even when the UI changes hosts; output shares the central
browser stream and retains central per-login/global quotas. The agent also
applies its own per-login and total quotas (defaults 8/16), size, idle and lifetime
limits. Disconnect/reconnect never replays input or opens a new shell.

Commands are purpose-limited to Reef's existing APIs. No arbitrary URL/header,
Docker API or shell-command relay is exposed. HTTP origin/CSRF/login checks remain
central; a host-specific opaque owner scopes remote console sessions without
forwarding browser cookies/passwords/CSRF tokens. Revocation or a replaced agent
connection closes transports and prevents new actions.

A command is delivered at most once within a connection generation. A network
failure can leave a Compose action's outcome unknown: refresh its durable state
before retrying. Accepted operations continue independently of browser/central
connectivity. Files, backups, registrations and operation history live on the
agent; the central server never opens remote filesystem paths. Agent shutdown
waits for operations; keep the example's **46-minute stop grace**. Forced kills
can interrupt work; no automatic rollback or command replay is performed.

Image checks and targeted cleanup use exactly the local engine's rules: only
confirmed managed projects, no pull during checks, and cleanup only after a
successful Apply update for replaced, untagged, unused image IDs. No global prune
or volume deletion. Private-registry availability and healthchecks are evaluated
on the agent's Docker host.

Inventory is reported approximately every five seconds. Contact older than 45
seconds is disconnected; inventory older than 15 seconds or Docker errors is stale.
Reconnection uses capped exponential backoff and jitter. Superseded generations
and reordered inventory/results are rejected. Central restart drops transient
inventory/transports; durable agent state and identities are retained.
