# Inventory agents

The first multi-host increment is inventory-only. No remote exec, application
logs, project file reads or Compose operations are supported. Local Reef remains
fully functional with `agents_enabled` omitted or false.

## Central setup

Configure a reachable HTTPS origin with a valid certificate (or an explicitly
trusted private CA). Reef continues listening on HTTP behind your own reverse
proxy. Set `agents_enabled: true` and, for browser access through HTTPS,
`secure_cookie: true` in its configuration, then restart Reef. The proxy must
forward `/agent/v1/` POST requests and allow payloads up to 16 MiB. Keep the
central HTTP listener private; the agent refuses HTTP URLs and redirects.
No Docker TCP endpoint is used. This feature does not configure a reverse proxy,
TLS certificates, firewall rules or NAS deployment automatically.

Open **Manage hosts**, enter a display name and create an enrollment token. Save
it to a private file with mode 0600 using your editor/secret manager; avoid putting
the token in shell history. It expires in ten minutes and can be redeemed once.
Closing the dialog clears the displayed token. A host appears after redemption.
A failed/lost enrollment response requires a new token; revoke any incomplete
host entry first. At most 64 host identities (including revoked entries) and 16
pending enrollment tokens are retained.

## Agent installation

Build the separate agent image from this checkout:

```sh
docker build --target agent -t reef-agent:0.3.3 .
```

Example Compose service, replacing the HTTPS origin and existing secret-file
path. This is an installation example, not an instruction to contact production.

```yaml
services:
  reef-agent:
    image: reef-agent:0.3.3
    restart: unless-stopped
    command:
      - -server
      - https://reef.example.test
      - -state-dir
      - /var/lib/reef-agent/state
      - -enrollment-file
      - /run/secrets/reef-enrollment
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - agent-state:/var/lib/reef-agent
    secrets:
      - reef-enrollment
secrets:
  reef-enrollment:
    file: /ABSOLUTE/PRIVATE/PATH/enrollment-token
volumes:
  agent-state:
```

The agent requires a private 0700 state directory and 0600 regular credential
files. Check the secret's permissions as mounted by your Compose installation;
it must not be group/world-readable. Socket access grants powerful Docker access,
even though the agent implements inventory calls only. Only the Docker socket,
identity volume and temporary enrollment secret are needed; no project folders
or incoming published port are required.

After successful enrollment, remove the enrollment-file flag and secret mount
from the service, then recreate it with the same identity volume. On subsequent
starts the token is ignored when an identity already exists. Do not copy an
identity volume to another machine or share it between agents. A process lock
prevents two agents using the same state directory. Keep this private volume
across rebuilds; changing the central URL requires explicit re-enrollment.

For a private CA, mount its public certificate bundle read-only and pass
`-ca-file /path/to/ca.pem`. Certificate verification stays enabled. Native Linux
and macOS binaries can also be built from `./cmd/reef-agent`; runtime flags are
listed by `reef-agent -help`.

## Status and recovery

The agent publishes an inventory approximately every five seconds, retaining
only container ID, name, image, state/status and Compose project/service. Docker
environments, commands, arbitrary labels and filesystem paths are not sent.
Protocol version 1 supports inventory only, independently of application SemVer.
Locally built binaries report `dev`; Docker builds report `VERSION`.

Authenticated contact older than 45 seconds means disconnected. Inventory older
than 15 seconds, or a reported Docker failure, is stale even if the agent is
reachable. Reef retains the last successful snapshot in memory and labels it
stale; central restart clears cached snapshots until fresh reports arrive.
Agents reconnect with capped exponential backoff and jitter. Each connection has
a new generation; superseded/reordered updates are rejected. Each payload is
limited to 16 MiB/10,000 containers, with a 64 MiB combined central cache budget.
Oversized inventories report an error instead of silently truncating.

Rename changes only the display name. **Revoke** durably rejects the identity,
clears its cached inventory and disconnects it logically; the remote process may
keep retrying, and its Docker containers keep running. Reinstall/re-enroll with a
new private identity volume and a new token when needed. No remote data deletion,
automatic upgrade or command replay occurs.

The central registry is private `state_dir/agents/registry.json`, separate from
browser sessions and project history. It contains credential/token hashes rather
than raw agent secrets. Back up it and the agent identity volumes as secrets;
do not manually edit registry files while Reef is running. Invalid or unsafe
registry state fails startup instead of silently forgetting enrolled hosts.
