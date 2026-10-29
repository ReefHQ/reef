# Scheduled operations 🕰️

[← Back to Reef](../README.md) · [Volume backups](VOLUME_BACKUPS.md) · [Accounts](USERS.md)

Available from **central Reef v0.19.0**. Schedules run on the server without an
open browser. Agents perform the existing operations on their own host; they do
not receive an autonomous scheduler or store your login password.

## Set the night shift

1. Select a host and open **Schedules → New schedule**.
2. Name the task and choose its operation and target.
3. Choose **Daily** or **Weekly**, a local time and an explicit IANA timezone,
   such as `Europe/Paris` or `UTC`.
4. Review the policy. New schedules start disabled; check **Enable** when ready.
5. Select **Review and save**, read the current target preview and confirm.

The list shows enabled state, next execution, last result and any required review.
Use **All allowed hosts** to include schedules on disconnected or removed hosts
you are still authorized to manage. Viewers can inspect metadata; admins may
create, edit, disable or delete schedules on assigned hosts. Polling preserves
form drafts; reloading restores the Schedules view, host filter and scroll.

**Disable** prevents future dispatches; it does not cancel an operation already
accepted by Docker. Deleting a schedule does not undo previous actions. Wait for
a running task to finish before editing or deleting its schedule. A disabled
schedule can be edited and explicitly enabled again for its next future slot.

## Choose a policy

| Operation | What Reef is allowed to do |
| --- | --- |
| **Check project images** | Read registry manifests for one managed project. It never deploys an update. |
| **Back up a volume** | Back up one volume identity on its host. Explicitly approve downtime for all eligible current **and future** consumers of that volume. |
| **Clean old untagged images** | Remove images without tags, digests or running/stopped container references, after the minimum age. |
| **Clean old build cache** | Remove old, reclaimable, unused, unshared, immutable records from the default builder only. |

Cleanup policies require **1–365 days** minimum age and **1–2,000 targets** maximum
per run; defaults are 7 days and 100 targets. Each run selects fresh candidates
under that approved policy, then rechecks exact IDs/fingerprints through the
ordinary resource handlers. Missing, invalid, future or too-recent timestamps
are retained. Cache must satisfy both creation age and last-use age when known.
There is no global Docker prune and no scheduled deletion of volumes or networks.

Projects and backup volumes are bound to their creation/configuration identity.
Replacing the target does not silently redirect the schedule to the replacement;
edit and review its current identity. Backup validation, private storage,
retention and recovery follow the [volume backup guide](VOLUME_BACKUPS.md).
Restoration remains a manual, guided action into a new volume.

These schedules supplement the existing automatic six-hour image checks. A check
that is already running, or a recent manual/automatic check inside its cooldown,
can satisfy or skip the scheduled request without duplicating registry work.

## Timing, downtime and restarts

Central Reef evaluates schedules every ten seconds. A normal tick may claim a
slot up to one minute late. Slots missed while Reef was stopped, or beyond that
window, are **skipped**, with no catch-up queue. A host must be connected and
support the required operation when evaluated. Offline, busy or unsupported hosts
are skipped; reconnecting does not replay a missed task.

At most **four scheduled tasks** run across Reef and **one per host**. Existing
resource locks still reject incompatible manual/Compose/storage operations. Tasks
that overlap are skipped rather than queued. Space tasks apart, particularly
when backups have application downtime. Read-only inspections may still run.

Timezone data ships inside Reef. A spring clock gap has no occurrence on that
day; a repeated fall local time is consumed once per local date/time. Claims and
the consumed local slot survive restarts, so the second occurrence cannot repeat
an already consumed slot. UTC scheduling avoids seasonal wall-clock shifts.

Reef persists the claim **before** sending a command. A transport timeout, lost
result, replaced operation report or interrupted observation cannot prove whether
an accepted mutation completed. Its result becomes **Unknown**, the schedule is
disabled, and other tasks on that host are blocked pending review. This can also
happen when central Reef shuts down while observing a detached operation: that
operation may continue on its host. No automatic retry, rollback or restore runs.

Inspect the host's resource/backup report and application state, then edit the
uncertain schedule and explicitly acknowledge the outcome. Save it disabled or
enable it for a **future** slot. Deleting an uncertain schedule also requires
acknowledgement. Acknowledgement clears the scheduling block; it does not perform
recovery commands or change the old result to Success. Read-only image-check
failures need no mutation-outcome acknowledgement.

## Authorization and private state

Each save binds the schedule to the current admin account, its authorization
revision and selected host. Accounts are checked again at execution and before
internal API calls; schedules do not create browser sessions or store passwords.
Disabling/deleting the authorizing account, changing permissions, changing its
password or revoking its sessions invalidates that stored grant. At the next
slot Reef disables/skips the schedule. An authorized admin must edit and confirm
it again. Operations already admitted may finish under the ordinary Reef rules.

Up to **128 schedules** and their latest result are stored in private
`state_dir/schedules/state.json` (directory 0700, file 0600, 2 MiB maximum), in the
existing persistent central state mount. Keep this state with the installation.
Claims, results, edits and disables use atomic/fsynced writes and revision checks.
A form keeps its creation ID across a lost response, preventing duplicate tasks
from the same retry. Inspect the current list before retrying an ambiguous save.
Corrupt, public, symlinked or externally modified state stops scheduling; other
Reef features remain available. Do not edit this file while Reef is running.

History and the configured Discord/JSON webhook receive schedule configuration
and execution metadata, in addition to the ordinary resource action events.
Skipped runs have a skipped/partial outcome; missing results remain Unknown.
These optional journals are best-effort and never contain terminal output,
volume contents, archive bytes or authentication secrets. Consult Schedules for
the durable latest result; no complete user audit or guaranteed delivery is claimed.

## Which agents need upgrading?

Scheduling itself is central-only and adds no new agent capability or port.
Managed-project checks require control support, image cleanup requires image
management (and batch support above 100 targets), and cache cleanup requires
Builds support. **Scheduled volume backups require agent v0.18.0 or newer** with
`volume-backups-v1`. Existing compatible agents need no re-enrollment; central
never falls back to Local when a remote host is unavailable.
