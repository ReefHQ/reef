# Action history

Available centrally from v0.15.0. This is a bounded metadata journal, independent
of whether external webhooks are configured/enabled. It complements project logs
and resource operation reports; it does not copy their command output or errors.

## Recorded metadata

Project, image, volume, network, build, agent and console lifecycle events reuse
the existing typed notification hooks. Webhook configuration/test actions record
constant names, without destinations or tokens. Only host identity/name, category,
action, target name/ID, event time, operation ID, status and aggregate counts are
recorded. Rejected requests before operation admission and login attempts are not
part of this action journal. A webhook test records queue acceptance, not delivery;
its note points to Webhooks for delivery status. Batch operations record aggregate metadata, not per-item command output.
Agent enrollment/rename/revoke are central administration actions and are recorded
on Local, with the affected agent's name as target. Host names are captured when
events are received, so older names can remain after a rename.

Starts and outcomes are grouped by host + category + action + operation ID. Events
without an operation ID remain separate actions. Late starts cannot overwrite a
terminal outcome. Started means no final event has been recorded, not proof of an
active worker. On central restart, persisted Local starts without an outcome are
shown as Unknown; completed/partial/failed/closed outcomes are preserved. Remote
starts are not changed merely because central restarted or an agent disconnected.
There is no user attribution under the current shared-password login. The future
user/permission feature must deliberately extend/migrate this event model.

## Persistence and retention

Central private state: `state_dir/history/actions.json`, mode 0600 in a 0700
folder. No additional host bind or public port is required. State is schema-checked
and bounded to 8 MiB, 2000 events and 4096 recent host/event deduplication keys.
Events expire 30 days after central receipt; records whose host event time is
already older than 30 days on arrival are ignored. Retention is enforced on load,
accepted writes, history reads and hourly maintenance. Count/age retention never
deletes Docker resources, project files or other operation logs.

Local emission uses a nonblocking 256-event queue and background batches of up to
32. Graceful shutdown drains that queue after HTTP/Docker workers finish. A crash
can lose queued metadata. Queue overflow or local write failure drops metadata
and increments a visible loss counter; the counter is durably captured on the
next successful write. History failure never triggers a Docker retry or changes
its result. Counters can themselves be lost if no successful persistence follows
a failure. This journal is not a complete audit ledger.

Writes use private temporary files, file sync, atomic rename and directory sync.
Symlinks, unsafe modes, oversized/corrupt state and external file changes are
rejected; corrupt files are left intact. History remains optional if it cannot
open, and the UI reports unavailable persistence. Runtime write failures keep
last-good metadata readable and expose an error. Do not edit the live history
file: repair/restore its private state while Reef is stopped, then restart.

## Remote ingestion and webhook independence

Existing `action-events-v1` agents (v0.11.0+) send bounded outbox batches through
the authenticated outbound connection. No new capability is required. Central
binds every event to the authenticated registry host/name and rejects stale
credentials/generations, revoked hosts and remote webhook-setting events.

History and webhook acceptance are independent and idempotent. A ready store's
failed acceptance returns a retryable error even if the other store succeeded;
retries deduplicate in both stores and never repeat the underlying Docker action.
History can receive events when notification storage is unavailable, and webhooks
can continue when history was unavailable at startup. If a store was disabled by
an opening failure, it cannot recover events already acknowledged by the other
store. Existing agent outbox size/availability limits still apply, so remote
metadata can also be delayed or incomplete. The UI reports central history health
and local queue loss; it does not claim to count every remote gap.

Webhook activation cutoffs, delivery configuration and retry policy are unchanged.
Recording all actions in history does not send older events to external webhooks.
Previously acknowledged metadata is not backfilled, and existing project log files
are never scanned/imported into this journal.

## Read API and interface

Authenticated, read-only `GET /api/history` always reads central state. The route
is not part of the agent command allowlist. `host=local`, an agent ID or `all`,
category, status and a bounded search filter the grouped results. Pages contain
50 actions. A string sequence cursor freezes the accepted event boundary for
older pages; ongoing retention can still remove old entries. Refresh returns to
the latest page. Records sort by central receipt order; Details also displays the
host's original event time, which can differ due to agent clock differences.

The UI fetches only while its view/tab is visible, aborts outstanding reads when
leaving/changing hosts, ignores late responses and preserves controls during
updates. Browser preferences retain only view/filter/page/cursor/scroll, not the
action contents. No UI endpoint edits or deletes journal data or executes actions.
