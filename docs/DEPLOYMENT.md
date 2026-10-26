# Versioned Reef delivery

Prepared on `ci/tagged-deployment`. The selected target is the same server as
the recent applications, in a folder named `reef`, accessed over the LAN without
an external URL. Canonical paths are derived on that server from the installation folder;
no public hostname is needed. Release/deployment flags are intentionally unset. No
application deployment server has been contacted by this preparation.

## Delivery flow

1. Merge reviewed code with its intended stable `VERSION` into the protected
   default branch. MR quality runs once; master does not repeat those checks.
2. If `REEF_RELEASES_ENABLED=true`, a serialized release job creates the annotated
   `vMAJOR.MINOR.PATCH` tag using the CI job token and a bot identity. Existing tags
   are immutable: another source revision requires bumping VERSION. Superseded
   source pipelines skip release creation. A retry at the same revision triggers
   the tag pipeline again, without moving the tag.
3. The tag pipeline runs Go, frontend, mirror and delivery checks. Only after
   success does buildx push Linux amd64/arm64 manifests for `$CI_REGISTRY_IMAGE`
   and `$CI_REGISTRY_IMAGE/agent`, tagged with both the release and full revision.
   Stable `latest` aliases are also updated; deployments use explicit release tags.
4. If `REEF_DEPLOY_ENABLED=true`, a deploy runner calls the authenticated webhook
   with `X-Deploy-Version` and `X-Deploy-Revision`. The updater validates the stable
   tag, pulls only `reef`, checks the image revision label, then recreates only
   that service. Managed application containers and inventory agents are not
   updated by this job.
5. `/healthz` must confirm the requested version and full revision before the
   updater atomically persists `APP_VERSION` in its private `.env`. CI verifies
   the same endpoint externally after webhook success. A login redirect/HTTP 200
   alone is not accepted as a successful deployment.

Master/API mirror retries continue to work independently. A manual API/web master
pipeline creates a release only when both `REEF_RELEASES_ENABLED=true` and
`REEF_CREATE_RELEASE=true` are set. A protected tag always runs quality, but
registry publication/deployment require their respective activation flags.
Prerelease/build-metadata tags are outside this initial delivery path; they may
run version/mirror checks but do not automatically publish or deploy images.
No GitHub release objects or application-store distribution are created.

## CI configuration, after approval

Keep both activation flags protected with environment scope `*`. Deployment
secrets should also be masked and scoped to the `production` environment where
appropriate.

| Variable/setting | Purpose |
| --- | --- |
| `REEF_RELEASES_ENABLED=true` | Opt in to release tags and registry publication |
| `REEF_DEPLOY_ENABLED=true` | Separately opt in to the production webhook |
| `DEPLOY_WEBHOOK_URL` | `http://reef-deploy:9009/hooks/deploy`, over the existing Docker `proxy` network |
| `DEPLOY_WEBHOOK_TOKEN` | Dedicated strong token matching the updater |
| `HEALTHCHECK_URL` | `http://reef-app:8099/healthz`, reachable by the deploy runner on `proxy` |
| Job-token repository pushes | Enable in project CI/CD Job token permissions |
| Protected `master`, `v*` | Allow release-bot job-token operations and secret access |
| Runners `build`, `deploy` | Docker builder (Buildx supplied per job if missing) and webhook/health access |

GitLab registry build credentials use the job token. The target requires a
separate deploy token with `read_registry`, not a CI job token that expires.
Do not reuse an inventory-agent identity or Reef browser password as the webhook
token. Public/source mirrors never contain the completed `.env` or credentials.

The build job checks Buildx in its isolated Docker config. If it is missing,
`scripts/ensure-buildx.sh` copies the official plugin from a never-started
`docker:29.9.2-cli` container for the runner architecture. The temporary plugin
and helper container are cleaned up; no runner installation is modified.

A job-token Git push does not create a pipeline; the release script explicitly
triggers the tag pipeline afterward. Leave repository push permissions and
release flags unchanged until the source MR passes and activation is authorized.
No personal SSH key or developer signing passphrase is needed in CI.

## Operator bootstrap on the chosen host

The installation folder is named `reef` on the existing recent-app server. Its
absolute canonical path must remain on that host. Use Compose identity `reef`
and choose authorized project roots deliberately. The
provided examples are not executed automatically:

- Create `reef` beside the existing `flix_webapp` installation, under the same
  parent, then copy `compose.production.yaml`, `.env.production.example`,
  `deploy-agent/` and `scripts/prepare-deployment.sh` (keeping its scripts folder).
- From that server's `reef` folder, run `sh scripts/prepare-deployment.sh`.
  It computes canonical installation/parent paths, creates private state/config
  and independent random browser/deployment secrets, and preserves existing files.
  It starts no service. Fill only the target registry credentials in private
  `.env` and review the resulting paths/port before installation.
  Keep the whole parent directory writable by the updater so atomic `.env`
  replacement works; do not mount `.env` alone as a bind file.
- Review the generated `config.json`: `state_dir` is `/var/lib/reef`, `listen`
  is `0.0.0.0:8099`, projects start empty and inventory agents start disabled.
  The installation path comes from the generated environment value; its parent
  is the default authorized root and is mounted at the identical absolute path.
  For HTTP over the LAN, `secure_cookie` is false; if using internal HTTPS, set
  it true and preserve the exact Host. No external domain is required.
- The generated browser password is stored privately in `secrets/password.txt`
  without being printed by preparation. Use it for the initial login. Existing
  password/config/state are preserved; do not replace registrations or sessions
  when updating an already installed Reef.
- Use the same server address as Flix with Reef's `REEF_PORT` (8099 by default),
  for example `http://SERVER_LOCAL_ADDRESS:8099`. Host-port publication follows
  Flix's convention (`0.0.0.0` by default); `REEF_LISTEN_ADDRESS` can restrict it
  to a specific interface. Confirm the selected port is free before installation.
  No public domain, public URL, port-forwarding or reverse-proxy route is needed.
- Reuse Docker network `proxy` (`REEF_CI_NETWORK`) used by the existing deploy
  runners. The updater has no published host port. Runner-to-service traffic uses
  unique aliases `reef-deploy:9009` and `reef-app:8099`. Both services explicitly
  disable Traefik discovery; no HTTP router or public hostname is configured.
  Docker `external: true` means the network already exists on the server. No DNS,
  certificate, proxy, router, firewall or network is created by this code.
- Build/start the deployment service locally on the selected target, after
  installation approval: `docker compose -f compose.production.yaml --profile
  deployment up -d --build deploy-agent`. It uses Docker CLI/Compose plus the
  same webhook mechanism as the recent applications. This requires target-side
  access to the package/image registries during bootstrap.
- Confirm an image has been published for the selected initial APP_VERSION before
  first Reef installation. Validate Compose with `config --quiet`, then start only
  `reef` after approval. Existing installations must use the same project/service
  identity and private paths; never replace their registrations or state.

If inventory agents are enabled later, their existing protocol still requires a
trusted HTTPS central URL. Internal HTTPS is sufficient; a public Internet URL
is not required. Leave `agents_enabled` false until that separate setup exists.

The deployment service mounts the Docker socket and the installation directory
at `/app`; it is privileged in effect and must remain private. Inventory agents
are a different service (`cmd/reef-agent`), publish inventory only and are not
installed or rolled out automatically here. Publish their versioned images for
explicit operator installations later.

## Shutdown, failure and recovery

Reef retains `stop_grace_period: 46m` to allow active Compose operations up to
45 minutes to finish. The synchronous webhook/CI waits up to 55 minutes, with a
one-hour deploy job. Do not kill Reef early just to accelerate deployment.

Invalid token/tag/revision, a concurrent update, failed validation/pull or a wrong
image revision prevents service recreation. A process-local lock prevents direct
parallel webhook requests as well as CI's resource group. A killed updater can
leave its lock; inspect any ongoing operation before restarting the updater.
If runtime health fails after recreation, `.env` retains its previous APP_VERSION,
but the running container may already be the new version. This is not automatic
rollback. Inspect the updater/service logs and explicitly choose recovery or a
known release, without touching other containers or private project state.
A webhook timeout has an unknown outcome; the CI POST is not automatically retried.

`/healthz` exposes only `status`, `version` and `revision`, without authentication
or a Docker request. It indicates that startup/private-state restoration succeeded
and the HTTP service is responsive. It does not verify managed-project health or
prove that the Docker engine/registry is reachable. Native unversioned Go builds
report `dev`/`unknown`; release Docker builds inject exact metadata.

## Local checks

`sh tests/delivery.test.sh` verifies idempotent private-file preparation (including
credential preservation and symlink rejection), then uses temporary repositories
and fake Docker/HTTP commands. It checks immutable annotated tags, two multiarch builds, rejected
credentials/tags/images, serialized updates, exact health checks and settings
preservation on failure. It never contacts a registry or production host.
Go tests additionally verify health metadata and unchanged API authentication.
The production Compose recipe can be checked with disposable placeholder files;
never use a real target `.env` merely to validate the example locally.
