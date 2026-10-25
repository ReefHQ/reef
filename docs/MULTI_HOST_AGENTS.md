# Multi-host Reef through agents

Status: agreed design. The inventory slice is being implemented on
`feat/multi-host-inventory`; see [operator setup](AGENT_SETUP.md) for actual flags and
limits. Later logs, console and Compose increments remain proposals. No remote
production access or deployment has been performed.

## Product behavior

One Reef UI and one browser login manage multiple Docker hosts. The current
installation remains the **Local** host and works without agents. A host selector
scopes Containers and Projects; v1 has no combined all-host view. The selected
host name stays visible in details, logs, console tabs and operation confirmations.
Switching hosts preserves existing console connections and editor buffers; any
discard prompt must still protect unsaved edits.

A **Manage hosts** dialog lists name, connection status, last contact, last successful
inventory and agent version. An authenticated user can add, rename or revoke an
agent. Revocation disconnects it immediately and rejects its credentials, without
stopping its Docker containers. Removing an offline host never deletes files or
containers remotely. Host names are display labels; immutable server-assigned IDs
are used for routing and storage.

The first increment offers inventory only. Remote project groups are view-only;
management, logs and console actions remain unavailable until their capability is
implemented on both sides. A remote container ID must never fall back to Local.

## Responsibilities and current code

The central Reef owns browser authentication, host identities, agent enrollment,
connection health and cached inventory. Browsers contact only Reef. An agent owns
its local Docker socket, approved project roots and, later, its Compose execution,
file validation, backups and durable operation history. Remote filesystem paths
are never opened by the central server or inferred from its installation folder.

Today `Server.discovery` in `internal/app/server.go` uses a single local
`docker.Lister`. Inventory can first be selected through a host registry with
local and remote implementations. The `Manager` in `internal/app/engine.go` owns
local project paths and executions; it stays local in the inventory increment.
Remote Compose needs an explicit host operation interface in a later increment.
Logs and console currently use the local Docker client and must also acquire
explicit host routing before they are enabled remotely.

Use a separate `cmd/reef-agent` entry point, sharing inventory types and the
existing `internal/docker` client. Keep the Go standard-library approach. The
agent starts with an explicit central URL, Docker socket and private state path.
Only its own Docker socket and state volume are needed for inventory. Mounting a
socket read-only does not make Docker API access read-only: the implementation
must expose only the operations allowed by its capabilities.

## Enrollment and authentication

1. An authenticated central UI action, protected by origin/CSRF checks, creates
   a cryptographically random one-use enrollment token, valid for 10 minutes.
   Reef shows it once; only its hash is persisted.
2. The operator supplies the token to the agent through a private file or secret
   mount. Installation examples must avoid embedding it in shell history, process
   arguments, image layers or Compose source committed to Git.
3. The agent enrolls over HTTPS with normal server certificate verification.
   Private CAs may be explicitly configured; certificate checking is never disabled.
4. Reef atomically consumes the token and creates a host identity with a distinct
   random 256-bit bearer credential. Token redemption is serialized and rate
   limited. A lost enrollment response requires a new token; failed enrollment
   cannot silently create duplicate hosts.
5. The agent saves its credential atomically with private permissions. Reef stores
   its hash and compares credentials securely. Revocation persists across restart.
   Re-enrollment uses a fresh credential and explicit operator action.

Agent credentials authorize only that host's protocol endpoints. They cannot
access the browser UI, issue enrollment tokens or publish another host's inventory.
Browser passwords/cookies are never forwarded to agents. Protocol authentication
is separate from browser CSRF: machine endpoints reject browser cookies as their
credential. Bound requests, rate limits and secret-free diagnostics apply there.

HTTPS terminates at the central deployment or a trusted reverse proxy; authorization
remains in Reef. No agent requires an incoming network port or a Docker TCP API.
The existing loopback local installation continues to work independently; remote
agents require configuring a reachable HTTPS address deliberately.

## First protocol increment

Use outbound HTTPS requests initiated by the agent. Enrollment and inventory
submission use versioned, purpose-specific JSON endpoints. No generic HTTP/Docker
proxy or arbitrary command RPC is exposed. Future command delivery can use a
separate outbound long-poll request without changing the identity model.

At startup, authenticate and negotiate protocol version/capabilities before
publishing inventory. Only one connection generation per host may publish;
a replaced generation's late results are rejected. Poll Docker every 5 seconds
without overlapping reads; report inventory or a bounded inventory error. Contact
time is recorded by the central server, not trusted from the agent's clock.

Initial proposed defaults:

- Heartbeat/contact every 15 seconds; mark disconnected after 45 seconds without
  authenticated contact. Heartbeats continue when Docker itself is unavailable.
- Inventory older than 15 seconds is stale even when the agent is connected.
  Docker errors immediately mark the cached inventory stale and show the error.
- Retry with exponential backoff, jitter and a 30-second cap. Do not replay data
  from an earlier connection generation after reconnecting.
- Limit each inventory payload to 16 MiB and 10,000 containers; validate field
  lengths, exact container IDs and allowed states before replacing the snapshot.
  Reject an entire invalid/oversized snapshot, retaining the previous valid one
  as stale. Do not truncate silently or replace failure with an empty inventory.
- Exchange protocol version and supported capabilities explicitly. Unsupported
  versions show a compatibility error and cannot enable actions based only on
  matching application SemVer. Server-side policy intersects agent capabilities.

These are protocol defaults, not user-configurable runtime settings. Host-disconnected, Docker-unavailable and incompatible-agent
states must be distinguishable in the UI.

Persist host metadata and credential hashes atomically under a private
`state_dir/agents/` directory, separate from browser authentication and existing
project history. Cache snapshots in bounded memory for v1; after a central restart,
hosts have no inventory until a fresh submission. Never persist raw agent tokens
or Docker environment variables. The agent's private state retains its own identity
across its container restart. Reusing that identity on two hosts is unsupported
and must produce a visible connection-replacement diagnostic.

## Host isolation and later capabilities

Use `(host_id, container_id)` and `(host_id, project_id)` as resource identities.
Carry the host through API requests, selections, UI caches, editor buffers and
console ownership. Same-named Compose projects and identical fixture container
IDs on different hosts must remain independent. Preserve existing local IDs and
state files; no migration of existing registrations is needed for inventory.

Increments after inventory:

1. Remote stdout/stderr logs with the existing filters and limits, request
   cancellation and per-host routing. A disconnected agent leaves the last output
   visible with an explicit stale/error state.
2. Remote console transport, explicit Connect, existing session ownership,
   input/output limits and quotas. Retain the current per-login and central total
   limits, and add a per-host limit. Disconnect closes streams; reconnect never
   replays input or automatically reopens a shell. Output backpressure must not
   block inventory or other hosts.
3. Remote project registration and Compose editing/actions. The agent enforces
   its own explicitly configured roots, validates paths, serializes project
   operations and persists history locally. The central server cannot broaden
   those roots. Every action carries an operation ID; retries query that ID
   rather than executing it again. A network loss means outcome unknown until
   reconciled, not automatic failure or permission to retry. Accepted operations
   follow the existing worker behavior independently of browser connectivity.

A remote agent is trusted to report its own host, but its strings, paths and
responses remain untrusted input. Render metadata as text and apply bounded
validation. Compromise of an agent must not confer another host's credentials or
central browser administration.

## Implementation order and acceptance

First implement host identities and local inventory routing with unchanged local
behavior. Then add enrollment, credential persistence/revocation and the
inventory-only agent. Finally expose Hosts and the selector, stale snapshots,
error states and disabled unsupported actions. Deliver the complete inventory
slice through a dedicated feature branch/MR before starting remote logs/consoles.

Required checks for that slice:

- Existing local inventory, login, logs, consoles and Compose workflows still pass.
- Two disposable hosts with colliding resource names/IDs stay isolated.
- Expired/reused enrollment tokens, revoked/wrong-host credentials and incompatible
  protocol versions grant no access; concurrent redemption succeeds at most once.
- Restarting central Reef or the agent preserves identity and revocation, without
  making stale inventory look fresh. Docker failure differs from agent loss.
- Delayed/reordered results, reconnect/replacement and oversized or malformed
  inventories cannot overwrite a newer valid snapshot or exhaust memory.
- Desktop/mobile UI clearly identifies the selected host and never routes a
  remote action to the local engine. No actions run on production for validation.

The inventory feature starts development version `0.3.0`, recorded in `VERSION`
and CHANGELOG. No matching release tag has been created. No automatic upgrades, fleet rollout or production/NAS deployment is
included in this proposal.
