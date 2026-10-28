# Changelog

## 0.9.0 — Unreleased

- Build saved managed Compose services explicitly, with no-cache/base-image pull
  options, bounded logs and persistent per-project build history.
- Inspect default-builder cache and review exact reclaimable records for targeted
  cleanup, retaining mutable/in-use/changed records and shared storage safeguards.
- Add a host-scoped Builds page with isolated log refresh and builds-v1 agent
  negotiation; serialize builds/cache operations with project/storage mutations.

## 0.8.2 — 2026-10-27

- Accept the full image selection in one cleanup request, process it in automatic
  batches and report live per-image progress instead of stopping at 100 images.
- Scan inventory once per batch, recheck exact images before removal and retain
  container/identity protections; negotiate large selections with remote agents.

## 0.8.1 — 2026-10-27

- Show visible loading/refreshing indicators across inventory lists, preserve
  existing rows and layout during refresh, and distinguish unavailable inventories.

## 0.8.0 — 2026-10-27

- Show host-scoped total counts for Images and Volumes in the main navigation,
  refreshed in the background independently of page filters.

- Manage Docker volumes on Local and upgraded remote agents: inventory, details,
  creation, explicit deletion and reviewed batch cleanup of unused volumes.
- Protect running/stopped container references and changed volume identities;
  confirm permanent data loss and serialize volumes/images/Compose mutations.
- Negotiate volumes-v1 per agent connection and restore the Volumes view on reload.

## 0.7.0 — 2026-10-27

- Manage Docker images on Local and remote hosts: inventory/details, pull,
  add/remove tags, selected deletion and reviewed cleanup of unused images.
- Retain images used by stopped/running containers, recheck changed references,
  and report bounded background operations per host without force/global prune.
- Negotiate images-v1 with upgraded agents and restore the Images view on reload.

## 0.6.1 — 2026-10-27

- Remove Docker Desktop as a launcher requirement on Linux/macOS. Accept local
  Unix-socket Docker contexts and mount the selected native Linux engine socket,
  including rootless Docker, while retaining installation engine identity checks.

## 0.6.0 — 2026-10-27

- Extend outbound agents with remote logs, consoles, project registration, Compose
  editing/actions, image checks and targeted cleanup using the shared Reef engine.
- Keep editor drafts, operation state and console routing isolated by host; preserve
  agent identities when upgrading and negotiate capabilities with older agents.

## 0.5.0 — 2026-10-27

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
