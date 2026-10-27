# Reef

**Your Compose projects, made simple.** A personal Docker interface built with Go and its standard library. One process, no external Go packages, no database or CDN. JavaScript and CSS are embedded and served locally. The display name is centralized in `appName` in `internal/app/server.go`; the marine styling is in `web/style.css`.

The selected deployment target is the same server as the recent applications,
with a `reef` folder beside `flix_webapp` and access over the local network.
No external URL is required. Reef is developed and checked separately, locally;
no server connection or production deployment has been performed by this work.
The original QNAP/Container Station compatibility questions remain unverified.

## Quick start — local Docker

The reference source folder is `/path/to/reef`. Real Docker validation has passed on this Mac.

Prerequisites: a running local Linux Docker engine with its context selected, Docker CLI with Compose v2 and Buildx for source builds, and standard shell utilities. Linux Docker Engine and local macOS Docker runtimes are accepted; Docker Desktop is optional. Python is not required. Go is not required on the host: the shell bootstrap compiles the small Go launcher in a temporary, network-isolated Go container and caches only its executable under `.reef-local/tools/`. No Docker socket or private runtime data is mounted into that compiler container. Node.js is only used for optional frontend development tests.

```sh
cd /path/to/reef
./reef-local.sh up
./reef-local.sh password
```

The first command builds the real image, starts Reef and prints its loopback URL. The second prints the private generated password. The current checked installation is at **http://127.0.0.1:53704**; a fresh installation prefers port 8099 if available. `up` reuses the existing configuration, files and password; it does not reset the playground. It may recreate the Reef UI container after a code change, so allow current operations to finish before running it.

The default Containers view reads **all containers, including stopped ones**, from the connected Docker engine. It displays only names, images, states/status and Compose project/service names. Environments, commands and arbitrary labels are not exposed. Discovery does not authorize operations or open other projects’ files. A fresh installation starts with no managed projects. Existing installations keep their configuration; on this Mac the previous Reef playground remains explicitly configured under `.reef-local/project`. Project bind mounts keep identical absolute paths on the host and inside Reef. The private runtime contains `config.json`, `compose.json`, state, a password file and cached local tooling. Existing managed project data is preserved. `.reef-local/` is excluded from Git, the Docker build context and delivery archives. Do not publish its password or state.

```sh
./reef-local.sh status
./reef-local.sh stop
```

Reef's UI container uses `restart: unless-stopped`. The local launcher sets this
for new installations and applies it to existing Reef service definitions on
`up`, before Compose validation and recreation. Other local launcher commands do
not change the policy. The manual installation example uses the same policy.

`stop` stops only the Reef UI; it does not stop projects managed through Reef. Use the UI's Stop action for the playground. The authenticated Reef UI and its own network are left running for exploration. Preexisting containers are listed read-only; they have not been restarted or adopted. Published images/build layers remain cached; no global cleanup or prune was run.

The launcher checks that the selected context uses a local Unix socket and reaches a Linux Docker engine with a stable ID. It does not require any engine name or vendor. It refuses SSH/TCP contexts, `DOCKER_HOST`/`DOCKER_CONTEXT` overrides and a different engine on later runs; select the context with `docker context use` first. It never switches contexts automatically. Native Linux engines mount their selected socket (including rootless sockets) into Reef at `/var/run/docker.sock`. macOS VM runtimes use the engine's internal `/var/run/docker.sock`; their filesystem sharing must include the checkout and authorized project paths. Keep separate checkouts/private runtimes for different engines.

The launcher supports Linux and macOS on amd64/arm64 and binds the UI to loopback. For a server deployment with LAN access and CI updates, use the production Compose setup below. Runtime acceptance is tested with Linux Engine/rootless and macOS Desktop/Colima/OrbStack context fixtures; the real local engine validation used Docker Desktop.

## Containers and Projects

Containers, Projects, Images and Volumes display an animated loading indicator
for their first inventory read and a refreshing indicator during later reads.
Previously loaded rows remain visible. A reserved status area avoids shifting
filters/tables on refresh, and reduced-motion preferences disable the animation.
An unavailable inventory ends the loading state and reports the error.

Reloading the same browser tab restores the Containers/Projects/Images/Volumes view, open
managed project, Docker host, filters and page scroll position. Navigation is
stored in sessionStorage for that tab; unavailable projects return to the
overview and revoked/missing hosts fall back to Local. If browser storage is
blocked, navigation still works without persistence. Editor drafts, open dialogs,
log output and console connections are not restored by this preference.

**Containers** is the default view. It lists running, stopped, created, paused and other Docker container states, supports combined text, state, Compose project and access filters, and refreshes every five seconds. Counts always describe the full snapshot; the result line describes the filtered subset. If Docker becomes unavailable, Reef shows a clear error and keeps the last successful snapshot marked as disconnected.

**Projects** combines discovered Compose groups with managed projects that currently have no containers, and distinguishes **View only** from **Managed**. Search and the Access filter narrow this list; counts include managed projects with no containers. Choose **Open project** to enter its editor, or use **All projects** to return to the overview. No Compose file is loaded merely by opening the overview. Only projects explicitly authorized in configuration or confirmed through **Manage this project** have an editor or Compose operations. Seeing a container never grants management access. Reef reads only the Compose working-directory/config-file labels as untrusted form suggestions; opening the form does not follow those paths.

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

On this development Mac, the installation is `/path/to/reef`, so the applied default root and bind are `/path/to/projects`. This service authorization does **not** grant the coding agent permission to explore other Workspace projects; verification uses disposable fixtures under Reef only. Containers remain visible-only until the user confirms registration.

In the project editor, **More actions → Stop managing** removes a UI registration after confirmation, without stopping containers, running Compose, deleting project files or deleting operation history. An active operation blocks removal. Entries originally configured in `config.json` cannot be removed by this button. Reef's own Compose project cannot be registered.

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

## Volumes

**Volumes** lists named and anonymous Docker volumes on the selected host.
Search by name, driver or Compose project and filter used/unused volumes.
Details show driver, scope, creation date, Docker mountpoint, Compose project and
referencing containers, including stopped containers. Mountpoints are metadata
only: Reef never opens their paths or browses/edits volume contents. Bind mounts,
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

## Container and application logs

Container names open a details panel with the full ID, image, state/status and
Compose project/service metadata from inventory. Managed project editors also
list their individual containers, with the same details and actions. All action
groups use the order **Logs → Console**; stopped containers keep logs available
and disable console access. The container list refreshes without replacing the
Compose editor or discarding its unsaved changes.

Choose **Logs** on a container or project row, or **Application logs** in the
project editor. Project logs let you select a container/service, including
stopped containers. This is read-only access for every container in the local
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

Managed local projects are checked at startup, after an Apply update and every
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

## Configuration

Copy `examples/config.example.json` to your chosen configuration location and replace **all** placeholder paths. No example path has been verified on QNAP.

- `directory`: the **canonical absolute path** to the project directory and the same resolution base used previously. On Linux, `readlink -f` can help identify QNAP path aliases. This is typically the main Compose file's parent. Both `--project-directory` and the command's working directory are fixed to this value.
- `compose_files`: existing relative file paths, with the editable main file first and explicit overrides after it. Implicit overrides are not discovered: list every override previously used. File symlinks in these configured paths are not supported.
- `project_name`: the **exact existing Compose project name**, required. Find it in previous commands or the `com.docker.compose.project` label of an existing container. A wrong name may create a different project. Reef always passes it through `--project-name`.
- `env_files`: relative interpolation files, in their original order. An empty list uses Compose's normal project-directory `.env` behavior. This does not replace service-level `env_file` entries.
- `installation_directory`: canonical absolute host folder of the Reef installation. The local launcher supplies it via `REEF_INSTALLATION_DIR`; manual installs supply this config field or that environment variable. Config takes priority over environment. When explicit roots are absent, its parent becomes the default authorized root. It is never inferred from a candidate or container cwd.
- `allowed_project_roots`: optional canonical absolute directories allowed for UI registrations. Mount each at the identical absolute host/container path. Never `/`. An explicit list replaces the installation-parent default; explicit `[]` disables new registrations. Removing a root still used by a persisted registration blocks startup until the registration/root configuration is reconciled. This is an authorization allowlist, not a filesystem sandbox for external paths referenced inside Compose.
- `state_dir`: an absolute, private, persistent directory separate from application data.
- `secure_cookie`: `false` for HTTP over loopback; `true` behind your HTTPS reverse proxy. The proxy must preserve the exact public `Host`; POST origins are checked against it. `X-Forwarded-*` headers do not determine security policy.

Compose project names must be unique within one instance, and registered projects cannot share Compose files. UI registrations take effect immediately and reload on restart. Installation/root changes and manual edits to the base configuration require a restart. A system lock prevents two processes from sharing the same `state_dir`; use **one instance per set of projects**, even when different state directories could be configured.

## Run locally with Docker access

Only use a production configuration after an explicit installation decision: the action buttons can control the local engine.

```sh
go test -race ./...
go vet ./...
go build -o /tmp/reef ./cmd/reef
# Point QCU_PASSWORD_FILE to a private file containing a strong password.
QCU_PASSWORD_FILE=/private/path/password.txt /tmp/reef -config /private/path/config.json
```

The password must contain at least 16 characters. `QCU_PASSWORD_FILE` takes precedence over `QCU_PASSWORD`. Docker client and Compose plugin must be installed. The socket is fixed to `/var/run/docker.sock`; remote Docker hosts and contexts are not supported. Docker subprocesses receive only `PATH`, `HOME`, the socket and Compose display settings, not the Reef password or arbitrary server environment variables. Put required interpolation values in project `.env` files rather than the server environment.

## Versioned deployment

The [deployment guide](docs/DEPLOYMENT.md) describes automatic stable tags from
`VERSION`, multiarch application/agent images, a version-checked deployment
webhook and operator bootstrap. The runtime `/healthz` endpoint reports only
status, version and revision without requiring a login. It does not inspect
Docker or prove the health of managed projects. No server/NAS has been deployed
by this development work.

## Container and future NAS installation

The `Dockerfile` builds a static Go executable and adds it to the official **Docker client image with Compose included**. No embedded daemon or additional services. Image versions can be changed with `GO_IMAGE` and `DOCKER_IMAGE`; verify them and pin digests for reproducible distribution. The arm64 image has been built and run on local Docker Desktop; its Docker client/Compose plugin and actual update cycle have been verified. This does not establish QNAP compatibility.

`examples/compose.install.yaml` is an **example to adapt**, not a ready-to-apply installation. Create the configuration, state and secret directories before starting; `create_host_path: false` prevents implicit directory creation. The example starts with no managed projects and an explicit placeholder installation_directory, whose parent is mounted. Replace both paths consistently before using Manage this project. Do not register Reef's own installation as a managed project.

Every project bind mount must use **the same canonical absolute source and destination path**. The host Docker daemon must recognize the paths resolved by Compose inside Reef. Mount each authorized project without exposing unrelated shares. Do not move or separate Compose and data directories to accommodate Reef. References outside these directories require additional mounts preserving their paths, after explicit review.

After adapting and checking the paths, and **only after installation is authorized**, the reference commands are:

```sh
# Check installation configuration without starting anything.
docker compose -f examples/compose.install.yaml config --quiet
# Build for the machine running this command.
docker compose -f examples/compose.install.yaml build
# Start Reef; its server will now have access to the Docker socket.
docker compose -f examples/compose.install.yaml up -d
```

If the NAS cannot build the image, build on a suitable machine for its architecture, transfer with `docker save`/`docker load`, then use `up -d --no-build` with the same `reef:local` tag. Do not transfer the macOS binary to the NAS. For cross-platform Buildx builds, explicitly select `linux/amd64` or `linux/arm64`; image and BuildKit availability still need checking in your environment.

The Docker socket grants powerful access, effectively host administration. The example runs as root to access the socket and preserve file ownership when replacing Compose; a limited user requires matching UID/GID and file permissions. The browser never receives the socket. Ports bind to `127.0.0.1` by default: use an SSH tunnel or an authenticated HTTPS reverse proxy on the local network. Do not expose Reef directly to the Internet. The local HTTP cookie uses HttpOnly and SameSite Strict; HTTP over the LAN would still send credentials without encryption. Set `secure_cookie: true` for HTTPS deployment.

Native same-origin forms work with `Referrer-Policy: same-origin`; cross-origin referrer details are suppressed. Strict origin checks remain active. Opaque origins are rejected, including requests carrying `Sec-Fetch-Site: same-origin`. See the [Fetch standard origin-header rules](https://fetch.spec.whatwg.org/#origin-header) for why the previous `no-referrer` setting broke native POST forms.

Sessions expire 12 hours after sign-in and survive Reef restarts/rebuilds without extending that expiry. Sign out revokes the session durably. Changing the configured password invalidates existing sessions at the next startup, including if the old password is later restored. The private `state_dir/auth/sessions.json` stores token fingerprints, CSRF tokens and absolute expiry times in an authenticated registry (directory 0700, file 0600); it never stores the raw cookie or password. Keep this state with the installation. A damaged or unauthentic registry resets to no sessions. Unreadable, public or symlinked authentication state prevents startup. Sign-in/sign-out only succeed after their state is saved. After upgrading from in-memory sessions, sign in once to create a persistent session. Mutations require a per-session CSRF token and matching origin. Login attempts are limited, CSP excludes third-party scripts, and logs are inserted as text. Graceful shutdown waits for operations; `stop_grace_period: 46m` prevents Compose from killing Reef too quickly. A forced kill, power loss or restart may interrupt an update. On restart the operation is marked interrupted with unknown Docker state; no recovery command runs automatically.

### Compatibility checks before production

- Build targets are **Linux amd64 and arm64**; cross-compilation has passed. The Linux arm64 Docker image has executed successfully on Docker Desktop. The standalone Linux executables, amd64 image and QNAP compatibility have not been tested. ARM 32-bit NAS devices are outside Reef's supported targets.
- QNAP/Container Station may use an older engine than the image's Docker client (29.9.2 by default). Check API negotiation and Compose compatibility in an isolated environment matching the NAS, and adapt the client image if needed. Selecting an older image also requires maintenance and security review.
- Private registries: Reef does not manage credentials. Mount appropriate private Docker configuration at `/root/.docker` if required, accounting for credential helpers that may be absent. Do not place these secrets in the displayed Compose file.
- Local build contexts: Dockerfiles and contexts must remain accessible at the same paths. `pull` may fail if an image does not exist in a registry; Reef then stops before downtime. There is no build policy, build button or `pull --ignore-buildable` override. Test projects that only build locally separately.
- `include`, `extends`, nested environment files and advanced features depend on the selected plugin. Custom CLI options and profiles are not configurable; services under profiles are not enabled automatically.

## Persistence, backups and limits

`state_dir/<id>.json` stores the latest operation and log. Completed operations also create `<id>-<op>.history.json`; the UI only shows the latest operation. Writes are atomic and synchronized to disk. Log persistence failures are shown and stop the sequence at the next checkpoint. Each operation log is limited to about 256 KiB; oversized lines are hidden. Historical snapshots and Compose backups have **no automatic retention cleanup**: monitor disk usage and archive manually while no operation is running. Keep the state directory private (0700, files 0600).

Simple `.env` values and the access password are masked in captured output; `config` runs quietly. This is not a universal secret detector: transformed values, YAML secrets, registry output or application messages can escape masking. Compose files and backups may contain plaintext secrets and are visible to the authenticated user. Review screenshots and logs before sharing.

File replacement preserves owner/group and normal Unix permissions, but not necessarily ACLs, extended attributes or hard links; check QNAP permissions and ACLs before production. Backups are private with mode 0600. Reef does not back up application data. Explicit volume changes and application commands remain your responsibility; Reef cannot prevent an application from deleting its own data.

The displayed operation status describes **the operation**, not container healthchecks: successful `up -d` does not prove application health. Reef has no multi-user accounts, automatic crash recovery, automatic rollback, monitoring or availability guarantees.



## Source layout and development checks

The GitLab CI configuration uses the `build` shell runners and disposable Go and
Node containers, following the existing projects' runner convention. It replaces
Auto DevOps with two quality jobs on merge requests and tags only. Branch pushes,
including the push to `master` after a merge, do not rerun quality. Integrate
changes through a merge request; tag pipelines check the version being released.
Checks cover Go race tests, vet, Linux amd64/arm64 application compilation,
launcher compilation and frontend tests/syntax. No Docker socket is passed to
the check containers. Run the same checks locally with `sh scripts/ci.sh go` and
`sh scripts/ci.sh frontend` (Docker required). CI can deliver stable SemVer releases through the GitLab registry and a
protected deployment webhook once explicitly enabled. See
[deployment setup](docs/DEPLOYMENT.md); both release publication and deployment
are disabled until their CI flags and target are configured. A separate job mirrors the source
to [ReefHQ/reef](https://github.com/ReefHQ/reef) after default-branch pushes and
protected tags, once publication is enabled. Every public branch/tag is rewritten
to remove private development notes and session identifiers from its entire
history. GitLab remains the source of truth; public commit IDs differ. Quality
jobs run on merge requests and tags, without repeating after a merge.


### Versioning

Reef uses Semantic Versioning (`MAJOR.MINOR.PATCH`). The `VERSION` file is the
source of truth for the application version, currently `0.8.2` in development.
Release tags
use the matching `v` prefix, for example `v0.1.0`; a version in this file alone
does not mean a release has been tagged or published.

- Before `1.0.0`, minor releases may introduce incompatible changes; patch
  releases contain backward-compatible fixes.
- From `1.0.0`, incompatible changes increment MAJOR, backward-compatible
  features increment MINOR, and backward-compatible fixes increment PATCH.
- Incrementing MAJOR or MINOR resets the lower components to zero. Prereleases
  use suffixes such as `0.2.0-rc.1`. Published releases are immutable.

Compatibility includes configuration, persisted state and documented HTTP/API
behavior. Changes requiring manual migration must be documented in release notes.
Record user-facing changes in `CHANGELOG.md`. When starting work beyond an already
tagged release, bump `VERSION` for the next release (MINOR for features, PATCH for
compatible fixes); related changes may share the same unreleased version. Before
publishing, finalize the changelog and create the matching Git tag. CI checks
SemVer syntax and requires tags to match `VERSION`; no automatic tag is created.

```text
cmd/reef/         Application entry point
cmd/reef-local/   Local launcher entry point
internal/app/     HTTP/auth, configuration, Compose operations and their tests
internal/docker/  Read-only Docker inventory client and tests
internal/local/   Local Docker setup/guardrails and tests, in Go
web/             Embedded HTML/CSS/JavaScript and themes
scripts/         Minimal shell bootstrap; reef-local.sh is the root convenience entry
tests/           Frontend theme, filtering and progress view-model tests
examples/        Explicit configuration and installation examples
.reef-local/     Private live configuration, state, password and tool cache (ignored)
```

There is no Python code or runtime dependency. The previous Python files only generated local configuration and called Docker; that setup logic and its safety tests are now implemented in Go. The server remains one Go process with the Docker CLI/Compose plugin for authorized project actions. Go's standard library is used throughout; no framework, external Go modules or database is required.

Go 1.23+ is needed for source development checks; Node.js 18+ is only needed for frontend tests. The default Go suite uses temporary directories and fake executors. It does not create Docker resources.

```sh
go test -race ./...
go vet ./...
node --test tests/*.test.cjs
node --check web/model.js
node --check web/app.js
node --check web/inventory.js
node --check web/logs.js
node --check web/console.js
node --check web/console-dock.js
node --check web/console-frame.js
node --check web/console-picker.js
node --check web/container-details.js
node scripts/check-version.cjs
node --check web/theme.js
```

The optional `QCU_TEST_DOCKER_CONFIG=1 go test -race ./...` parses temporary fixtures with an installed Compose CLI and a nonexistent socket. Tests cover Compose ordering/preservation, auth/CSRF, all-container inventory including stopped containers, label whitelisting, daemon errors, explicit registration/auth/root checks, concurrent conflicts and persistent reload, non-adoption of merely discovered projects, local engine guardrails and theme preferences. Keep developer reports, screenshots and executables outside the source tree; local bootstrap tooling is isolated under `.reef-local/tools/` and excluded from the image build.

Real Docker Desktop validation on 22 October 2026 covered image build/execution, updates between published images, file and named-volume preservation, and validation/pull failures without stopping the running service. On 23 October, the inventory view, Go launcher and UI registration workflow were tested on the local engine with disposable fixtures under Reef; only Reef itself was redeployed among preexisting containers. QNAP compatibility is still unverified. No production deployment or security certification is implied.

Local runtime references: [Docker contexts](https://docs.docker.com/engine/manage-resources/contexts/), [Colima contexts and sockets](https://colima.run/docs/faq/), [OrbStack Docker context](https://docs.orbstack.dev/docker/).

Official references: [Compose options and paths](https://docs.docker.com/reference/cli/docker/compose/), [interpolation and `.env`](https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/), [include rules](https://docs.docker.com/reference/compose-file/include/), [official Docker client image with Compose](https://github.com/docker-library/docker/blob/master/29/cli/Dockerfile).

Reef is developed under **ReefLabs**, with the GitHub organization identifier
**ReefHQ**. The public mirror is [ReefHQ/reef](https://github.com/ReefHQ/reef).
Reef is licensed under the [GNU General Public License v3.0](LICENSE)
(SPDX: `GPL-3.0-only`). Vendored xterm.js and its fit addon retain their MIT
licenses and attribution under `web/vendor/`.

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

See [agent setup and upgrades](docs/AGENT_SETUP.md) and the
[multi-host architecture](docs/MULTI_HOST_AGENTS.md). The example agent mounts
its installation's parent read/write at the identical host path, following Local's
authorization rule. Explicit roots can restrict adoption. An upgrade preserves its
private identity/state volume and needs no new enrollment token.
