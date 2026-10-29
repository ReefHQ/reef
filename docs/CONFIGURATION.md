# Configuration and manual installation

[← Back to Reef](../README.md) · [Recommended server deployment](DEPLOYMENT.md) · [Usage guide](USER_GUIDE.md)

## Configuration

Copy `examples/config.example.json` to your chosen configuration location and replace **all** placeholder paths. Adapt paths to the target host; example placeholders are not installation defaults.

- `directory`: the **canonical absolute path** to the project directory and the same resolution base used previously. On Linux, `readlink -f` can help identify QNAP path aliases. This is typically the main Compose file's parent. Both `--project-directory` and the command's working directory are fixed to this value.
- `compose_files`: existing relative file paths, with the editable main file first and explicit overrides after it. Implicit overrides are not discovered: list every override previously used. File symlinks in these configured paths are not supported.
- `project_name`: the **exact existing Compose project name**, required. Find it in previous commands or the `com.docker.compose.project` label of an existing container. A wrong name may create a different project. Reef always passes it through `--project-name`.
- `env_files`: relative interpolation files, in their original order. An empty list uses Compose's normal project-directory `.env` behavior. This does not replace service-level `env_file` entries.
- `installation_directory`: canonical absolute host folder of the Reef installation. The local launcher supplies it via `REEF_INSTALLATION_DIR`; manual installs supply this config field or that environment variable. Config takes priority over environment. When explicit roots are absent, its parent becomes the default authorized root. It is never inferred from a candidate or container cwd.
- `allowed_project_roots`: optional canonical absolute directories allowed for UI registrations. Mount each at the identical absolute host/container path. Never `/`. An explicit list replaces the installation-parent default; explicit `[]` disables new registrations. Removing a root still used by a persisted registration blocks startup until the registration/root configuration is reconciled. This is an authorization allowlist, not a filesystem sandbox for external paths referenced inside Compose.
- `state_dir`: an absolute, private, persistent directory separate from application data.
- `volume_backup_directory`: optional private persistent archive folder; defaults to `state_dir/volume-backups`.
- `volume_backup_retention`: succeeded backups kept per volume identity, 1–100; omitted or 0 uses 7. See [Volume backups](VOLUME_BACKUPS.md) for mounts, limits and recovery.
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

The password must contain at least 16 characters. `QCU_PASSWORD_FILE` takes precedence over `QCU_PASSWORD`. Docker client and Compose plugin must be installed. The process uses `/var/run/docker.sock`; remote hosts are connected through [Reef agents](AGENT_SETUP.md), rather than remote Docker contexts. Docker subprocesses receive only `PATH`, `HOME`, the socket and Compose display settings, not the Reef password or arbitrary server environment variables. Put required interpolation values in project `.env` files rather than the server environment.

## Manual container installation

The `Dockerfile` builds a static Go executable and adds it to the official **Docker client image with Compose included**. No embedded daemon or additional services. Image versions can be changed with `GO_IMAGE` and `DOCKER_IMAGE`; verify them and pin digests for reproducible distribution. Release images target Linux amd64 and arm64. Check the target engine and Compose compatibility, especially on appliance-managed Docker installations.

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

Sessions expire 12 hours after sign-in and survive Reef restarts/rebuilds without extending that expiry. Sign out revokes the session durably. From v0.17.0, the configured password bootstraps `admin` once; changing that environment/file value does not change established account passwords. Use the account controls or [offline recovery](USERS.md#recover-administrator-access). Account password, permission, disablement and session-revocation changes invalidate that user’s cookies immediately through persisted account revisions. The private `state_dir/auth/sessions.json` stores token fingerprints, CSRF tokens and absolute expiry times in an authenticated registry (directory 0700, file 0600); it never stores the raw cookie or password. Keep this state with the installation. A damaged or unauthentic registry resets to no sessions. Unreadable, public or symlinked authentication state prevents startup. Sign-in/sign-out only succeed after their state is saved. Upgrading from shared login invalidates its old cookies; sign in as `admin` with the existing password. Mutations require a per-session CSRF token and matching origin. Login attempts are limited, CSP excludes third-party scripts, and logs are inserted as text. Graceful shutdown waits for operations; `stop_grace_period: 46m` prevents Compose from killing Reef too quickly. A forced kill, power loss or restart may interrupt an update. On restart the operation is marked interrupted with unknown Docker state; no recovery command runs automatically.

### Compatibility checks before production

- Build targets are **Linux amd64 and arm64**. ARM 32-bit devices are outside Reef’s supported targets. Validate filesystem permissions, Docker/Compose versions and mounts on the target installation.
- QNAP/Container Station may use an older engine than the image's Docker client (29.9.2 by default). Check API negotiation and Compose compatibility in an isolated environment matching the NAS, and adapt the client image if needed. Selecting an older image also requires maintenance and security review.
- Private registries: Reef does not manage credentials. Mount appropriate private Docker configuration at `/root/.docker` if required, accounting for credential helpers that may be absent. Do not place these secrets in the displayed Compose file.
- Local build contexts: Dockerfiles and contexts must remain accessible at the same paths. `pull` may fail if an image does not exist in a registry; Reef then stops before downtime. Use the [Builds view](USER_GUIDE.md#builds-and-builder-cache) to build saved project definitions separately. Apply update still pulls before recreation; test locally built-only workflows before relying on that action.
- `include`, `extends`, nested environment files and advanced features depend on the selected plugin. Custom CLI options and profiles are not configurable; services under profiles are not enabled automatically.
