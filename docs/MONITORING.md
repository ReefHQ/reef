# Monitoring alerts

[← Back to Reef](../README.md) · [Webhooks](WEBHOOKS.md) · [Agent setup](AGENT_SETUP.md)

Available from **v0.16.0**, disabled by default. Open **Alerts → Monitoring policy**
to enable background monitoring and configure delays. Settings apply to the
whole instance; the host/filter controls only change the displayed results.
Monitoring never starts, stops, recreates or repairs Docker resources.

## What triggers an incident?

| Condition | Default opening rule | Recovery rule |
| --- | --- | --- |
| Agent disconnected | No authenticated inventory contact for 120 seconds | Connected continuously for 60 seconds |
| Unhealthy container | Docker health status stays unhealthy for 120 seconds | Docker reports healthy continuously for 60 seconds |
| Restart loop | Restart counter increases by at least 3 within 300 seconds | Running, no observed restarts left in that window, then stable for 60 seconds |

Agent registry connections become disconnected after 45 seconds; the configured
monitoring delay is measured since last contact, rather than added to that delay.
A newly enrolled host that never connects is eligible only after the startup/
enablement grace period. Container monitoring covers running/restarting containers
on the host, whether or not their project is managed by Reef. Containers without
healthchecks cannot trigger an unhealthy alert. Stopped/paused containers are not
reported as failures by these rules.

Threshold ranges: disconnect 60–3600 seconds, unhealthy 30–3600 seconds,
2–100 restarts within 60–3600 seconds, recovery 30–600 seconds.
Saving a changed policy retires existing incidents and starts new observation
baselines. It never announces that an application recovered. Disabling monitoring
stops new collection and evaluation; already accepted external webhook deliveries
cannot be withdrawn.

## Collection and coverage

Local and compatible agents use the same read-only Docker inspector. A host
collects up to 1,000 containers, with four concurrent inspections and an overall
eight-second deadline, then waits 30 seconds before the next sample. The first
sample is requested on enablement (Local may wait for its next 30-second check).
No browser needs to stay open. Each inspection response is limited to 1 MiB.
Only container ID/name, state, health status and restart count leave the inspector;
healthcheck output, commands, errors, environment and config are discarded.
The Docker fields come from the [container inspection API](https://docs.docker.com/reference/api/engine/version/v1.56/#tag/Container/operation/ContainerInspect).

Agent collection is independent of five-second inventory heartbeats. It starts
only when central replies that monitoring is enabled. Snapshot sequence and
connection generation protect freshness; repeated snapshots do not advance timers.
Agents need **monitoring-v1** (v0.16.0) for containers. Older agents still permit
connection monitoring and show **Upgrade required** for container coverage.
Upgrade central before agents, preserving their existing identity/CA/mounts.
No new port, secret, registration or updater worker is required.

Central evaluates host availability every five seconds and accepts at most one
fresh container sample per host every 20 seconds. It uses its own receipt times,
not an agent’s wall clock. An observation gap over 90 seconds resets continuity
and counter baselines; it never counts as sustained bad/healthy time. The first
restart counter is a baseline, so prior restarts are not retroactively counted.
Counter resets also establish a new baseline.

**Collection coverage** distinguishes unavailable Docker, partial inspection,
disconnection, missing capability, waiting and maintenance. Missing inspection is
unknown, never a healthy zero. Inventories above 1,000 containers are explicitly
unavailable for container monitoring; no silently selected subset is considered
healthy. Connection monitoring continues independently. Collection gaps can miss
short incidents; this is periodic observation, not complete event capture.

## Maintenance and recovery

Admitted Reef mutations suspend observations for the host, resetting pending
health/restart continuity but preserving firing incidents. Samples overlapping a
mutation remain marked as maintenance even if the operation ends before the
heartbeat arrives. Following an agent-update request from Reef, a ten-minute
central maintenance hold also suppresses expected disconnection alarms. The hold
expires automatically and is persisted when storage is available; a failed
persistence attempt keeps it in memory and does not block the update.

The suppression is conservative and host-wide. External Docker commands and
commands entered in consoles are not automatically recognized as maintenance.
No indefinite maintenance mode is introduced.

Removal, stopping/pausing, healthcheck removal, agent revocation and policy changes
**retire** affected incidents instead of claiming recovery. A fresh healthy sample
must meet the recovery rule before an incident becomes **Resolved**. An existing
Firing incident remains its last recorded state while collection is unknown.
After central restart, incidents/outbox survive, but observation timers and
counter baselines restart; disconnection gets a full enablement/startup grace.
No Docker operation is replayed.

## Persistence and notifications

Private central state: `state_dir/monitoring/state.json`, directory 0700 and file
0600. It contains policy, incidents, maintenance holds and up to 256 transition
events awaiting acceptance by central sinks. State is bounded to 4 MiB and
2,000 incidents. Resolved/retired incidents expire after 30 days and oldest closed
records can be removed to admit new incidents. Firing incidents are retained;
if capacity is full, further transitions wait and the UI reports the limit.
Restart-counter samples/baselines are bounded and kept in memory only.

Writes are private, atomic and synced. Symlinks, public modes, corrupt/schema-
incompatible data, excessive sizes and external edits are rejected without
silently resetting/overwriting existing state. Startup failure disables this
optional subsystem, leaving Docker actions available. Runtime failures retain
last-good persisted incident state and expose a warning. Repair private state
while Reef is stopped, then restart; never edit the live state file.

Each incident produces one opening and one observed recovery, without repeated
reminders. A new incident after resolution gets a new ID. History and webhook
storage accept each transition idempotently; a failed sink keeps its transition
pending, while a successful sink deduplicates retries. Monitoring does not replay
Docker commands in response to a storage or delivery failure.

Webhook events reuse `reef.action.v1` with scope `monitor`, action `disconnected`,
`unhealthy` or `restart_loop`; `failed` denotes opening, `completed` denotes
recovery. Both share the incident `operation_id`. Discord spells out the transition
and disables mentions. Notification delivery still has its existing bounded queue,
retry/expiry policy; the Alerts pending counter measures central acceptance,
not successful external delivery. Use Webhooks to inspect actual delivery status.
When webhooks are disabled, History still records the transitions; enabling a
webhook later does not send older events.
