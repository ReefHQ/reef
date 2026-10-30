# Developing Reef

[← Back to Reef](../README.md) · [Roadmap](ROADMAP.md) · [Changelog](../CHANGELOG.md)

Reef uses Go’s standard library with embedded HTML, CSS and JavaScript.
Docker CLI/Compose performs authorized project actions. No external Go modules,
framework, database or CDN is required. Vendored terminal assets live in
`web/vendor/` with their licenses.

## Checks

Run the same checks as CI using disposable containers (Docker required):

```sh
sh scripts/ci.sh go
sh scripts/ci.sh frontend
sh tests/delivery.test.sh
sh tests/mirror.test.sh
```

The Go check runs race tests, vet and Linux amd64/arm64 builds for central Reef
and its agent, plus a launcher build. Frontend checks run the JavaScript tests,
syntax checks and version validation. The quality containers have no network or
Docker socket. Delivery and mirror suites use isolated fixtures rather than
real deployments.

For host-side checks, use Go 1.24+ compatible with `go.mod` and a current Node.js LTS:

```sh
go test -race ./...
go vet ./...
node --test tests/*.test.cjs
node scripts/check-version.cjs
```

The default Go suite uses temporary directories and fake executors. Opt-in
integration tests must use disposable resources and leave existing projects alone.
`QCU_TEST_DOCKER_CONFIG=1 go test -race ./...` checks temporary Compose fixtures
with an installed Compose CLI and a nonexistent socket.

## Find your way around

| Path | Purpose |
| --- | --- |
| `cmd/reef/` | Central application entry point |
| `cmd/reef-agent/` | Remote agent entry point |
| `cmd/reef-local/` | Local launcher entry point |
| `internal/app/` | HTTP/auth, configuration and operation handlers |
| `internal/agents/diagnostics.go` | Bounded, fixed central observations and curated per-agent snapshots |
| `internal/docker/` | Docker inventory and resource operations |
| `internal/schedules/` | Explicit timezone policies, durable claims, restart recovery and latest results |
| `internal/volumebackup/` | Private archive catalog, checksums, validation and retention |
| `internal/accounts/` | Individual credentials and host authorization |
| `internal/history/` | Central action metadata journal |
| `internal/local/` | Local setup and engine checks |
| `web/` | Embedded interface and themes |
| `scripts/` | Bootstrap, CI and delivery tooling |
| `tests/` | Frontend and delivery checks |
| `examples/` | Configuration and installation examples |

`.reef-local/` holds private live configuration, credentials and state. `work/`
holds ignored development fixtures and reports. Keep both out of commits, images
and public archives.

## Versions and delivery

Reef follows Semantic Versioning. [`VERSION`](../VERSION) is the source of truth;
release tags use the matching `v` prefix. A version in the file alone does not
mean it has been published. Published releases are immutable.

- Before 1.0, minor releases can introduce incompatible changes; patches contain
  backward-compatible fixes.
- From 1.0, breaking changes increment MAJOR, compatible features MINOR and fixes
  PATCH. Incrementing MAJOR or MINOR resets lower components to zero.
- Document migration requirements for configuration, persisted state or HTTP/API
  changes in the changelog. Related unreleased work may share a version.

Quality jobs run on merge requests and tags. Ordinary branch pushes and the
post-merge master push do not repeat them. When enabled, CI creates the stable
release tag from VERSION, then checks/builds/publishes it. See
[versioned delivery](DEPLOYMENT.md) for the exact gates and deployment flow.

GitLab is the source of truth. The GitHub mirror rewrites public history to
remove private development notes and session identifiers; its commit IDs differ.
Do not push unsanitized source history directly to the public mirror.

The public GitHub workflow audit uses a pinned Plumber release, with advisory
findings and explicit runtime failures. Its triggers, reports, permissions and
validation are documented in [GitHub CI](GITHUB_CI.md).

## Keep the README useful

Update the README as user-facing features and setup behavior change. Keep it in
English, readable and welcoming: a short introduction, working quick start,
concise feature overview and links to detailed guides. Small marine touches are
welcome; implementation details and session reports belong in dedicated docs.
Clearly distinguish available features, unreleased work and roadmap ideas.
Update the linked guide, changelog and source version together when relevant.

`REEF_TEST_BACKUPS=1 go test ./internal/app -run TestDockerVolumeBackupsIntegration -v`
uses the Docker CLI/socket to create uniquely named disposable volumes/containers.
It checks content, UID/GID, modes, links, active-only restart and stop/export/restart errors,
then removes only its own fixtures. It is excluded from ordinary socket-free CI.

`REEF_TEST_SCHEDULES=1 go test ./internal/app -run TestDockerScheduledBackupIntegration -v`
exercises a central scheduler claim through the backup APIs using only a uniquely
named disposable volume/container; validates source preservation, restart, archive
integrity and no duplicate slot, then cleans its own fixtures.

Standalone container management and its opt-in disposable Docker integration test
are documented in [STANDALONE_CONTAINERS.md](STANDALONE_CONTAINERS.md). Set
`REEF_TEST_STANDALONE=1` only against a local test engine with the Docker CLI; the
test creates uniquely named fixtures, simulates registry delivery with local image
retagging, and verifies real recreation, retained mounts/ports/IP and rollback.

Read-only Buildx regression: compile the app test binary for Linux with CGO disabled,
then run `TestDockerReadOnlyBuildxIntegration` with `REEF_TEST_READONLY_BUILDX=1`
in the release Docker CLI image using `--read-only --tmpfs /tmp --network none`.
Mount the binary and a **synthetic** `/root/.docker/config.json` read-only, plus
the test engine socket. The test reads cache metadata and Compose configuration;
it performs no build, pull or cleanup. It checks registry configuration preservation
and removal of per-command private Buildx metadata. Never mount real credentials.


Image patch-version integration checks are opt-in and separate from socket-free CI:

- `REEF_TEST_HUB_TAGS=1 go test ./internal/app -run TestLivePublicGitlabPatchTags -v`
  reads public Docker Hub metadata only; no Docker engine or credentials are used.
- Compile the Linux app test binary with CGO disabled and run
  `TestComposeImageVersionProposalIntegration` in the release Docker CLI image
  with `REEF_TEST_IMAGE_PROPOSAL=1`, `--network none` and no engine socket. It uses
  private disposable Compose fixtures to check literal edits, shared anchors,
  overrides and variables. Source files stay unchanged; temporary candidates are
  removed. No build, pull, container start or image removal occurs.

The tag API uses the fixed Docker Hub namespace/repository endpoint. See
[Docker Hub tags](https://docs.docker.com/reference/api/hub/latest/operations/ListRepositoryTags/).
Pagination is constructed locally and incomplete scans cannot establish currentness.

Central Reef update checks use a fixed public GitHub tags endpoint with no token.
`reef_updates_test.go` uses an injected HTTP transport (no network); browser
coverage lives in `tests/reef-updates.test.cjs`. See [update checks](REEF_UPDATES.md)
for cadence, stable-tag policy and limits. Agents do not perform these checks.

## Shared interface conventions

Use [the UI conventions](UI_CONVENTIONS.md) when adding or changing a screen.
Reuse the shared control height, page actions and sticky dialog heading instead
of adding one-off sizes. Keep loading, empty and unavailable states distinct;
refreshes must preserve drafts and clearly identify stale data.
