# Agent diagnostics 🔎

[← Back to Reef](../README.md) · [Agent setup](AGENT_SETUP.md) · [Accounts](USERS.md)

Available from **central Reef v0.20.0**. Open **Manage hosts → Diagnostics** beside
an agent. Viewers and admins can read diagnostics for assigned hosts, including
revoked hosts still in their allowed scope. A different host's data is never used
as a fallback.

The panel reads **central observations already available**. It does not ping the
host, inspect files, execute Docker commands, repair configuration or replay an
operation. No new incoming port, enrollment or agent upgrade is required merely
to display diagnostics. Older agents show the capabilities they actually reported.

## Read the signals

| Signal | Meaning |
| --- | --- |
| **Last accepted connection** | The latest authenticated, protocol-compatible handshake seen in this central process. |
| **Last contact** | The latest accepted handshake or inventory request. Diagnostic reads and command errors do not refresh this timestamp. |
| **Last inventory attempt** | An authenticated request for the current generation passed the initial identity/sequence checks; its payload may still have been rejected. |
| **Last valid inventory** | The latest complete accepted Docker snapshot. A failed refresh retains the previous snapshot. |
| **Agent version** | The last accepted stable version label, or a development label. Arbitrary/unrecognized labels are withheld. |
| **Negotiated features** | Capabilities advertised for that connection. A version match alone does not establish support or prove execution will succeed. |

Reef considers contact fresh for **45 seconds**, and inventory current for
**15 seconds**, using central receipt time. A newly accepted connection must supply
its own first inventory; a recent cached inventory from the previous connection
is explicitly stale until then. Offline/revoked capability reports are historical,
not current compatibility conclusions.

Agent versions can be older, newer or equal to central without a protocol error.
Missing capability reports identify features that may need an upgrade or setup.
An **incompatible protocol** is a separate, authenticated handshake rejection.
Non-stable builds may not support stable-version comparison.

## Work through a problem

| What Reef shows | What to check |
| --- | --- |
| **No contact observed** | Start the agent and inspect its local logs. Check its configured Reef address, routing/NAT, trusted CA and enrollment. This means no contact since central startup, not proof it has never connected. |
| **Disconnected** | Check the agent service and its outbound HTTPS connection. Central cannot distinguish every DNS, TLS, router or authentication failure that occurs before an authenticated request reaches it. |
| **Connected, waiting for inventory** | Allow the next heartbeat. If it does not arrive, check agent load and local Docker access. |
| **Docker inventory unavailable** | The agent reports an inventory failure. Check Docker Engine, its configured socket mount and access permissions on that host. Connectivity and Docker availability are separate signals. |
| **Inventory rejected / exceeds limits** | Check compatible agent versions and host inventory size. Reef keeps its previous valid snapshot instead of accepting partial or invalid data. |
| **Feature not reported** | Upgrade a compatible agent or complete that feature's optional setup. Updates from Reef need the separate agent-updater setup; a missing report is not automatically a network failure. |
| **Connection replaced** | Normal during an update or reconnect. If this happens repeatedly without an expected update, check that the private agent identity was not copied to another host. |
| **Remote response interrupted** | Read the host's operation/resource report before retrying a change. A lost response or reconnection does not prove that the operation failed. |
| **Revoked** | Access was deliberately removed. If access is intended again, enroll with a new identity; revoked credentials cannot reconnect. |

## Recent observations, not private logs

The panel retains up to **20 observations per host in memory**, newest first.
Consecutive identical observations are coalesced with a count and first/last time.
These include accepted/replaced connections, inventory failures/recovery, rejected
responses and communication failures observed by ordinary Reef requests.

Late failures from an old connection cannot contaminate a newer connection.
Normal request cancellation, such as closing a log view, is not presented as a
communication fault. Successful reconnection/inventory recovery does not change
an old uncertain mutation into a Success or Failure.

Only fixed classifications and hints are retained. The panel does not expose
credentials, enrollment tokens, private identities, generation nonces, registry
credentials, raw HTTP bodies, URLs, socket paths, console content or application
logs. Arbitrary agent error text is replaced with a fixed message. Host names,
public host IDs and normal operational timestamps remain visible to authorized
users. This is troubleshooting metadata, not a complete security audit.

Observations, cached versions and last-contact metadata reset with the central
process. Revocation itself remains persistent. Within the same central run, a
revoked host keeps its last safe diagnostic metadata while its operational cache
and connection are removed. Nothing is written to a new diagnostic log file.

Automatic refresh reads central metadata every five seconds only while the panel
is visible. Pause it with **Refresh automatically**, or use **Refresh** manually.
The panel preserves its scroll position and marks a previous snapshot stale when
refresh fails. Close remains visible while scrolling; diagnostics do not refresh
the whole Reef interface or trigger automatic remediation.
