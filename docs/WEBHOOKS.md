# Action webhooks

Open **Webhooks** in the navigation. Choose **Discord** or **JSON**, enter the
incoming webhook URL, choose your notification categories and enable notifications,
then confirm **Save settings**.
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

## Notification categories

In Webhooks, tick the categories you want to receive: **Containers, Projects,
Images, Volumes, Networks, Builds, Agents, Consoles, Schedules, Alerts & monitoring,
and Webhook settings**. Use **Select all** or **Select none** for a quick starting
point, then confirm **Save settings**. The selected count updates as you choose.
Polling refreshes delivery status without replacing your category draft.

Existing installations default to all categories. Selecting all also includes
future categories added by Reef; a selected subset excludes newly added categories.
Selecting none pauses automatic webhook notifications while keeping the saved
destination and enable switch. **Send test** always checks an enabled saved
destination, regardless of the category selection. Selecting Webhook settings
controls automatic configuration notifications; it does not suppress a manual test.

Preferences apply instance-wide to Local and remote hosts, for Discord and JSON.
They filter delivery by event category, including both started and terminal results;
History retains every action it normally records. Agents continue forwarding their
metadata for History. Central Reef durably acknowledges/deduplicates excluded
remote events, so they do not retry or later reappear when categories are enabled.
Intentional exclusions do not increase the dropped/failure count or fill the queue.
Changing preferences uses the existing confirmation and discards waiting deliveries;
an in-flight delivery may already have reached the receiver. Changes apply to new
events, without replaying historical events.

Selections persist with the existing private notification settings and survive
restarts. Legacy missing/null categories mean all; an explicit empty list means
none. Invalid/duplicate/oversized selections are rejected. No new mount, port,
enrollment or agent capability is needed; upgrade central Reef to0.30.0.

For API clients, GET `/api/webhook` returns selected `categories` and labeled
`category_options`. POST save uses `categories_present=yes` and repeated
`categories=project&categories=image` fields. The flag with no category values
means none; omitting it preserves the saved selection, including for older clients
or webhook removal. Existing origin/CSRF and global-admin restrictions apply.

## Events

Reef sends action metadata and outcomes for:

- Managed projects: save, apply, start, restart, stop, registration and stop managing.
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
Reef accepts official Discord incoming webhook HTTPS URLs, posts a compact embed card as Reef and requests confirmation with `wait=true`.
Cards use friendly action titles, separate Status/Host/Target fields, native Discord
timestamps and a full operation reference in the footer when available. Colors are
teal for in progress, green for completed/recovered, red for failures/incidents,
amber for partial or unknown outcomes and gray for closed sessions. Text labels
always identify the result as well. Batch cards show item totals and terminal
success/failure/skipped counts; a started card does not show premature zero results.
Alerts distinguish Incident opened from Recovered. Failed/unknown results point
back to Reef without including private Docker errors or logs.

Mentions remain disabled. Host and target are bounded literal code text; embedded
backticks render as a similar typographic mark and @ is separated by a zero-width
space, so metadata cannot introduce Markdown or mentions. No external image,
thumbnail, action URL or top-level message content is added. The old SUPPRESS_EMBEDS
flag is removed so these intentional cards are visible. Generic JSON carries this same card in its additive `presentation` block.
Only central Reef needs this presentation upgrade; agents, saved settings, event
selection, queue/retries and notification frequency stay unchanged. No bot account or bot token is required.

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
  },
  "presentation": {
    "author": {
      "name": "Reef · Projects"
    },
    "title": "Project update",
    "description": "The action completed successfully.",
    "color": 3120760,
    "fields": [
      {
        "name": "Status",
        "value": "Completed",
        "inline": true
      },
      {
        "name": "Host",
        "value": "`Local`",
        "inline": true
      },
      {
        "name": "Target",
        "value": "`my-project`",
        "inline": false
      }
    ],
    "footer": {
      "text": "Operation · abcdef0123456789abcdef0123456789abcdef0123456789"
    },
    "timestamp": "2026-10-28T10:00:00Z"
  }
}
```

From0.29.0, `presentation` is produced by the same formatter as Discord's embed.
Its title, description, author/footer text, integer RGB color, timestamp and fields
let a receiver recreate the card. Field values use a small Discord Markdown subset
(inline code for literal host/target metadata); names, title, description and footer
are plain text. `inline` is a layout hint, not a requirement for a receiver.
The receiver must implement the rendering; the JSON envelope is not itself a
Discord webhook payload. Original unmodified metadata remains under `event`.

This is an additive extension of `reef.action.v1`: existing `schema` and `event`
fields retain their values and meaning. Receivers that ignore unfamiliar fields
continue to work; strict custom validators must allow `presentation` before upgrading.
No event selection, privacy boundary, authentication or delivery behavior changes.

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

Network actions (v0.13.0): create, remove, cleanup, connect and disconnect emit
started/results metadata through the same Local/remote notification pipeline.
No network driver options, IPAM options or container output enter events.

## Central action history (v0.15.0)

History uses the same typed events independently of webhook configuration. It
does not relax webhook activation cutoffs or send historical metadata to an
external receiver. Remote acceptance persists/deduplicates independently in
history and notifications; a failed ready sink causes metadata retry, not Docker
action replay. See [history guarantees and limits](ACTION_HISTORY.md).


## Monitoring transitions (v0.16.0)

When enabled in Alerts, central monitoring emits scope `monitor`, action
`disconnected`, `unhealthy` or `restart_loop`. `failed` means an incident opened;
`completed` means stable recovery was observed. Both share an `operation_id`
(the incident ID), while their event IDs differ. Discord labels these explicitly
as incident opening/recovery. JSON retains the `reef.action.v1` envelope with
these new enum values. Consumers should tolerate new action/scope values.
Host IDs/names remain those of the observed host, including remote hosts.

Monitoring transitions have a bounded durable outbox and are accepted idempotently
by central History and notification storage before that outbox acknowledges them.
External delivery still follows the existing retry/expiry rules. Policy changes,
revocation, removal and stopping retire incidents without emitting recovery.
Previously accepted webhook deliveries cannot be withdrawn by a later policy
change. Disabled webhooks do not receive a backfill when enabled later.
