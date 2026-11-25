# When is Reef ready for 1.0?

**Proposal for maintainer review, not a release announcement.** Reef remains on
0.32.1. This document defines the decision; it does not certify that the checks
have passed or authorize a deployment.

Reef can become **1.0.0 when the existing core is reliable, recoverable and has a
clear compatibility promise**. We do not need to finish every future feature.
The maintainer approves the scope and signs off the evidence below before the
version changes.

## The core we intend to stabilize

One central Reef manages Local and remote Linux Docker hosts, through the same
interface and the agent capabilities actually negotiated:

- Containers and Compose projects: discovery, reviewed registration, supported
  lifecycle/update/delete actions, project and individual Compose-container
  restarts, Compose editing, logs and consoles.
- Images, volumes, networks and builds: inventory, details and supported actions,
  with explicit cleanup targets and protection for referenced resources.
- Individual accounts and host scope, persistent sign-in, agent enrollment,
  diagnostics and opt-in agent upgrades.
- Action history, monitoring, webhooks, schedules and supported volume archives.
- Public central/agent images, installation without cloning, and project/Reef
  update-availability notices while the browser is closed.
- A consistent English interface with visible progress, useful failures and
  recovery guidance, including narrow layouts and both themes.

Keep the documented boundaries: Docker access is host administration; viewers
can read potentially sensitive logs/Compose files. Backups cover eligible local
volumes, not every driver or NAS ACL. Compose profiles/advanced definitions and
standalone recreation retain their existing limits. Automatic new-tag suggestions
retain the documented public Docker Hub patch-series policy. Manual tag browsing
covers the documented Docker Hub, LSCR and compatible Registry V2 paths; these
are separate workflows. Statistics describe containers, not every host process
or the cause of host swap usage. External
registry availability, zero downtime, automatic application rollback and
unlimited resource counts are not promised.

Kubernetes, Swarm orchestration, application-level backup consistency, external
identity providers, a general public automation API and automatic central Reef
updates are not prerequisites. A limit may stay if it is documented and safe;
a defect in an advertised supported path must be fixed or explicitly scoped.

## What 1.x compatibility means

[SemVer 2.0.0](https://semver.org/spec/v2.0.0.html) requires a defined public
contract. For Reef, the proposed contract is the documented operator interface:
configuration fields and environment variables, launcher/installation commands,
Compose installation model, persisted user/project/agent state, agent protocol
and capability meanings, and documented generic webhook fields.

Within 1.x, preserve existing documented settings and supported workflows.
Compatible optional additions belong in a minor release; compatible fixes in a
patch. Document deprecations in a minor release before planned removal in a
major release. A required configuration rename, removal of a supported capability,
or incompatible change to this contract requires a major release and migration
instructions. Losing or corrupting operator data is a blocking defect in any
version, not something a major bump excuses. Emergency security restrictions must still
be documented honestly rather than silently changing this promise.

The browser's `/api/*` endpoints, DOM selectors, Go packages and private state
file layout are implementation details, not a public SDK. That does not permit
breaking the shipped interface, losing operator data or bypassing agent checks.
Private state may migrate internally if the supported upgrade preserves it.

Upgrade central first, then agents. New features may require a new capability;
missing capabilities must give explicit guidance and never fall back to Local.
Record tested central/agent version pairs, including the previous stable agents
used before migration. Version numbers alone must never enable a capability.
Do not promise every historical agent works with every 1.x central version or
that downgrading migrated state is safe.

## Release gates

Every row needs dated evidence applicable to the selected candidate. Reuse existing
tests and operational results when their versions, scope and unchanged relevant
behavior are established; retest affected paths and evidence gaps. Do not rerun
every check solely because a candidate was named. “Implemented” or a green
pipeline alone is not acceptance.
Use the [qualification record](V1_QUALIFICATION.md) and keep secrets out of it.

| Gate | Pass condition | Evidence to record |
| --- | --- | --- |
| Core workflows | The scenario matrix below passes on Local and a compatible remote agent. Failures and unsupported cases are explicit. | Versions, scenario results and redacted logs. |
| Upgrade and recovery | Fresh installation, upgrade from the preceding stable version, restart and restoration of private Reef/agent state preserve accounts, projects and identities. Admin recovery works in a disposable installation. | Source/target versions, preserved identities/state and restore results. |
| Isolation and permissions | Viewers cannot mutate; scoped admins cannot cross hosts; stale replies cannot act on a replacement selection. Reconnect/restart does not replay an uncertain action or console input. | Automated checks plus isolated multi-host/failure scenarios. |
| Data safety | Used resources are retained, destructive targets require review, backup restoration verifies fixture data and interrupted work remains visibly uncertain. | Real disposable Docker fixture results; no production cleanup for testing. |
| Platform coverage | Advertised Linux amd64 and arm64 images actually start and perform a smoke workflow; Linux Docker Engine, NAS mounts and macOS local launcher behavior are recorded. | Exact OS/CPU, Docker/Compose/Buildx versions and runtime results. Cross-compilation alone is insufficient. |
| UI and diagnosis | Desktop/narrow, light/dark, keyboard, long dialogs, loading/empty/error and refresh behavior pass visual review. Fresh remote Builds/history/cache checks reproduce and resolve symptoms when present, or record the verified expected behavior when absent. | Browser versions, screenshots and tested reproduction conditions. |
| Quality and delivery | Candidate CI checks pass; central/agent versioned images, health and public source match. GHCR images are anonymously accessible for both architectures; original build revisions stay compatible across registries despite rewritten mirror commit IDs. Review current workflow/security findings. | Both CI systems' run links, image digests, health version/revision and public source tag. |
| Operational stability | Accept the operational evidence below, with no open blocking defect and a published list of non-blocking limitations. | Start/end, environments, incidents, fixes and maintainer sign-off. |

A **blocking defect** includes data loss/corruption, credential exposure or host
permission bypass, an incorrect destructive target, false success/automatic
replay after an uncertain mutation, an unusable advertised core workflow, or an
upgrade/recovery that loses access or state. A cosmetic defect with a usable
workflow can remain as a tracked limitation. Uninvestigated reports affecting
these guarantees stay unresolved; they are not silently reclassified as cosmetic.

## A small, repeatable scenario matrix

Use disposable projects, volumes, networks and accounts on an explicitly chosen
test engine. Preserve real installations. Record Pass/Fail/Not run for each
applicable Local and remote case; mark central-only cases as such.

| Scenario | Check |
| --- | --- |
| Install and access | Fresh public-image Compose install without clone/login, Linux Engine and LAN URL, local-only default, existing-installation restart, NAS canonical paths/aliases and launcher with a local macOS runtime. |
| Accounts and sessions | Bootstrap login, changed-password behavior, viewer/admin host scope, no-assignment account, ordinary/remembered session persistence, revocation and offline admin recovery. |
| Host navigation | Switch hosts and refresh in every section; keep the section/filters, separate same-named projects, preserve drafts and reject late responses for the previous host. |
| Projects and updates | Register authorized paths, reject unauthorized paths, validate/save a fixture Compose file, browse a supported registry tag, review the unsaved draft and apply it. Exercise whole-project and individual Compose-container restarts. Failed pull/config validation must precede downtime. |
| Standalone containers | Start/stop/restart and supported recreation preserve configuration and volumes; unsupported recreation explains why before stopping anything; deletion retains volumes by default. |
| Logs and consoles | Read both streams, open multiple shells, navigate while connected, handle reconnect/revocation and close cleanly. No duplicate input or automatic shell reopening. |
| Resource management | Pull/tag/cleanup fixture images, create/remove unused volumes/networks, retain referenced resources and confirm exact destructive targets. |
| Builds | Build a saved fixture project, inspect its history/logs and the selected host's default-builder cache, including read-only runtime paths. The menu count is managed projects, not historical builds. |
| Backup and recovery | Archive eligible fixture data, restore to a new volume and compare contents/ownership. Exercise interrupted work, active-consumer stop/restart and private Reef/agent-state recovery. A database requires its own consistency procedure. |
| Background features | Review History, webhook categories/delivery failure/recovery, availability notices without opening the UI and monitoring observations. Check unchanged-update deduplication across restart, new targets and failed checks. Test a due schedule, missed slot, restart and uncertain-result acknowledgement without duplicate work. |
| Agent upgrade | Central-first upgrade, previous stable agent's supported features, explicit missing-capability message, opt-in agent update, identity preservation, disconnect and failed update result. Verify the documented GitLab/GHCR repository choices and matching source-revision checks. |
| Interface | Narrow/desktop, light/dark, keyboard/focus, long scrollable panels, explicit pending/empty/error states and retained stale data. |

Existing automated/opt-in checks are listed in [Development](DEVELOPMENT.md).
Do not enable every Docker integration flag against a production socket or treat
this matrix as authorization for real deployments, password changes or deletion.

## Stabilize, then decide

Use existing sustained operation as evidence; **a new seven-day wait is not a
mandatory release gate**. On November 25, the maintainer reported weeks of stable
use. This supports operational confidence for the workflows actually used, but
exact installed versions, environments, dates and coverage have not been recorded.
It does not establish unexercised restoration, permission or platform scenarios.

Identify the selected stable 0.x version, image digests and environments, then map
existing operational and test evidence to the gates. The maintainer can accept
that history where it applies. If coverage or stability remains uncertain, use
**seven consecutive days (168 hours)** on central plus a remote agent as a default
observation window, adjusted to the actual gap. Record the reason and outcome;
this duration is a project guideline, not a SemVer requirement.

A runtime fix or newly found blocker requires targeted regression checks and
renewed observation appropriate to the affected behavior; do not mechanically
restart an unrelated full matrix. A documentation-only change, Renovate proposal
or CI-only maintenance does not alter running images. A runtime/base dependency
change included in the candidate needs relevant requalification. Record ordinary
errors and recoveries too; elapsed time alone does not exercise every workflow.

Current delivery accepts stable tags only. Use a frozen, versioned 0.x image as
the qualification candidate. Do not publish `1.0.0-rc.1` through the existing
release/deploy jobs: prerelease delivery would need a separate reviewed change.

Once all gates have evidence, the maintainer reviews the record and authorizes
the 1.0 release. Prepare VERSION/README/changelog and migration/compatibility notes
on a dedicated branch. Run the release checks again, then verify the published
images, mirror and deployed health version/revision. Record the release commit
separately from the qualified candidate and review any differences; version-only
metadata is expected, new runtime changes require requalification.

Do not move existing tags, automatically roll back migrated state or close
qualification work merely because the version-definition document was merged.
Issue #40 is complete when these criteria are reviewed and agreed; satisfying
them and publishing 1.0 are separate work. Record the criteria decision and the
accepted operational evidence or remaining observation explicitly when the documentation is reviewed; no
candidate is selected and no qualification period starts merely by merging it.
