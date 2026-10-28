# Action webhooks

Open **Webhooks** in the navigation. Choose **Discord** or **JSON**, enter the
incoming webhook URL and enable notifications, then confirm **Save settings**.
Use **Send test** to enqueue a harmless test notification. The status line shows
waiting, sent and dropped notifications, the last success and a sanitized delivery
error. Polling refreshes that line without replacing unsaved form fields.

There is one instance-wide destination, independent of the selected Docker host.
Settings use the existing sign-in, origin and CSRF protections. The full URL is
never returned after saving: only its hostname/port is displayed. Leave the URL
empty to keep it; **Remove webhook** disables delivery and removes the saved URL.
Changing settings or disabling/removing the webhook discards waiting deliveries.
An HTTP request already in flight may have reached the previous destination.

Configuration and the bounded queue are stored in the central installation's
private `state_dir/notifications/state.json` (0600 inside a 0700 directory).
URLs can contain private tokens: preserve this state with private backups and do
not publish it. No new environment variables, container mounts or ports are needed.
Unavailable/corrupt notification state disables notifications without stopping
Docker control; repair that private state and restart before reconfiguring.

## Events

Reef sends action metadata and outcomes for:

- Managed projects: save, apply, start, stop, registration and stop managing.
- Images: pull, tag/untag, removal/cleanup and manual/automatic update checks.
- Volumes: creation, removal and cleanup.
- Builds: saved project builds and default-builder cache cleanup.
- Agents: enrollment, rename, revoke and agent updates.
- Consoles: session start, failure/unknown outcome and closure.
- Webhook settings saved while enabled and explicit test notifications.

Asynchronous operations emit a started event and a terminal outcome. Related
events share an operation ID. Batch outcomes include total/success/failure/skipped
counts; a batch notification does not enumerate every selected image or volume.
Registrations and host administration emit completion after success. Calls rejected
before action admission, page reads, sign-ins, keyboard input and console resizing
are outside this event stream. Actions performed directly with Docker outside Reef
are not observed.

Payloads contain a timestamp, host identity/name, scope, action, status, an optional
operation ID and bounded target metadata (project/container name, image reference
or ID, volume name, agent version or selection counts). They exclude Compose/env
file contents and paths, operation/Docker error text, registry credentials, login
sessions, enrollment secrets, application/build logs and console input/output.
A failed result points the operator back to Reef for its detailed logs.

## Discord

Create an incoming webhook for the desired channel in Discord and paste its URL.
Reef accepts official Discord incoming webhook HTTPS URLs, posts a short text
message as Reef, disables mentions and link previews, and requests confirmation
with `wait=true`. No bot account or bot token is required.

See [Discord webhook execution](https://docs.discord.com/developers/resources/webhook#execute-webhook)
and [rate-limit handling](https://docs.discord.com/developers/topics/rate-limits).
Reef respects Retry-After/retry_after and successful-response rate-limit reset
headers, rather than assuming a fixed Discord request quota.

## Generic JSON

A generic receiver gets an HTTP POST with Content-Type application/json and
X-Reef-Event-ID set to `host_id:event_id`. Use that key to deduplicate requests.
Use HTTPS for external receivers; HTTP is also accepted for explicitly configured
local endpoints. Credentials embedded as URL user information are rejected. A
receiver may use a private token in the URL path/query; custom Authorization
headers and HMAC signatures are not part of this version. Redirects are not followed.

```json
{
  "schema": "reef.action.v1",
  "event": {
    "id": "0123456789abcdef0123456789abcdef0123456789abcdef",
    "time": "2026-10-28T10:00:00Z",
    "host_id": "local",
    "host": "Local",
    "scope": "project",
    "action": "apply",
    "status": "completed",
    "operation_id": "abcdef0123456789abcdef0123456789abcdef0123456789",
    "target": "my-project"
  }
}
```

Statuses are started, completed, failed, partial, unknown and closed. Operation ID,
target and count fields can be absent. Event IDs identify individual notifications;
operation IDs relate the start and result of one operation. Receivers should
ignore unfamiliar fields when this schema evolves.

## Remote agents

Upgrade central Reef first, then agents to v0.11.0. Agents negotiate action-events-v1
on each connection. Earlier agents retain their other features but do not send
action notifications. No new enrollment, agent port, webhook URL or webhook token
is needed on the remote host.

Each agent persists a bounded metadata outbox under its own managed state.
Batches travel through authenticated outbound HTTPS to `/agent/v1/events` with
the current connection generation. Central binds events to the registry's host
identity/name and rejects stale/revoked/non-negotiated senders. ACK follows durable
acceptance; repeated batches are deduplicated within the most recent 4096 central
event keys, and acknowledged agent events are removed durably. Only central Reef
contacts Discord or the JSON destination. Browser closure does not stop collection
or delivery. Events from before the current destination's activation/change are
acknowledged without being forwarded; enabling a webhook does not send old backlog.

## Delivery limits

Delivery runs outside action workers with a five-second request deadline, at most
five attempts, and bounded backoff for network errors, HTTP 429 and server failures.
Other HTTP failures are not retried. Each central queue and agent outbox holds at
most 512 events; new events are dropped on overflow. Stale events are not delivered
after 24 hours. The central dropped count covers its delivery/queue failures, not
all remote outbox losses. Disabling the webhook skips collection centrally; remote
ACKs still clear incoming events. No notification failure rolls back a Docker action.

This is a best-effort notification stream, not a complete audit ledger. A crash
between changing Docker state and recording its event can lose that notification.
A crash or timeout after a receiver accepted a message can lead to a duplicate.
A process killed during an operation may leave only its started event. Central
queue/remote outbox state survives normal restarts; operation retries are separate
from webhook retries and are never triggered by webhook delivery.
