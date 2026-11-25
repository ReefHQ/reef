# Keep Reef's dependencies afloat 🌊

Renovate proposes dependency updates **in GitLab**, where Reef's source and review
process live. GitHub remains the audited public mirror. No Dependabot setup or
direct merge into the GitHub mirror is needed.

The Reef setup is active: its first full dry run and real proposal run passed on
November 24, 2026. Renovate created its dependency dashboard and merge requests
in GitLab. The setup steps below explain how to configure or recreate the runner;
new installations should start with a dry run before enabling real proposals.
Dependency updates still require review and an explicit release when shipping images.

## What the bot watches

| Dependency | How it is tracked |
| --- | --- |
| Main and deploy-agent Docker base images | Native Dockerfile manager, including the existing `ARG` defaults |
| GitHub Actions | Native manager for `.github/workflows`, preserving SHA pins |
| CI/launcher/Buildx helper images | Explicit custom manager for the image references in `scripts/ci.sh`, `local.sh`, `ensure-buildx.sh` and `renovate.sh` |
| Volume backup helper and integration fixture images | Explicit custom manager for the relevant `internal/app` Go files |
| Manual install example's Docker image argument | Same custom image manager |
| Plumber's verified binary version | Custom manager, grouped with its action reference |
| Renovate itself | Its exact image tag in `scripts/renovate.sh` |

Docker CLI references are grouped across base images/helpers/fixtures; Go images
are grouped across the build and launch/check tooling. Compatible GitHub Actions
updates are grouped, while the Plumber action and explicit binary version get their
own coordinated proposal. Keep the verified binary version aligned with the pinned
action: changing only one can break its checksum/provenance verification.

The regex manager is limited to named source files and known image repositories.
It does not scan private runtime directories, project Compose files, credentials,
user installations or the running Docker daemon. It is dependency maintenance for
Reef itself; Reef's project image checks remain a separate feature.

There are no external Go modules in the current `go.mod`. The embedded xterm.js
assets are **manual maintenance**: review upstream releases, replace the actual
assets, check licenses, record archive checksums in `web/vendor/README.md`, then
exercise consoles. Updating a version mention alone would leave the old code in
place. Likewise, maintainer documentation may need editing after dependency MRs;
Renovate does not rewrite every prose example or install unpinned APK packages.

## Review and release

Renovate opens at most **three concurrent MRs**, with at most two new MRs per hour.
It maintains a dependency dashboard issue in GitLab. Major updates require approval
there before a branch is prepared. **Automerge is disabled**, including at the bot
runner level. Minor/patch proposals still require normal review and CI.

Review the whole change: image variants and engine compatibility, both architecture
builds where applicable, action revisions and the Plumber binary, linked releases
and any dependency warnings. A configuration-only check does not prove that a new
Docker client or tool works with the NAS and remote agents.

For a dependency update that should ship a new Reef image, include the appropriate
SemVer bump in `VERSION`, update the README/changelog and check the release jobs.
The bot does not bump Reef's application version. An existing release tag remains
immutable: merging dependency changes with an unchanged `VERSION` does not publish
a new image. Tooling-only maintenance can use the ordinary default-branch mirror.

## Activate in GitLab

Use **Settings → Access tokens** on the source project to create a dedicated,
expiring project access token with **Developer** role and **`api`** scope. Its bot
needs permission to push `renovate/*` branches and create issues/MRs, without direct
push/merge access to protected master. Follow any project push rules when selecting
the bot identity. A CI job token or registry deploy token is not a substitute.

Add these in **Settings → CI/CD → Variables**, environment scope `*`:

| Variable | Setting |
| --- | --- |
| `RENOVATE_TOKEN` | Dedicated GitLab project token; protected, masked, hidden if supported; expansion disabled |
| `GITHUB_COM_TOKEN` | Dedicated GitHub token for read-only public action/tag metadata; protected/masked, expansion disabled; no package or repository write access |
| `REEF_RENOVATE_ENABLED` | Protected `true` |
| `REEF_RENOVATE_DRY_RUN` | Protected `full` initially; missing also defaults to `full` |

The GitHub token is required by Renovate for GitHub action dependencies even though
the source platform is GitLab. It is not the GHCR publisher token. Never paste
either token into source, a ticket, a terminal command or chat.

In **Build → Pipeline schedules**, create a weekly schedule for protected **master**:
suggested cron **`0 8 * * 1`**, timezone **Europe/Paris**. The schedule owner needs
permission to run pipelines on that protected branch. The `build` runner needs
Docker and HTTPS access to GitLab, GitHub, GHCR and the public image registries.
TLS checks remain enabled. Run the schedule once using its **Run** action and
inspect the `renovate` job. An ordinary web pipeline or MR does not run the bot.

The first full dry run logs proposed work without changing branches or MRs.
Confirm the repository, extracted dependencies, token permissions and lookup
results. Then set **`REEF_RENOVATE_DRY_RUN=off`** and run the schedule again to
create real proposals/dashboard. `extract` and `lookup` modes are also supported
for diagnosis; an invalid mode is rejected. The `off` wrapper value is translated
to an absent Renovate dry-run option, not passed as an invalid engine enum.

Only schedules on the protected default branch of `reef/reef` (project 102) are
admitted. Other repositories, MR/branch runs and CI debug tracing are refused.
The runner uses an ephemeral container without Docker socket, host credentials
or application mounts; token **names** are passed on the command line, not values.
Install scripts/plugins and post-upgrade commands are disabled. No persistent
cache is configured initially; monitor run duration and registry limits.

To pause maintenance, disable its pipeline schedule or set
`REEF_RENOVATE_ENABLED=false`. Existing proposals remain for review. Rotate the
tokens before expiry, retaining the bot identity where GitLab supports it.

## Validate a configuration change

```sh
sh scripts/renovate.sh validate
sh tests/renovate.test.sh
```

`quality-renovate` runs both on source MRs and version tags. Validation uses the
exact Renovate image version with no network or tokens. Tests check runner
admission and credential handling with fake Docker, then run the real extractor
offline against a synthetic repository. They verify image ARGs, inline helper/tool
references, action SHAs and both Plumber version references, with zero HTTP calls.
Configuration and fixtures are copied into temporary containers with `docker cp`;
no checkout bind mount is required. This supports shell runners inside containers
using the host Docker daemon even when their filesystem paths differ. Container
exit codes are checked and temporary containers are removed on success or failure.
The audited mirror is gated on that check too. These tests do not establish live
GitLab bot permissions, registry availability or actual update compatibility;
verify the first hosted schedule after merging.

References: [Renovate on GitLab](https://docs.renovatebot.com/modules/platform/gitlab/),
[Dockerfile ARG support](https://docs.renovatebot.com/modules/manager/dockerfile/),
[custom regex managers](https://docs.renovatebot.com/modules/manager/regex/).
