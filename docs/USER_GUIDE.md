# Using Reef

[← Back to Reef](../README.md) · [Local setup](LOCAL_SETUP.md) · [Configuration](CONFIGURATION.md)

## Containers and Projects

Containers, Projects, Images, Volumes and Networks display an animated loading indicator
for their first inventory read and a refreshing indicator during later reads.
Previously loaded rows remain visible. A reserved status area avoids shifting
filters/tables on refresh, and reduced-motion preferences disable the animation.
An unavailable inventory ends the loading state and reports the error.

Reloading the same browser tab restores the Containers/Statistics/History/Projects/Images/Volumes/Networks/Builds view, open
managed project, Docker host, filters and page scroll position. Navigation is
stored in sessionStorage for that tab; unavailable projects return to the
overview and revoked/missing hosts fall back to Local. If browser storage is
blocked, navigation still works without persistence. Editor drafts, open dialogs,
log output and console connections are not restored by this preference.

Changing Docker hosts keeps the current section, including when returning to a
previously visited server. Projects returns to its overview on a different host;
a project from one host is never opened on another automatically. Host-specific
data, selections and filters reset as before. Unsupported sections stay visible
with their agent-upgrade guidance. A capability refresh on the same host keeps
its open project detail.

**Containers** is the default view. It lists running, stopped, created, paused and other Docker container states, supports combined text, state, Compose project and access filters, and refreshes every five seconds. Counts always describe the full snapshot; the result line describes the filtered subset. If Docker becomes unavailable, Reef shows a clear error and keeps the last successful snapshot marked as disconnected.

**Projects** combines discovered Compose groups with managed projects that currently have no containers, and distinguishes **View only** from **Managed**. Search and the Access filter narrow this list; counts include managed projects with no containers. Choose **Open project** to enter its editor, or use **All projects** to return to the overview. The managed-project highlight and picker reflect only the displayed project detail; returning to the overview clears their visual selection while retaining editor drafts in memory. No Compose file is loaded merely by opening the overview. Only projects explicitly authorized in configuration or confirmed through **Manage this project** have an editor or Compose operations. Seeing a container never grants management access. Reef reads only the Compose working-directory/config-file labels as untrusted form suggestions; opening the form does not follow those paths.

Empty `projects: []` is valid for inventory-only operation. To manage a discovered project:

1. Open **Projects → Manage this project** beside its exact existing Compose name. Setup first groups the folder/files and optional environment settings, then shows a separate review. Path errors return to the relevant field without losing your entries.
2. Review the suggested canonical absolute directory and the ordered Compose files (relative paths, main file first). Correct missing, stale or conflicting Docker labels. Enter custom interpolation env files in their original order, if used. Docker labels do not reliably retain these, profiles or all original CLI options. Blank env files uses Compose's normal `.env` behavior. Profiles and custom CLI options remain unsupported; do not adopt a project whose original workflow requires those options.
3. Confirm authorization of these paths, the identity, file order and environment options, choose **Review connection**, then check the authorization and choose **Confirm and manage**. Only after this confirmation does Reef check the submitted paths. No directory is created, no file is moved/copied, and no Compose command is run. Compose syntax is checked by the existing action workflow, not during registration.
4. The editor and existing actions become available immediately, without restarting Reef. Registration persists atomically in the private `state_dir/registered-projects.json`, separately from the read-only configuration and existing operation history. Do not edit that registry while Reef is running. An external edit causes further registry changes to fail until restart; an invalid registry causes startup to fail closed rather than silently losing projects.

**Default authorized root:** the parent of the **Reef installation directory**. For the local launcher, the installation directory means the actual canonical host folder containing `reef-local.sh`, `cmd/`, `internal/` and `web/`. The shell resolves its own location and the Go launcher resolves symlinks there; the caller's working directory, the container's `/app`, the binary's `/usr/local/bin` and Docker project labels are never used to invent this path. Installing Reef at `/srv/compose/reef` therefore defaults to `/srv/compose`. Candidates do not change this rule.

`./reef-local.sh up` passes this known host location to Reef through `REEF_INSTALLATION_DIR` and creates an identical-path, read/write bind for its parent, with `create_host_path: false`. This applies to new installs and migrates the previous implicit local default on the next `up`. The migration preserves the base config byte-for-byte, existing project mounts, configured projects, password, UI registration registry and history; it only adds the installation environment value and the required parent bind to Reef's service definition. It does not enumerate the parent or adopt projects. Repeated launches are idempotent. It refuses conflicting custom parent mounts or a mismatched explicitly supplied installation location rather than replacing them. `status`, `stop` and `password` do not migrate anything.

**Explicit roots take priority:** an `allowed_project_roots` array replaces the default, including an explicit `[]` which disables registration. The local launcher leaves custom root/mount configuration untouched and does not add a parent mount when explicit roots are configured. `null` behaves like an absent field. Setting `installation_directory` in config takes priority over the environment fallback. Removing or changing roots does not automatically remove existing bind mounts; keep the installation's mounts aligned with your chosen authorization policy.

**Manual Docker installations:** the server cannot discover the host installation folder from inside an image. Supply the canonical host path in `installation_directory` in config, or use `REEF_INSTALLATION_DIR` if that field is absent. Mount the installation's parent read/write into Reef at the identical absolute host/container path. Alternatively, explicitly configure `allowed_project_roots` and their matching mounts. When both the installation location and explicit roots are unknown, registration stays disabled; no cwd fallback is used. `/` cannot be a root, including when it would be the installation's parent. Installation/root changes require restarting/recreating only Reef.

For example, the supplied manual example uses `"installation_directory": "/CANONICAL_ABSOLUTE_PATH/reef"` and a bind whose source and target are both `/CANONICAL_ABSOLUTE_PATH`. Replace these placeholders with your actual installation paths. To override this with a narrower root, use e.g. `"allowed_project_roots": ["/srv/compose/selected"]` and an identical-path bind for that root. Directories must already exist. Symlinks in project directories, Compose files or env files (including `.env`) are rejected. The form reports unauthorized/unavailable paths or read-only mounts and cannot repair a missing mount. Opening a form only reads Docker metadata; files are checked only after confirmation. Reef's own Compose identity and its installation directory cannot be registered.

In the project editor, **More actions → Stop managing** removes a UI registration after confirmation, without stopping containers, running Compose, deleting project files or deleting operation history. An active operation blocks removal. Entries originally configured in `config.json` cannot be removed by this button. Reef's own Compose project cannot be registered.

Image, volume and build-cache operation panels close automatically after a fully
successful result. Partial, failed or incomplete results remain readable until
**Dismiss** is selected. Dismissed results stay hidden during later polls and page reloads in the same
browser, scoped to their host and operation. Dismiss stays in the panel’s sticky
heading, accessible while scrolling long results. A new operation shows its own
progress normally. If browser storage is blocked, dismissal still works until
the page is reloaded.

## Editor workflow

Opening a managed project shows its Compose identity, folder, main filename and Saved / Unsaved changes state. **Save file** validates, backs up and saves; **Apply update** also pulls and recreates the project after a single downtime confirmation. **More actions** contains Start / retry, Stop project and (for UI registrations) Stop managing. A rejected validation keeps your submitted edits in the editor for correction. Reload asks before discarding changes, and switching projects preserves the existing unsaved-change guard. Switching to the overview or Containers keeps the editor buffer. Cmd/Ctrl+S runs Save file in the editor.

The operation panel shows progress derived from actual Compose command phases, with a concrete recovery action when startup may be required. The command log stays in a disclosure and opens automatically on a failure. Filters, forms and actions remain keyboard accessible; the tables scroll within their own panel on small screens.

## Working with existing projects

Each authorized project is explicitly listed in configuration or confirmed and persisted through the registration form. Its **existing directory** remains the source of truth: Reef neither copies it into an application tree nor recreates it. Only the first configured Compose file is editable in Reef. Additional configured files retain their order and are included in validation and every command.

**Apply update** performs these steps:

1. Check the loaded revision and lock the project.
2. Create a temporary candidate **beside the original Compose file**, then validate with `config --quiet`, using the same additional files, project name, base directory and environment files.
3. Check the revision again; save the previous bytes in a private `compose.yaml.qcu-backup-<id>` file; atomically replace only the main Compose file.
4. Validate the saved file, then run **`pull`**.
5. Check the revision and validate again before stopping anything.
6. Run **`down`**, then **`up -d`**. No `-v`, no `prune`, no directory deletion.
7. Verify the new containers are running and, when configured, healthy (up to two
   minutes). Remove only replaced image IDs captured before pull, with no tags
   and no containers using them, including stopped containers in other projects.
   Docker removal is never forced and does not prune parent images. Skipped or
   refused cleanup is recorded in the command log and leaves the old image intact.

A validation failure before saving leaves the original intact. A pull failure leaves the edited Compose saved, but prevents `down`. Backups are not restored automatically. If `down` or `up -d` fails, containers may be stopped or partially running; Reef makes this clear and offers **Start / retry** (`config --quiet`, then `up -d`). There is no automatic rollback or availability guarantee.

**More actions → Stop project** validates, then runs `down`, without deleting named volumes. A down/up cycle can remove and recreate containers and networks; anonymous volumes may not be reattached as before. Prefer existing bind mounts or named volumes. **Save file** stops after validation, backup and saving; it does not restart services. Compose action requests cannot provide arbitrary commands: the server constructs their arguments using `exec.CommandContext`, without a host shell. The separately authorized container console uses Docker exec.

Operations continue after closing the tab and have a total timeout of 45 minutes. A project lock prevents editing and simultaneous operations on that project. A content hash covers configured Compose files, configured interpolation files and the default `.env`, rejecting outdated edits. SSH changes are detected at checkpoints, but Reef cannot lock an external editor: do not change these files while an operation is running. Files referenced through `include`, `extends`, service `env_file`, secrets, configs or build contexts are outside this revision hash.

### Image update checks

Managed projects are checked on their owning host at startup, after an Apply update and every
six hours. Newly managed projects and invalidated checks are picked up within a
minute. Open a project's **Image updates** panel and choose **Check for updates**
for a manual check (limited to once per minute). The projects overview shows its
latest summary. Checks continue without an open browser, are serialized and
exclude simultaneous Compose operations on that project.

Reef reads the saved Compose configuration and registry manifests; it does not
pull or recreate containers during a check. It compares the deployed image's
configuration digest for its OS/architecture/variant, including tags such as
`latest` whose contents can change. It does not search for newer tag names or
edit Compose versions. Missing/unreachable/private registries show an unknown
status, never an assumed update. Locally built services, digest-pinned images
and services without deployed containers have explicit separate statuses.
Checks use the Docker CLI's existing registry credentials in Reef's own runtime
(`HOME/.docker`), not the deployment webhook's credentials or the browser's.

Reports are transient and refreshed after restart. A check of saved files can
differ from an unsaved editor draft. There is no automatic application update;
use **Apply update** when ready. Cleanup runs only after this action succeeds,
never after a failed pull/recreation or a standalone Start / retry. Tagged old
versions remain available. A healthcheck-free container must be running, which
does not establish application-level health. No rollback image is guaranteed
after eligible old images are removed.

## Images

**Images** lists the selected host's Docker images, including untagged images.
Search tags, IDs and digests, filter used/unused images, and open an image for
its full ID, tags, registry digests, creation date, platform, size and containers.
Usage includes stopped containers. Cumulative sizes include shared layers and
are not an estimate of recoverable disk space. Refresh updates the inventory;
background refresh runs every ten seconds while the browser tab is visible.
The main navigation shows the host’s total image count, independent of filters.

Pull downloads an image reference using the selected host's Docker credentials.
It can move an existing tag but does not restart containers or apply a project.
Add tag requires an explicit repository:tag and refuses existing destinations.
Image details offer Remove tag; removing the last reference may delete the
unused image. Used images retain their tags. Image configuration, environments,
history, volumes and build cache are not exposed or cleaned.

Select unused images for batch deletion, or use Review cleanup with either
unused images without tags (default) or all unused images. The confirmation lists
the exact candidate images and host. A single request accepts the entire selection
(up to the inventory limit of 2000 images), processes automatic batches of 100
and reports per-image progress. The whole accepted operation continues without
keeping the browser open; do not submit later batches manually.
Before each removal, Reef verifies the image ID, tags/digests and current usage;
changed images are retained for review. It removes explicit references without
force or parent pruning. Multi-tag deletion can partially succeed if Docker
refuses a later reference; refresh and inspect the per-image result before
retrying. Other Docker clients can still change the engine concurrently.

One image mutation runs per host, excluding Reef Compose operations. Pulls and
batches continue after closing the page or disconnecting an agent and have a
15-minute deadline for pull/tag operations and a 45-minute deadline for deletion
or cleanup. Timed-out selections report unprocessed images and are never replayed.
Their latest per-image report is kept in memory only;
after a process restart, refresh inventory to reconcile an interrupted operation.
Inventory reads are limited to 2000 images/10000 containers and ten seconds.
Docker errors are summarized without exposing registry credentials.

Local and upgraded remote agents use the same implementation. Upgrade central
first, then agents to v0.8.2 while preserving their existing identity volume,
CA and configuration. The images-v1 capability is negotiated per connection;
older agents retain their existing features and request an upgrade for Images.
Selections above 100 also require images-batch-v1 (agent v0.8.2). Older image
agents keep small operations available and show a clear upgrade message for a
large selection. Upgrade central before agents and preserve identity/CA/config.

The approved feature sequence is tracked in [the roadmap](ROADMAP.md).

## Volumes

**Volumes** lists named and anonymous Docker volumes on the selected host.
Search by name, driver or Compose project and filter used/unused volumes.
Details show driver, scope, creation date, Docker mountpoint, Compose project and
referencing containers, including stopped containers. Mountpoints are metadata
only: Reef never opens their mountpoint paths or browses/edits individual files.
From v0.18.0, use **Backup** or **Backups** for explicit filesystem archives and
restoration into new volumes. See [Volume backups](VOLUME_BACKUPS.md). Bind mounts,
volume driver options and arbitrary labels are not exposed. Volume sizes are not
estimated by scanning application data.

Create volume accepts a name and uses Docker's local driver with no custom
options. Existing names are refused; a unique creation label verifies whether
Docker actually created the volume rather than reusing a concurrent creation.
Creating a volume does not attach it to any container or edit Compose files.

Delete a volume, select several, or choose Review unused volumes. The confirmation
names the host and each exact candidate and explicitly warns of permanent data
loss. A volume without a container reference can still contain valuable data;
review backups before deletion. No undo or automatic backup is provided.
Batches contain at most 100 volumes, and may partially succeed with a result for
each volume. Docker volumes backed by external drivers follow that driver's
removal semantics; external users of their storage are not visible to Reef.

Before removal Reef rechecks metadata/creation identity, all running/stopped
container references and the current fingerprint. Volumes without a recorded
creation identity are retained. Docker removal runs without force and refuses
new container references. There is no global volume/system prune. Other Docker
clients can still race named-volume recreation; pause conflicting external
operations while reviewing/removing volumes.

Image, volume and Reef Compose mutations are mutually exclusive per host.
Volume workers continue after closing the page/disconnecting and have a
five-minute deadline. The latest result is in memory, so reconcile inventory
after a process restart before retrying. Inventory reads have a ten-second limit,
with at most 2000 volumes and 10000 containers. Inventories refresh every ten seconds while the browser tab is visible;
the navigation shows total volume counts independently of filters.
Page/filters/scroll are restored per tab on reload.

The same backend runs locally and on agents advertising volumes-v1. Upgrade
central first, then agents to v0.8.0 while preserving identity volumes, CA and
configuration. Older agents keep their existing capabilities and show an upgrade
message for Volumes.

## Networks

**Networks** lists the selected host's Docker networks with a total navigation
count, search and used/unused filters. Details show driver, scope, creation time,
Compose project, internal/IPv6/attachable flags, subnets, gateways and referencing
containers, including stopped containers. Driver options, IPAM options and
arbitrary labels are not exposed.

Create network uses the local bridge driver with automatic IP addressing. Select
unused networks for deletion or choose Review unused networks to confirm exact
candidates. Up to 2000 networks can be selected per operation. Networks with any
container reference or endpoint are retained. Built-in networks, non-local scopes,
non-bridge drivers and networks used by Reef administration containers are
inspection-only. Mutations use immutable IDs and recheck configuration/usage;
Docker commands never force removal or prune globally.

In network details, choose a running container and Connect or Disconnect after
confirmation. Reef containers and their Compose companions, shared network
namespaces and host/none network modes are protected. These are live changes:
they can interrupt application traffic and do not update Compose files. Configure
persistent network connections in Compose before recreating a managed project.
Other Docker clients can still change the engine concurrently.

Network operations share the existing host-wide mutation lock with Compose,
images, volumes, builds and agent updates. Accepted operations run independently
of the browser, with a five-minute deadline and per-target results. Reports are
in-memory; refresh inventory after a process restart or uncertain outcome before
retrying. Inventory reads are limited to 2000 networks/10000 containers and ten
seconds. Successful reports auto-hide; dismissed failed/partial results stay
hidden after reload. Action metadata is included in existing webhooks.

Remote agents require the negotiated **networks-v1** capability (v0.13.0).
Upgrade central first, then agents, preserving their existing identity/configuration;
older agents keep their other features and display an upgrade message for Networks.
The view, filters and page position are restored across reloads.

## Builds and builder cache

**Builds** constructs images from a managed project's saved Compose files, ordered
config/env files and current file revision. Choose all buildable services or one
service; optional Without cache and Refresh base images map to Compose build
options. Build definitions/contexts must be accessible on the selected host.
Unsaved editor changes are excluded. A build may update image tags but never
runs Compose up/down, pushes images or restarts containers. Apply an update
separately when you intend to deploy it. Build contexts and Dockerfile commands
execute with the authority of the selected Docker engine, as in ordinary Compose.

Builds use the explicit default builder, retain the 45-minute operation deadline
and drain on graceful shutdown. They exclude other Reef project/storage/cache
mutations on that host. Build service discovery is limited to 100 services and
has a ten-second UI timeout; changing saved files invalidates the submitted
revision. Missing contexts/Buildx/authentication are reported as build failures.
Existing engine credentials apply; no shell snippets/build args or arbitrary
Dockerfile paths are submitted by the browser.

The Builds navigation counter counts **managed projects**, not executed builds or
cache records. Choose a managed project to see its **Build history**: only builds
launched through Reef are recorded here, so an empty history can be expected.
Use **Refresh cache** to load the selected host’s **default builder** cache; other
builders are not included, and an unused/cleaned builder can have no records.

For read-only deployments, upgrade central Reef and affected agents to **0.22.3+**
to fix Buildx client metadata writes. Reef uses private temporary metadata while
keeping registry credentials read-only. This fixes cache inspection and Compose
build execution; it does not import builds launched outside Reef into history.

The page shows the latest 50 builds per selected project, across subsequent
project operations and process restarts. Summary metadata is stored separately
from the bounded operation logs in the private state directory, with no automatic
retention deletion. Reading history is capped at 10000 directory entries; archive
old history if that limit is reached. Build logs refresh independently of the
form and preserve the reading position. Known project env/password values are
masked, but arbitrary Dockerfile output can still contain application secrets.
A restart marks an interrupted build unknown without claiming containers stopped;
no build command is replayed. No-cache builds leave existing cache intact.

Refresh cache inspects only the selected host's default engine builder; it does
not initialize or switch custom builders. Numeric byte sizes and stable timestamps
come from the Engine API build-cache query; Buildx supplies mutable/reclaimable
states. Descriptions/Dockerfile commands are omitted from the browser response.
Cumulative/shared sizes do not predict recoverable disk space. The cache read is
bounded to ten seconds, 10000 records and limited response sizes.

Select records, or review reclaimable cache, then confirm exact IDs and host.
Reef rechecks metadata/fingerprints and retains changed, mutable or in-use records.
Each prune targets exactly one ID after these checks, using Docker's
non-interactive confirmation flag only after the UI confirmation. There is no
unfiltered system/image/volume prune. Shared storage needed by images remains
owned by Docker; clearing cache can slow future builds. External Docker clients
can race eligibility, so cleanup reports retained/unknown outcomes for review.
The cleanup is a detached, serialized 45-minute operation with per-record progress;
its latest report is in memory and must be reconciled after restart.

The same features run on Local and agents negotiating builds-v1. Upgrade central
then agents to v0.9.0, preserving identity volume, CA and configuration. Older
agents keep their previous features and require an upgrade for Builds.
See Docker's [cache inspection](https://docs.docker.com/reference/cli/docker/buildx/du/)
and [targeted cache pruning](https://docs.docker.com/reference/cli/docker/buildx/prune/).

## Statistics

**Statistics** shows CPU, memory, network RX/TX and block read/write metrics for
the selected Docker host. Search by container, image, Compose project or ID and
filter running/not-running containers. Pages contain at most 32 containers;
page, filters and scroll position are restored when reloading the same tab.

Snapshots refresh five seconds after the previous collection finishes, only
while the page/tab is visible. Pause stops automatic collection; Refresh can
collect one snapshot while paused. Leaving the page, hiding the tab or changing
hosts cancels pending requests and discards late responses. Stopped containers
are listed without reading live stats. Unsupported metrics and missing samples
show **—**, rather than a false zero. Failed requests keep previous samples with
an explicit stale-data message; per-container failures remain visible per row.

CPU uses Docker's current/previous sample deltas: one fully used CPU core is
100%, so multicore usage can exceed 100%. Memory excludes inactive file cache
when reported (cgroup v1/v2 with a legacy cache fallback); the displayed limit is
Docker's reported memory limit and may be the host/VM limit. Network and block
I/O Σ values are cumulative counters. Rates are differences between two displayed
samples divided by their actual sample timestamps; they remain unavailable on
the first sample, after counter reset or after changing page/filter/host.
See [Docker statistics semantics](https://docs.docker.com/reference/cli/docker/container/stats/).

The backend uses stream=false reads (two sampling cycles for CPU), not an open
subscription or background monitor. A host-wide sampler shares identical pending
reads, caches successes/failures for two seconds, retains at most 256 entries and
allows eight simultaneous Docker stats calls. Each inventory/page request has a
ten-second deadline; inventory is limited to 10000 containers and each Docker
stats response to 1 MiB. No metrics history is written to disk; the browser keeps
only the current page's previous samples to calculate rates. Reads do not run
exec commands or alter containers. Statistics do not generate action webhooks.

Remote agents require the negotiated **stats-v1** capability (v0.14.0). Upgrade
central before agents, keeping identity, CA and mounts. Older agents keep their
existing features and display an upgrade message for Statistics; no Local fallback.

## Action history

**History** lists action metadata from Local and compatible agents, even when
webhooks are disabled. Starts and results of the same host/category/action/
operation are grouped into one row. Filters cover the selected host or all hosts,
category, result and a text search; Details shows timestamps, operation ID and
aggregate result counts. No commands, environments, log output, terminal contents,
webhook URLs or registry credentials enter this journal.

The latest page refreshes every five seconds while visible, with Pause/Resume
and manual Refresh. Older pages use a sequence cursor and pause automatic refresh
so newly recorded actions do not shift the page being read. The view, filters,
older-page cursor and scroll are restored in the same browser tab after reload.
Disconnected/revoked hosts' recorded actions remain readable centrally; no live
agent request or fallback to Local is used for history reads.

History retains at most **2000 events or 30 days from central receipt**; grouped
actions may use multiple events. Local writes use a bounded background queue,
independent of Docker action admission. Failed/full persistence is reported in
the UI and never changes a Docker action's outcome or replays its command. This
is a best-effort journal: abrupt shutdowns, queue overflow, missing final events
or unavailable agent event delivery can leave gaps. An unmatched local start is
shown as Unknown after a central restart; remote starts remain last-recorded
starts until their outcome arrives. History does not prove current running state
or identify an individual actor for legacy, automatic or remote events. Individual
accounts restrict access but do not retroactively attribute those events.

History starts with new events after this upgrade and any eligible pending agent
metadata. Existing project command-log history is not imported. Remote collection
reuses **action-events-v1** (agent v0.11.0+); compatible existing agents need no
new enrollment or mounts for this feature. See [history storage and limits](ACTION_HISTORY.md).

## Container and application logs

Container names open a details panel with the full ID, image, state/status and
Compose project/service metadata from inventory. Managed project editors also
list their individual containers, with the same details and actions. All action
groups use the order **Logs → Console**; stopped containers keep logs available
and disable console access. The container list refreshes without replacing the
Compose editor or discarding its unsaved changes.

Standalone containers also offer **Manage** after Logs/Console: start, stop,
restart, update their image or delete the container while retaining volumes.
Review host, exact identity, downtime and writable-layer loss first. Compose/Swarm
and Reef administration containers stay with their owner/project. Remote management
requires agent 0.22.0+; see [recreation and recovery](STANDALONE_CONTAINERS.md).
**Review container operation** reopens the latest host operation even after the
original disappears, without replaying it.

Choose **Logs** on a container or project row, or **Application logs** in the
project editor. Project logs let you select a container/service, including
stopped containers. This is read-only access for every container in the selected host’s
inventory; a project does not need to be managed by Reef.

The viewer reads application stdout/stderr through Docker, with timestamps.
Choose all output, stdout or stderr and the latest 100, 200, 500 or 1000 lines.
Snapshots refresh every three seconds while the viewer is open and the tab is
visible. Pause updates to inspect a snapshot; scroll up to read without being
automatically moved to the bottom. Closing the viewer cancels pending reads.
Errors retain the last successful output and pause automatic refresh.

Each response is capped at 256 KiB and reports truncation. Logs are shown as
plain text and are not saved by Reef. They can contain application secrets;
unlike Compose operation logs, application output is not masked. TTY containers
combine stdout/stderr; drivers without readable logs may return an error.
Files inside containers are not read.
This is a periodically refreshed snapshot rather than a persistent Docker
follow/attach connection. No container is restarted or adopted by reading logs.

## Container console

Choose **Console** on a running container, project row or managed project editor.
A new session tab opens in Reef's persistent bottom panel. Select `/bin/sh` or
`/bin/bash`, then **Connect** to authorize the shell; opening a console tab alone
executes nothing. The selected shell must exist in the container image.

Each tab has its own shell, even when several tabs target the same container.
Continue navigating Containers/Projects and editing Compose while sessions stay
connected. **New session** opens another tab for the selected container. Drag the
panel separator (or use its arrow keys), reduce, expand or maximize the panel
without disconnecting. Switching tabs does not close or recreate terminals.
Project/container names remain visible to identify the selected shell.

**Disconnect** ends the selected shell connection and keeps its terminal output
available. **Disconnect all** ends all connections in this panel. Closing a
console tab disconnects it and removes its terminal. A page reload, closing the
browser tab, logout, login expiry or Reef shutdown also ends connections; Reef
never replays commands or reconnects shells automatically. Use `exit` to leave
the shell. Disconnecting does not guarantee that already started commands have
terminated, and never stops the container.

The exec inherits the container's configured user and has no additional
privileges. Console access is independent of Compose registration; an
authenticated user can explicitly connect to any running container in inventory.
Commands can change files and mounted data; input/output is neither masked nor
persisted. These commands are outside Compose operation locks, like an external
editor. Avoid changing Compose files during operations. Container recreation
interrupts its console. Stopped/paused containers have no console.

Defaults are **8 console sessions per login and 16 for the Reef instance**.
Configure `console_sessions_per_login` (1–64) and `console_sessions_total` (1–128)
in config; omitted/zero values use the defaults, and the per-login limit cannot
exceed the total. The panel permits that many session tabs; server quotas count
active and opening shells across browser pages. Configuration changes require
restarting Reef. Sessions retain their 30-minute lifetime, 10-minute input/output
inactivity timeout and 16 MiB output limit.

The Go backend uses only the standard library. Local xterm.js 6.0.0 and fit addon
0.11.0 (MIT) render each terminal in an isolated same-origin frame. Only the
terminal document permits inline CSS and same-origin framing; the main page's
CSP and framing restrictions remain intact. One bounded HTTP output stream is
shared by all shells in the panel, avoiding HTTP/1.1 connection-pool exhaustion.
A reverse proxy must pass streaming responses without buffering; heartbeats are
sent every 15 seconds. No CDN, WebSocket or runtime package download is used.

## Appearance

Use **System / Light / Dark** on the login page or in the application header. System is the default and follows OS changes. Explicit choices are remembered in this browser for the current origin and shared between its tabs; selecting System clears the override. The theme is applied by a small local script before styles load to avoid a light flash when dark mode is selected. If browser storage is blocked, switching still works for the current page but may not persist. CSS provides a system-theme fallback without JavaScript.

Both marine palettes cover the editor, operation logs, warning/error states, confirmation dialog, keyboard focus and login form. Theme controls use native keyboard-operable buttons with `aria-pressed`. No backend, Compose command, authentication policy or project data behavior depends on the theme.


## Action webhooks

Open **Webhooks** in the navigation to send action metadata/results to Discord or
a generic JSON endpoint. Settings are instance-wide, the saved URL stays private,
and a test button and delivery status are available. Notifications cover projects,
images, volumes, builds, agents and console lifecycle; no Compose contents, logs,
credentials or console input/output are sent.

Delivery uses a bounded persistent queue and retries without blocking Docker
operations. Remote events require agents v0.11.0 or newer and travel through their
existing outbound connection; only central Reef knows the webhook URL. See
[configuration, payloads and delivery limits](WEBHOOKS.md).

## Agent updates

**Manage hosts** can update a prepared remote agent to the exact stable version
and revision of central Reef, after confirmation. Its host must be idle and its
consoles disconnected. Progress and outcomes persist on that host; success
requires the replacement agent to reconnect with the expected build. Other
managed containers keep running. No automatic upgrades or rollbacks are made.

Existing agents need one manual upgrade to v0.10.0 and the optional host-local
update worker. See [one-time setup and recovery](AGENT_SETUP.md#update-agents-from-reef).
The worker preserves the agent's identity volume, CA and configuration and stays
running independently while replacing only the agent service.

## Multiple Docker hosts

Reef manages remote Docker hosts through outbound HTTPS agents. Upgraded agents
provide the same logs, consoles, project registration, Compose editing/actions,
image checks and targeted cleanup as Local. The selected host owns its files,
Docker operations, backups and history. Editor drafts are isolated by host;
console tabs stay attached to their original host while you navigate.

Agents are disabled by default centrally. Enable `agents_enabled: true` and
provide an HTTPS endpoint reachable from the agent, with certificate verification.
No incoming remote-agent port is needed. Existing inventory agents continue to
work until upgraded; management requires the negotiated `control-v1` capability.

See [agent setup and upgrades](AGENT_SETUP.md) and the
[multi-host architecture](MULTI_HOST_AGENTS.md). The example agent mounts
its installation's parent read/write at the identical host path, following Local's
authorization rule. Explicit roots can restrict adoption. An upgrade preserves its
private identity/state volume and needs no new enrollment token.
## Persistence, backups and limits

`state_dir/<id>.json` stores the latest operation and log. Completed operations also create `<id>-<op>.history.json`; the UI only shows the latest operation. Writes are atomic and synchronized to disk. Log persistence failures are shown and stop the sequence at the next checkpoint. Each operation log is limited to about 256 KiB; oversized lines are hidden. These project operation snapshots and Compose backups have **no automatic retention cleanup**: monitor disk usage and archive manually while no operation is running. Keep the state directory private (0700, files 0600). The separate action metadata journal has its own [bounded retention](ACTION_HISTORY.md#persistence-and-retention).

Simple `.env` values and the access password are masked in captured output; `config` runs quietly. This is not a universal secret detector: transformed values, YAML secrets, registry output or application messages can escape masking. Compose files and backups may contain plaintext secrets and are visible to the authenticated user. Review screenshots and logs before sharing.

File replacement preserves owner/group and normal Unix permissions, but not necessarily ACLs, extended attributes or hard links; check QNAP permissions and ACLs before production. Backups are private with mode 0600. Compose file backups do not include application data. Use the separate [volume backup workflow](VOLUME_BACKUPS.md) for compatible local volumes. Explicit volume changes and application commands remain your responsibility; Reef cannot prevent an application from deleting its own data.

A successful Compose command does not by itself prove application health. Apply update checks running/healthcheck state before eligible old-image cleanup, but application-level checks remain your responsibility. Reef supports [individual accounts and host permissions](USERS.md), but has no automatic rollback or unattended recovery. Live Statistics is available; [monitoring alerts](MONITORING.md) can be enabled in Alerts.


## Monitoring alerts

Open **Alerts** to enable the instance-wide monitoring policy and set disconnect,
unhealthy, restart-count/window and stable-recovery thresholds. Monitoring runs
without an open browser. Filter incidents by host, condition/target text and state;
check Collection coverage before treating the absence of alerts as good health.
Notifications use the configured Webhooks destination. Compatible remote agents
need v0.16.0; connection monitoring also works with older agents.
See [thresholds, maintenance and persistence](MONITORING.md).


## Accounts and host permissions

Sign in with a username and password. Leave **Remember me for 30 days** unchecked
for a 12-hour session, or select it to stay signed in on this browser for 30 days.
Both survive normal Reef restarts; logout and account/session revocation still
apply immediately. The existing installation becomes initial
username `admin` using its current password; old shared-login cookies expire on
upgrade. Open your username in the header to change your password or sign out
everywhere. An all-host administrator can add users, assign admin/viewer roles
and select hosts, disable/delete accounts or revoke their sessions. At least one
enabled all-host admin must remain. Viewers may read assigned hosts, including
Compose files/logs, but cannot save, mutate resources, open consoles or change
global settings. See [migration, behavior and offline recovery](USERS.md).

## Schedules

From central v0.19.0, open **Schedules** for daily/weekly project image checks,
volume backups and explicitly approved age-based image/cache cleanup. Choose a
timezone, review the target/policy and enable the task. Missed/offline/busy slots
are skipped. Uncertain mutations require host review before scheduled work can
resume; [timing, permissions and recovery](SCHEDULES.md).

## Agent diagnostics

From central v0.20.0, **Manage hosts → Diagnostics** shows central observations
for an assigned agent: freshness, version/protocol, current or historical feature
reports and recent troubleshooting hints. It is read-only and requires no agent
upgrade or probe; [interpret the results](AGENT_DIAGNOSTICS.md).
