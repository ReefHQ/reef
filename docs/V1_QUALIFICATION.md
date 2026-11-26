# Reef 1.0 qualification record

**Current decision: not yet qualified.** This is a partial evidence record;
individual results below do not qualify every platform or the complete release.
See [the reviewed release criteria](V1_READINESS.md). Remaining work is tracked
in [#52](https://gitlab.renaudbaivier.com/reef/reef/-/issues/52); #40 completed the
criteria definition, not the release qualification.

Current published baseline: Reef 0.32.1, reviewed November 26, 2026. Recovery
executions below used 0.32.0; their exact scope and versions remain explicit.
This is not a frozen or qualified 1.0 candidate.

## Candidate and agreed scope

| Item | Record |
| --- | --- |
| Maintainer / decision date | Criteria accepted by maintainer merge of !65 on November 26; final release decision pending. |
| Criteria decision | Reviewed scope/eight gates accepted via !65. Reuse applicable evidence; no mandatory fresh seven-day wait. |
| Operational feedback | Maintainer reports weeks of stable use on November 25; exact versions, dates, environments and exercised workflows not yet recorded. |
| Frozen 0.x version / source commit | Not selected |
| Central and agent image digests | Not recorded |
| Previous stable version / upgrade path | Not selected |
| Advertised platforms and tested central/agent pairs | Not recorded |
| Accepted operational history / additional observation | Stable-use report recorded; applicability and any remaining observation to assess. No new window started. |
| Final release commit and differences from candidate | Not applicable yet |

## Joint review — November 25, 2026

| Point | Confirmed evidence | Remaining question |
| --- | --- | --- |
| Installed version | Maintainer reports the latest central and agents. This feedback preceded publication of 0.32.1. | Exact installed health/version/revisions and image digests have not been independently collected. |
| Routine upgrades | Maintainer confirms central and at least one agent were upgraded while retaining accounts, projects and connections. | Record tested version pairs; this is real upgrade evidence, not an offline-restoration test. |
| Operational stability | Weeks of stable use reported across a QNAP and two Intel Ubuntu servers. | QNAP architecture, exact OS/Docker versions and exercised workflows still need mapping. No production ARM runtime is established by this feedback; isolated ARM64 evidence is recorded below. |
| Restoration | Disposable remote volume backup/restore passed on Ego on November 25: exact file content/SHA-256, UID/GID 1234:1235 and mode 0640 preserved. | Isolated central account/registry and actual agent identity restoration passed on November 26, detailed below. Full configured-project/registration/schedule/archive recovery, active-consumer recovery and broader filesystem cases remain outside these manual checks. |
| Builds report | Safari inspection of the NAS Local host confirms Builds 6, uptime-kuma selected, 0 buildable services, no recorded builds, and a populated cache table with 242 records / 16.5 GiB cumulative after Refresh cache. Maintainer also reports no recorded builds for miam shopping. | Local cache loading is verified. Read-only Safari check of remote Vision / deluge, including Refresh cache, returns 0 records / 0 B without a visible error; Builds count is 3 and deluge has 0 buildable services and no recorded builds. This verifies remote UI refresh/empty response, not independent Docker cache completeness or a real build/history round trip. Counter wording remains ambiguous. |
| Remote Ego cache | Initial Refresh cache failed with a generic Buildx error. Maintainer checks: agent 0.32.0 / revision 780a080ddc107a5c242b65d000ff35f75dc62b7b; Buildx works with normal and isolated environment (920 JSON rows / 318519 bytes, normal run 1s). Subsequent Safari refresh succeeds: 920 records / 22.6 GiB cumulative, consistent with Docker 24.23 GB decimal. | Successful real remote cache read confirmed. Initial failure is intermittent/unexplained; no code fix, restart or cleanup performed. A cold-start/deadline explanation remains a hypothesis. No real build/history round trip checked. |
| Real builds and history | Published 0.32.1 central/agent ARM64: successful scratch builds and controlled failures through Local and TLS-relayed APIs; artifact contents and persisted logs/history verified after central/agent restarts. | Uses one local engine with separate fixture projects/state volumes. Does not certify two physical servers, RUN steps, registry base pulls or browser log rendering. |
| Existing automated coverage | Release pipeline 14488 jobs 20198 (Go) and 20199 (frontend), plus delivery/mirror/build/deploy, rechecked successful on November 25. | These are release-baseline results, not newly executed tests or proof of actual restoration/platform behavior. |

Relevant existing regressions include the permission matrix and delayed-result
revocation (`internal/app/permissions_test.go`), persisted accounts and recovery
(`internal/accounts/store_test.go`), remembered sessions after restart
(`internal/app/remember_test.go`), enrollment/revocation/capability isolation
(`internal/agents/registry_test.go`), and updater preservation/exact reconnect/
no replay (`internal/agentupdate/worker_test.go`). Updater tests inject Docker
responses; they complement the maintainer's real upgrade experience.

`TestDockerVolumeBackupsIntegration` is explicitly opt-in and skipped by ordinary
CI. Its presence is not a passed restoration result. It creates owned fixtures,
checks restored content/ownership and failure behavior, and removes its own data.
Private-state disaster recovery is a separate check from volume archive recovery.

## Remote volume restoration — passed November 25

Maintainer performed the workflow on Ego (Intel Ubuntu, agent 0.32.0 / original
revision `780a080ddc107a5c242b65d000ff35f75dc62b7b`), using only named disposable
local volumes and a network-disabled Docker CLI helper.

1. Created `reef-v1-restore-1795647215-525556`, containing `check.txt` with
   `Reef restore check` followed by a newline, owner 1234:1235 and mode 0640.
2. Backed up through Reef; record reported succeeded at 23:54:37, November 25
   (displayed local time). Small archive size rounded to 0.0 MiB.
3. Restored through Reef to the new volume
   `reef-v1-restored-1795647215-525556`; record reported succeeded at 23:55:30.
4. Read the restored volume through a read-only mount. Content matched exactly;
   source and restored SHA-256 both were
   `ebf16ed299e5af920c7b22a3dbdc8ea1234c81829f0563f2aa5e092df9aa5480`;
   numeric ownership and mode both matched `1234:1235 640`.

**Pass for this scenario.** It does not establish database consistency, NAS ACLs,
links, active-consumer restart behavior or central/agent private-state disaster
recovery. Source/restored volumes and backup record have not been cleaned up.
Maintainer reported the backup panel jumping during progress refresh. This was
addressed separately in 0.32.1 / #51, with desktop fixture verification and
publication evidence below. Narrow/mobile visual acceptance remains pending.
No application code changes are included in this documentation branch.

## Isolated private-state restoration — passed November 26

Used the published **0.32.0** central and agent images with original revision
`780a080ddc107a5c242b65d000ff35f75dc62b7b`, Linux ARM64 under local Docker.
All accounts, passwords, cookies, machine credentials, certificates and volumes
were generated solely for these fixtures. No host bind mounts or Docker socket
were attached. Ephemeral container roots were writable for fixture file copying;
this run does not validate production read-only-root behavior.

### Central account and agent-registry state

- Started central with no network and a private anonymous state volume. Created
  an admin, a Local-only viewer and a synthetic enrolled peer using shipped APIs.
- Confirmed viewer mutation denial, stopped central, copied its private state,
  then restored into a different container and new volume. Removed only the
  copied session registry, following the documented older-backup recovery policy.
- Confirmed existing account IDs, roles, host scopes and passwords match; the
  discarded old browser session is rejected and viewer mutation is still denied.
- Confirmed the same synthetic machine credential authenticates after central
  restoration; the already-used enrollment token cannot be replayed.
- Ran the shipped offline `-recover-admin` command against the stopped restored
  fixture. Verified unchanged user ID, intended all-host admin recovery, old
  password rejection and invalidation of the user's previous session.

### Actual agent identity state

- Ran another disposable central, a TLS gateway with a generated trusted test
  certificate and the actual published agent on a dedicated **internal-only**
  Docker network. No ports published, external network or Docker socket.
- Enrolled the agent, stopped it and copied its state into a new agent volume.
  The replacement received the trust certificate but **no enrollment token**.
- Confirmed it reconnects as the same single host; private identity bytes and an
  explicitly synthetic managed-state marker match after restoration.
- Docker inventory is intentionally unavailable without a socket. This does not
  test inventory recovery, an agent upgrade or real registered project recovery.

Image config IDs used: central
`sha256:033e97b8212089b032e1dbaf0596a401c60c419dd547b456de990d552434d14f`, agent
`sha256:b1d8b08021cca43a8a8dc7c931f0b38f0fbe70d4e1fa92f935e99f9981defd1f`.
All owned containers, anonymous volumes and the internal network were removed
by the fixture cleanup. Production installations were untouched. These scoped
passes do not mark the entire upgrade/recovery or platform gates complete.

## Real builds and history — passed November 26

Used the actual published **0.32.1** central and agent, original revision
`856eaa4b1a217dc89b2a931d638f8ee03b689106`, Linux ARM64 under local Docker.
Engine **29.9.3** (linux/arm64), Compose **v5.6.1**, Buildx **v0.38.1**
(`0dbe87f36ed472fe36b6796c3f70b4210a731fc5`) were recorded.
Both ran with read-only roots, writable private anonymous state/fixture volumes
and `/tmp`. Their engine access was the local Docker socket. The TLS agent and
central communicated over an internal-only fixture network with a generated
trusted certificate; no listening ports were published or credentials mounted.
All accounts and enrollment credentials were generated only for this test.

Two uniquely named Compose projects each had one buildable service. Each used
`FROM scratch`, a fixture label and `COPY marker /marker`; `.dockerignore` allowed
only the Dockerfile and marker. No RUN step, base-image pull or application
startup was requested. Each build created only a uniquely named fixture tag.

For Local, then for the real agent through central's relay:

1. GET `/api/builds/project` identified the saved revision and buildable service.
2. POST `/api/builds/action` with confirmation/CSRF, exact revision/service,
   no-cache enabled and pull disabled returned 202. The operation completed.
3. The actual Docker image existed with the fixture ownership label; reading
   `/marker` from a never-started disposable container verified the exact contents.
4. Replaced only the fixture Dockerfile with a deliberately missing COPY source.
   A second accepted build failed visibly; the earlier successful history survived.
5. Read both `/api/builds/history` and `/api/builds/log`. After central restart
   for Local and agent restart for the remote path, exactly the same two build
   identities remained and both log SHA-256 values matched their pre-restart values.
6. Central's login remained valid, the agent retained its identity, host histories
   remained separate, and no extra build or application deployment appeared.

**Pass for these Local and relay scenarios.** Central and agent shared one local
engine, so this does not certify two independent physical hosts. It does not
cover registry authentication/base pulls, RUN steps, browser log layout, cache
pruning or interruption during an in-flight build. No production project was used.
Fixture containers, anonymous volumes, tagged images and the internal network
were removed. No broad cache prune was performed; small build-cache entries may
remain in the local default builder.

Image config IDs: central
`sha256:97cac7a650158defb028cab3f8097abe1d916dd5c71cee9d2bf4421af83a9ca0`, agent
`sha256:944476f8611ab4ab9d00c1c74ae48c4339db75f681e83e2107a6c3b0ce442f23`.
These are observed test images, not a selected 1.0 candidate's frozen digests.

## Environment and scenario evidence

For each test environment, record OS/architecture, Docker/Compose/Buildx/browser
versions, central/agent versions and a non-sensitive host label. For every row in
the readiness scenario matrix, record Local/remote **Pass / Fail / Not run**,
date, reproduction steps and redacted evidence. A central-only case can be N/A on
an agent, with a reason. No runtime result may be inferred from a successful build.

Keep credentials, Compose secrets, cookies, account/agent state archives and
private host paths out of published evidence. Store recovery material privately;
record only the outcome and non-sensitive resource identities here.

| Gate | Current qualification status | Evidence / next action |
| --- | --- | --- |
| Core Local/remote workflows | Not run as a consolidated candidate recipe | Existing features and earlier fixture tests are a baseline, not a candidate-wide pass. |
| Upgrade and recovery | Scoped 0.32.0 private-state restoration passed | Fresh disposable central, cold-copy accounts/agent registry restore, offline admin recovery and actual agent identity reconnect passed November 26; real upgrades reported by maintainer. Full project/registration/schedule/archive recovery and exact upgrade pairs still need assessment. |
| Isolation and permissions | Candidate qualification pending | Repeat existing regression checks and isolated cross-host scenarios. |
| Data safety | Remote basic volume restoration passed; broader coverage pending | Manual Ego fixture verified content, ownership and permissions; applicable existing tests and remaining recovery cases still need assessment. |
| Platforms | ARM64 startup/auth/recovery evidence added; workflow matrix incomplete | Published central/agent 0.32.0 Linux ARM64 images ran under local Docker during recovery checks. QNAP and Intel Ubuntu operation reported separately. Additional published 0.32.1 ARM64 Local/agent scratch builds passed below; this is not every Docker workflow. |
| UI and diagnosis | #51 desktop fix verified and published; broader review pending | Backup dialog no longer moves in the Safari desktop fixture; focus/scroll/draft retained. Narrow-viewport check blocked by unavailable native window access. Other visual cases remain pending; actual API build/history round trips passed below. Remote cache observations are recorded above. |
| Quality and delivery | 0.32.1 publication verified | GitLab quality/build/deploy/mirror and GitHub image publication succeeded. Anonymous central/agent amd64/arm64 metadata match the release. This does not establish candidate-wide runtime or recovery. |
| Observation and blockers | Stable-use feedback recorded; applicability pending | Map existing use to the selected version and workflows; agree additional observation only for gaps. Assess open blockers. |

## Existing evidence to reuse carefully

These observations are real, but qualify only their recorded version and scope.
Map them to the selected candidate and record its digests; repeat checks where
behavior changed or the evidence does not cover the gate.

- **Maintainer feedback, November 25:** Reef has been used for weeks and is
  reported stable. This is accepted as operational feedback, without inventing
  an exact start date, installed version, incident history or scenario coverage.
  A new seven-day wait is not automatically required. Restoration, authorization
  and untested platform behavior still need their own evidence.

- **0.32.0 delivery, November 3:** [GitLab release pipeline 14488](https://gitlab.renaudbaivier.com/reef/reef/-/pipelines/14488)
  passed quality/build/deploy/mirror; [GitHub image publication 41637283645](https://github.com/ReefHQ/reef/actions/runs/41637283645)
  succeeded. Anonymous GHCR manifest/config reads verified central/agent Linux
  amd64/arm64, version 0.32.0 and original source revision
  `780a080ddc107a5c242b65d000ff35f75dc62b7b`. This is not native runtime evidence
  for both architectures, a fresh-install exercise or a restored-state test.
- **Renovate, November 24:** [real schedule pipeline 16601](https://gitlab.renaudbaivier.com/reef/reef/-/pipelines/16601)
  created dependency MRs after a full dry run. Its configuration and runner tests
  passed. This proves maintenance activation, not compatibility of every proposed
  dependency upgrade.
- **Workflow maintenance, November 25:** [GitLab master pipeline 16723](https://gitlab.renaudbaivier.com/reef/reef/-/pipelines/16723)
  and [GitHub Plumber run 45659244913](https://github.com/ReefHQ/reef/actions/runs/45659244913)
  succeeded after the action updates. The artifact reports Plumber 0.7.23,
  A/100 and zero findings; SARIF upload/post-processing succeeded. This does not
  establish application security or an absence of all vulnerabilities.

## Backup dialog fix and 0.32.1 delivery — November 26

[#51](https://gitlab.renaudbaivier.com/reef/reef/-/issues/51) completed after
[MR !64](https://gitlab.renaudbaivier.com/reef/reef/-/merge_requests/64), source
`8af8de55e08bca39d01a4a7dda0263b22d6a61a3`, merged as
`856eaa4b1a217dc89b2a931d638f8ee03b689106`. MR pipeline 16752, master 16753
and [release pipeline 16754](https://gitlab.renaudbaivier.com/reef/reef/-/pipelines/16754)
passed. [GitHub image publication 45722844528](https://github.com/ReefHQ/reef/actions/runs/45722844528)
succeeded. Anonymous GHCR config reads verified central and agent v0.32.1 for
amd64 and arm64 with that original source revision.

All 226 frontend tests pass. Four new regressions fail against the previous
implementation. The real Safari desktop synthetic fixture showed about 10px
of loading-induced movement before the fix and zero after it, preserving record
focus, scroll and restoration draft through running/completed updates. An extra
390px viewport check was blocked by lost browser window access; it did not pass.
No production backup/restore/delete was performed during fix verification.

Runtime source comparison from 0.32.0 to 0.32.1 shows only `web/style.css` and
`web/volume-backups.js` changed; `cmd`, `internal`, `Dockerfile` and `go.mod` are
unchanged. Reuse relevant older evidence with this scope, without relabelling
0.32.0 recovery executions as 0.32.1 tests or assuming rebuilt images are identical.
No selected candidate, complete visual sign-off or 1.0 authorization follows
from this patch publication. #51 was moved Done before explicit closure.

## Defects and limitations

Record each finding as a GitLab issue with severity, affected supported scenario,
reproduction, version/environment, outcome risk and verification after the fix.
List open blockers separately from accepted limitations with a usable workaround.
The maintainer reviews any proposed non-blocking classification.

Do not assume the historical remote Builds symptom is still present or fixed:
verify its selected project, history origin, default builder, agent capabilities
and actual error/result. Retain the diagnosis before deciding the gate status.

## Release decision

- Criteria review: maintainer accepted the reviewed definition through !65 on
  November 26. Applicable evidence can be reused; no mandatory fresh seven-day
  wait. #40 is Done/closed; #52 tracks remaining qualification.
- Every gate passed with linked evidence: pending.
- Open blocking defects: not assessed for a candidate.
- Accepted limitations and upgrade/recovery notes: pending.
- Operational evidence: maintainer reports weeks of stable use; mapping to the
  selected version/workflows and any additional observation remains pending.
- Maintainer authorization to publish 1.0.0: not given.
- Final pipelines, central/agent image digests and public mirror: not applicable.
- Deployed health version/revision and post-release smoke: not applicable.

This record must be updated from observed results. Merging the documentation or
closing #40 does not mark the release gates Pass.
