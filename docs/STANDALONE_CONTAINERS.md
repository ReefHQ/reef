# Standalone containers

From **Containers**, choose **Manage** on a container without Compose/Swarm
ownership. Local and agents advertising `containers-v1` (**agent 0.22.0+**) support
this feature. Upgrade older agents from Manage hosts first. Assigned viewers can
read the review/result; only admins can change containers.

## Review before acting

The panel refreshes status in place, keeps unchanged details/results and preserves
your scroll position. Loading feedback keeps a reserved space, including during
background refreshes.

The panel pins the host, full container ID and current configuration/state
fingerprint. The server checks Docker again before work. The browser never sends
environment variables, commands or a replacement Docker configuration.

- **Start / Stop / Restart** affect this container only. Restart requires it to be
  running; Stop uses a 20-second Docker stop timeout.
- **Update image** pulls its existing registry reference and recreates it if the
  resolved immutable image changed.
- **Delete container** stops it if necessary and removes its writable layer.
  Docker removal uses neither force nor volume deletion. Named/anonymous volumes,
  bind sources, networks and images are retained.

Updates/deletion need a data/downtime acknowledgement and final confirmation.
Back up important volumes first. The writable container layer is **not copied**
into the replacement; use persistent mounts for data. Rollback cannot undo writes
or database migrations made by the application on shared volumes.

## How an update works

1. Pull before downtime, pin the immutable image ID and verify Linux OS,
   architecture and variant against the old image. An already-current image leaves
   the old container unchanged. Re-read eligibility/configuration after the pull.
2. Create a stopped replacement with the current Docker config and host config.
   Preserve environment, entrypoint/arguments, user, working directory, health
   check, resources/security, restart policy, mount options and network settings.
   Resolve anonymous volumes to existing names. Running containers retain allocated
   host ports, including random published ports. Explicit IPAM addresses and user
   network aliases are retained; automatically allocated addresses can change.
3. Disable the previous container's restart policy, stop it if running, retain it
   under a recovery name and release its network attachments. This prevents both
   copies restarting against the same writable volumes after an engine restart.
4. Give the replacement the original name. A running original gets a running
   replacement; an originally stopped container stays stopped. Check running state
   for three samples, or wait up to 90 seconds for its configured health check.
   This is a startup check, not an application/data compatibility test.
5. Remove the stopped previous container only after success, preserving its
   volumes and image. Before that step, failures attempt bounded rollback: stop
   and remove the identified replacement, restore original name, network settings
   and restart policy, and restart it only if it was previously running.

The old writable layer is kept until removal of the previous container. Success
then permanently discards it. No volume/image prune or old-image cleanup runs.

## Configurations left to their owner

Compose/Swarm containers stay with their project/orchestrator. Reef protects itself
and Docker administration containers mounting the Docker socket. Auto-remove,
paused/transient states, shared container network/PID/IPC namespaces and targets
with dependent namespace/volume consumers are rejected. Dependency inspection is
bounded to 2,000 containers and fails closed if incomplete.

Recreation rejects inherited volumes/legacy links, container-ID files, host or
container network mode, unsupported mount types and non-local/non-bridge networks
(ordinary `none` networking is also supported). Lifecycle actions may remain
available when only recreation is unsupported. Limits are reported before stopping
the old container. Use its original deployment definition for unsupported cases.
Avoid changing the same container concurrently through external Docker tools.

## Interrupted operations

**Containers → Review container operation** shows the latest host operation even
if the original is gone: original/replacement IDs, temporary/recovery names, phase
and result. Polling or reopening the panel never replays work.

Each host journals phases in private `state_dir/container-operation.json` before
Docker writes. It holds bounded metadata, never inspect/configuration secrets.
A restart during work becomes **review required**, without automatic resumption.
Other resource mutations on that host stay blocked until explicit acknowledgement.

Inspect the displayed immutable IDs on the host with Docker/operator tooling.
Check name ownership, running state, mounts, network attachments/IPAM/aliases and
restart policies. Do not run both copies against writable volumes. Resolve the
intended state and remove only a verified unwanted container, retaining volumes.
Then choose **I reviewed the interrupted operation**. This only clears the block;
it performs no Docker action. A storage failure stays blocked until private state
is repaired/reloaded; do not delete the journal blindly.

One host-wide resource mutation runs at a time, excluding Compose, image/volume/
network/cache mutations and agent-update maintenance. Accepted work continues if
the browser closes. Already admitted operations may finish after account revocation;
new requests and response chunks still check current access.

History and configured Discord/JSON webhooks record actual-host container action
metadata. Raw inspect/create data, environment, Docker response bodies and registry
errors never enter browser/relay/event payloads.

Implementation reference: [Docker Engine API](https://docs.docker.com/reference/api/engine/version/v1.51/).
