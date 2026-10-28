# Multi-host Reef through agents

Status: full host functionality implemented for development version 0.9.0.
See [agent setup](AGENT_SETUP.md) for operator configuration and upgrade order.
Existing inventory agents remain compatible with the central service.

## Product and ownership

One Reef interface and browser login manage Local and remote Docker hosts.
Selecting a host scopes inventory, project state, files, registration, operations,
logs and image checks. Remote project operations reuse the existing manager,
validation, revision hashes, backups, locks and persistence on the agent.
Remote paths are never opened by the central filesystem. The operator configures
and mounts allowed roots on each agent; the central UI cannot expand those roots.

Editor buffers are held per host, including same-named/same-ID projects. Late
responses are guarded by an editor epoch. Console tabs keep the host captured at
creation while users navigate elsewhere. The host is visible in the selector,
console labels, log titles and operation confirmations. Existing local IDs and
state require no migration.

## Identity and negotiated capabilities

Agents enroll once through a short-lived one-use token and maintain their own
private identity volume. Central stores hashes under state_dir/agents rather
than raw machine secrets. TLS verification, origin/cookie separation, generation
isolation, revocation and inventory limits from protocol 1 remain unchanged.

Volume inventory/create/delete/cleanup use the shared host backend and require
`volumes-v1` from v0.8.0. The capability resets on reconnect; a previous agent
keeps its other features and cannot route volume operations to Local.

A connection explicitly advertises `control-v1` and, for the Images catalog,
`images-v1`. Image inventory, pull, tagging and reviewed cleanup use the same
backend locally and remotely. Older control agents keep their existing features;
Images requires upgrading the agent. Capability state is ephemeral
and reset by reconnect. An old agent's version string never enables commands.
Upgrade central first, then the agent image/configuration without replacing its
volume, URL or enrollment identity. Revocation prevents further commands and
closes relayed sessions; already accepted Compose operations are not undone.

Builds and cache operations require `builds-v1` from v0.9.0. The shared manager
executes saved Compose builds on the agent, with local revision checks, env
redaction, operation locks and persistent summary/log files. Cache reads combine
raw engine metadata with the default Buildx builder's eligibility; confirmed
cleanup sends exact cache IDs, never arbitrary filters or shell commands.

## Outbound command transport

The agent uses authenticated POST long polling to `/agent/v1/commands` with its
current generation, alongside its five-second inventory loop. Polls wait at most
three seconds; the broker wakes on new work. Commands carry immutable IDs,
host-specific opaque console owners, expiries and a fixed method/path allowlist
of existing Reef APIs. `/login`, `/logout`, host administration, arbitrary URLs,
Docker endpoints and shell-command RPC are not permitted. The backend constructs
its own trusted internal request; browser cookies, passwords and CSRF tokens are
not forwarded.

Results are small sequenced frames submitted to `/agent/v1/results`. Each frame
is bound to host, generation and command ID. Unknown/cancelled IDs are ignored;
invalid/out-of-order frames are rejected. The central broker exposes bounded
response readers, sanitizes response content types/headers and cancels individual
requests under backpressure. Batches respect wire and payload limits. Central
HTTP origin/CSRF/login checks run before any remote dispatch.

Commands are dispatched once per generation. Lost delivery/acknowledgement can
mean outcome unknown; the UI must query durable project state rather than replay
mutations. The agent tracks completed IDs within the generation. Reconnection
replaces old jobs and cancels shells; no input or command replay occurs.
Accepted manager workers continue even if the browser/central connection closes.

Transport bounds: 24 concurrent requests per host, 64 globally, 32 MiB total
queued command wire budget, four commands/seven MiB per poll, 16 KiB frame chunks,
16 frames per result batch, bounded 16-frame response queues. Ordinary responses
are capped at 16 MiB; console wire responses at 24 MiB on top of the existing
16 MiB raw output limit. Agent uploads are bounded, jobs have deadlines and
generation buffers are discarded on disconnect. Inventory has its separate
16 MiB/10,000-container payload and 64 MiB combined central cache limits.

## Console multiplexing

Central owns the browser's one NDJSON panel connection and enforces existing
per-login/global quotas across Local and remote hosts. A remote Docker adapter
opens the same console handler on the agent through the outbound relay, forwards
bounded input/resize requests and translates output into the existing central
stream. Console IDs are random and scoped to the central owner and remote owner.
The agent also enforces its own default eight-per-owner/sixteen-total limits.
Its normal running-container checks, allowed shells, inherited user, idle timeout,
30-minute lifetime and output cap apply without a second implementation.
Browser close/logout/expiry, revocation and generation replacement close the
corresponding relay and Docker terminal. Output cannot block inventory.

## Project state and images

The agent's backend stores registrations, latest/history operations and image
checks below its private managed state directory, separately from identity.
It uses the local manager for Compose validation/save/pull/down/up and targeted
old-image cleanup, so failed validation/pull/recreation has the same behavior as
Local. Image availability is checked against the selected agent's deployed image
platform and its own registry credentials. No automatic application upgrade,
rollback, global prune or volume removal is added by the relay.

A project operation is serialized with checks/unregistration. File revision
checks and symlink/root rules execute on the agent. Optional explicit config can
restrict roots/projects and console limits. The installation-parent default and
identical-path bind follow Local's existing authorization rule. Graceful agent
shutdown waits for accepted workers; preserve a 46-minute Compose stop grace.

## Validation requirements

Local workflows continue to pass. Tests cover verified HTTPS command transport,
capability negotiation, old agents, host/generation isolation, cancellation and
no redelivery, allowlists and bounds, remote state/actions/logs/consoles, owner
checks, durable operation behavior and host-separated editor buffers. Same IDs on
two hosts and delayed responses cannot cross hosts. Validation uses owned local
fixtures and fake engines; production hosts are upgraded only by the operator.
