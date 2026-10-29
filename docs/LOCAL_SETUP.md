# Local Docker setup

[← Back to Reef](../README.md) · [Server deployment](DEPLOYMENT.md)

Prerequisites: a running local Linux Docker engine with its context selected, Docker CLI with Compose v2 and Buildx for source builds, and standard shell utilities. Linux Docker Engine and local macOS Docker runtimes are accepted; Docker Desktop is optional. Python is not required. Go is not required on the host: the shell bootstrap compiles the small Go launcher in a temporary, network-isolated Go container and caches only its executable under `.reef-local/tools/`. No Docker socket or private runtime data is mounted into that compiler container. Node.js is only used for optional frontend development tests.

```sh
cd reef
./reef-local.sh up
./reef-local.sh password
```

The first command builds the real image, starts Reef and prints its loopback URL. The second prints the initial private password for username `admin`. After changing an account password in Reef, this command still prints the bootstrap value, not the new password. A fresh installation prefers port 8099 if available; use the URL printed by the launcher. `up` reuses the existing configuration, files and password; it does not reset the playground. It may recreate the Reef UI container after a code change, so allow current operations to finish before running it.

The default Containers view reads **all containers, including stopped ones**, from the connected Docker engine. It displays only names, images, states/status and Compose project/service names. Environments, commands and arbitrary labels are not exposed. Discovery does not authorize operations or open other projects’ files. A fresh installation starts with no managed projects. Existing installations keep their configuration. Project bind mounts keep identical absolute paths on the host and inside Reef. The private runtime contains `config.json`, `compose.json`, state, a password file and cached local tooling. Existing managed project data is preserved. `.reef-local/` is excluded from Git, the Docker build context and delivery archives. Do not publish its password or state.

```sh
./reef-local.sh status
./reef-local.sh stop
```

Reef's UI container uses `restart: unless-stopped`. The local launcher sets this
for new installations and applies it to existing Reef service definitions on
`up`, before Compose validation and recreation. Other local launcher commands do
not change the policy. The manual installation example uses the same policy.

`stop` stops only the Reef UI; it does not stop projects managed through Reef. Preexisting containers remain view-only until explicitly registered. Images and build layers remain cached.

The launcher checks that the selected context uses a local Unix socket and reaches a Linux Docker engine with a stable ID. It does not require any engine name or vendor. It refuses SSH/TCP contexts, `DOCKER_HOST`/`DOCKER_CONTEXT` overrides and a different engine on later runs; select the context with `docker context use` first. It never switches contexts automatically. Native Linux engines mount their selected socket (including rootless sockets) into Reef at `/var/run/docker.sock`. macOS VM runtimes use the engine's internal `/var/run/docker.sock`; their filesystem sharing must include the checkout and authorized project paths. Keep separate checkouts/private runtimes for different engines.

To allow access from another computer, choose LAN access explicitly:

```sh
./reef-local.sh up --lan
```

This publishes Reef on all IPv4 interfaces (`0.0.0.0`) and prints the server's
network URLs. Open the server IP shown, rather than `0.0.0.0`, on the other
computer. The existing port, password, configuration and managed state are kept.
The choice is saved in `.reef-local/compose.json`: later `up` calls without an
option reuse it. To return to access from this machine only:

```sh
./reef-local.sh up --local
```

Both options apply only to `up`. Short and long Compose TCP publications with one
explicit host port for Reef's port 8099 are supported. Ambiguous/multiple or
random-port custom mappings require manual review. First launches without a flag
stay local; CLI output explains the current access mode and the command to change
it. Changing access recreates only Reef; wait for current operations to finish.

The launcher supports Linux and macOS on amd64/arm64 and defaults to loopback.
After `up` and `status`, it reads the actual published port from Docker. If an
existing `.reef-local/compose.json` publishes on `0.0.0.0` or `::`, the printed
URLs use the server's available network-interface IPs and include loopback access.
Explicit bindings are printed as configured, including their actual host port.
The launcher does not broaden network exposure automatically. Network access
still depends on host routing and firewall settings. For a server deployment with LAN access and CI updates, use the production Compose setup below. Runtime acceptance is tested with Linux Engine/rootless and macOS Desktop/Colima/OrbStack context fixtures; the real local engine validation used Docker Desktop.

Reef scripts and Docker commands launched by Reef use an English diagnostic
locale (`LANG=C`, `LC_ALL=C`, `LANGUAGE=en`), independently of the host language.
This applies to local startup, preparation, deployment and CI tooling. Container
console sessions, application logs and managed service environments keep their
existing language settings.
