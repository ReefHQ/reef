# Changelog

## 0.5.0 — Unreleased

- Check managed-project image updates automatically and on demand by comparing
  registry manifests with deployed image contents for the matching platform.
- After a successful Apply update, remove replaced untagged images only when
  updated containers are running/healthy and no container still uses the image.

## 0.4.0 — 2026-10-27

- Restore the current page, managed project, Docker host, filters and scroll
  position after reloading the same browser tab.

## 0.3.3 — 2026-10-27

- Generate bootstrap secrets with OpenSSL on NAS systems without od, keeping
  existing installation files intact when preparation is retried.

## 0.3.2 — 2026-10-26

- Build release platforms separately with Docker's integrated builder and assemble
  registry manifests afterward, avoiding a nested BuildKit/runc incompatibility.

## 0.3.1 — 2026-10-26

Buildx provisioning succeeded; initial image publication then failed in the nested
BuildKit runtime while masking /proc/acpi. No server deployment occurred.

- Release builds supply Buildx from the official Docker CLI image when the shell
  runner lacks it, using an isolated job configuration without changing runners.

## 0.3.0 — 2026-10-26

Source tag created; initial image publication failed because Buildx was unavailable
on the build runner. No server deployment occurred.

- Prepared opt-in stable-tag delivery: immutable release tags, multiarch registry
  images, authenticated deployment webhook and exact version/revision health
  checks. Production target and CI activation remain operator-configured.

- Shared Reef SVG favicon on the login, main application and console pages.

- GPLv3 licensing and a GitLab-to-GitHub mirror for ReefHQ/reef (ReefLabs), with
  private session notes removed from every public branch/tag and its history.

- Multi-host inventory through outbound HTTPS agents, with one-use enrollment,
  durable identities/revocation, host selection and stale inventory indicators.
  Agents are disabled by default; remote logs, consoles and Compose operations
  remain unavailable. Local workflows retain their existing configuration.

## 0.2.0 — Previous development baseline

- Prevented the console frame from flashing white while its document loads.

- Login sessions survive Reef restarts/rebuilds until their original 12-hour
  expiry; logout and password changes revoke them durably.

- Removed the offline demo mode and its launcher/fixtures; Reef now uses Docker
  only, with explicit configuration/password and strict origin validation.

- Consistent input/select heights with original filter width proportions;
  local Reef installations use `restart: unless-stopped`, including migration
  of existing UI service definitions on launcher `up`.

- Read-only container/application stdout/stderr logs, with service selection,
  stream filters, bounded history and quiet automatic refresh.
- Persistent multi-session container console inside Reef, with explicit Connect,
  sh/bash selection, terminal resizing and session ownership checks. Accessible
  from Containers, project rows and the project editor, with a service picker.
  Sessions survive navigation, tab switches, panel reduction and resizing;
  configurable defaults are 8 per login and 16 per instance.
- CI validates SemVer in `VERSION` and checks release tags against that file.
- Container details from inventory and managed projects, with consistent
  Logs/Console action ordering and individual service/container access.

## 0.1.0 — Initial development baseline

- Local Docker inventory, Compose project registration, editing, operation logs,
  save/update/start/stop workflows and system/light/dark themes.

These headings do not imply that Git tags or published releases exist.
