# Reef 🌊

**Your Compose projects, made simple.**

A home for your Docker containers, Compose projects and remote hosts. Check what’s
running, read logs, open a shell and ship an update — without losing your place.

Built with Go’s standard library. One application process, embedded assets,
no database and no CDN.

**In development: 0.22.3** · [Changelog](CHANGELOG.md) · [Roadmap](docs/ROADMAP.md) · [GPLv3](LICENSE)

> **Upgrading from a version before 0.17.0?** Sign in with username **`admin`**
> and **your existing Reef password**. Reef now uses individual accounts, so you’ll
> need to sign in again after the upgrade. You can then change your password and
> manage users from your username in the header.
> [Accounts and upgrade details](docs/USERS.md#upgrade-an-existing-installation)

## 🚀 Get your feet wet

From the Reef checkout, with a running local Linux Docker engine, Docker CLI,
Compose v2 and Buildx:

```sh
./reef-local.sh up
./reef-local.sh password
```

Open the URL printed by the first command and sign in with the generated
password, using **admin** as the initial username. Reef prefers **8099** when available. Linux Docker Engine works;
on macOS, use a local Docker runtime. Docker Desktop is optional. You don’t need
Go, Node.js or Python installed to launch Reef this way.

Want to open Reef from another computer on your network?

```sh
./reef-local.sh up --lan
```

Use the **server IP printed by Reef**, not `0.0.0.0`, in your browser.
A fresh launch without a flag binds to `127.0.0.1`; later launches remember your
choice. `./reef-local.sh up --local` switches back to this machine only.

| Need to… | Run |
| --- | --- |
| Start or rebuild Reef | `./reef-local.sh up` |
| Find its URL and status | `./reef-local.sh status` |
| Read the initial admin password | `./reef-local.sh password` |
| Stop Reef | `./reef-local.sh stop` |

Account passwords can be changed from your username in the header. The command
above prints the bootstrap password, so it won’t reveal a password changed later.

**Staying a while?** Check **Remember me for 30 days** when signing in. Leave it
off for the usual 12-hour session. [Session details](docs/USERS.md#sessions-consoles-and-admitted-operations)

Your configuration and accounts survive restarts in the private `.reef-local/`
folder. Stopping Reef leaves your applications running. Rebuilding can recreate
Reef itself, so let active operations finish first.

[Local setup details](docs/LOCAL_SETUP.md) · [Deploy on a server](docs/DEPLOYMENT.md)

## 🧭 What’s on deck?

| In Reef | What you can do |
| --- | --- |
| **Containers** | Inspect containers, read logs, open consoles and start, stop, update or delete standalone containers. |
| **Projects** | Connect existing Compose projects, edit and validate their main file, save backups, start, stop and apply updates. |
| **Images** | Inspect usage, pull images, manage tags and review unused-image cleanup. |
| **Volumes** | Inspect references, create volumes, back up their data, restore into a new volume and review cleanup. |
| **Networks** | Inspect addressing and usage, create bridge networks and connect or disconnect running containers. |
| **Builds** | Build saved project definitions, read build results and review default-builder cache cleanup. |
| **Statistics** | Watch CPU, memory, network and disk I/O, with pause/resume and per-container measurements. |
| **Alerts** | Detect agent disconnections, unhealthy containers and restart loops, with configurable delays and recovery notifications. |
| **Accounts** | Give each person a username, role and selected hosts; manage passwords and sessions from the header. |
| **Schedules** | Run image checks, volume backups and reviewed age-based cleanup daily or weekly, with an explicit timezone. |
| **History** | Follow actions and results across hosts, with search, filters and operation details. |

Console sessions live in a bottom panel, so you can keep navigating Reef while
working in several shells. Refreshing the page restores your view, filters,
selected host and scroll position; live shells and unsaved drafts are not restored.
Switch servers and keep your current section; Reef loads the new host’s data.
In Builds, choose a managed project for its Reef build history; **Refresh cache**
reads the selected host’s default builder.
Light, dark or system theme? Your choice.

The **Manage** panel refreshes container status without moving your reading position.

[Explore the usage guide](docs/USER_GUIDE.md) · [Standalone containers and safe updates](docs/STANDALONE_CONTAINERS.md)

## 🛠️ Bring your existing projects

Reef starts by discovering containers. **Seeing a project does not give Reef
permission to manage it.**

1. Open **Projects**, then **Manage this project** beside the Compose project.
2. Check its existing folder, Compose file order and any interpolation env files.
3. Review and confirm. Its editor and Compose actions become available.

Your files stay where they are. Project mounts must preserve the same absolute
paths on the host and inside Reef. Registration uses configured authorized roots;
by default, these come from the parent of the Reef installation directory.

**Save file** validates and saves without restarting services. **Apply update**
pulls and recreates the project, checks running/healthcheck state and removes
eligible replaced images. Expect downtime during recreation. Image-update checks
also run automatically, with a manual check available; applying an update stays
your decision.

[Project workflow and recovery](docs/USER_GUIDE.md#working-with-existing-projects) · [Paths and configuration](docs/CONFIGURATION.md)

## 🌍 One Reef, several hosts

Connect remote Docker hosts through agents with outbound HTTPS connections.
Select a host and use its projects, logs, consoles, images, volumes, networks,
builds and statistics. Features depend on the agent’s negotiated capabilities;
an older agent shows an upgrade message when needed.

Prepared agents can be updated from **Manage hosts** to the same stable version
as central Reef, preserving their identity and configuration. Open **Manage hosts →
Diagnostics** for connection freshness, negotiated features and recent observations
with troubleshooting steps — without running a probe on the agent.

[Set up or upgrade an agent](docs/AGENT_SETUP.md) · [Agent diagnostics](docs/AGENT_DIAGNOSTICS.md)

## 📬 Keep track without keeping a tab open

**History** records action metadata even with webhooks disabled. Starts and
results are grouped by operation, with up to **2,000 events or 30 days** retained.
It’s a best-effort journal; storage failures or queue overflow are reported.

**Schedules** takes the night shift: daily/weekly checks, volume backups and
reviewed cleanup policies, with explicit timezones and visible results. Missed
slots are skipped; uncertain operations wait for your review.

**Alerts** watches hosts in the background once enabled. Configure disconnect,
health and restart thresholds, then follow incidents and collection coverage.
Opening and recovery notifications use the existing webhook destination.

**Webhooks** sends action results to Discord or a generic JSON endpoint. Configure
it once, send a test and check delivery status from Reef. These events contain
selected metadata, without terminal contents, application logs or credentials.

[Scheduled operations](docs/SCHEDULES.md) · [Monitoring alerts](docs/MONITORING.md) · [Action history](docs/ACTION_HISTORY.md) · [Webhook setup and payloads](docs/WEBHOOKS.md)

## ⚓ A few things to know

- Docker access gives Reef substantial authority over the selected host. Keep
  the UI on a trusted network or behind an authenticated HTTPS reverse proxy.
- Give each person their own account: **Viewer** for read-only access or **Admin**
  for changes, limited to assigned hosts. Viewers can read Compose files and logs,
  which may contain secrets. [Accounts and recovery](docs/USERS.md).
- Cleanup previews name their targets, but deleting a volume still deletes data.
  Use [volume backups](docs/VOLUME_BACKUPS.md) for compatible local volumes; keep an independent copy off the host.
- Logs and consoles can show application secrets. Review them before sharing.
- Compose updates can interrupt services. Reef does not automatically roll back
  or replay interrupted operations.

## 📚 Pick your next stop

| Looking for… | Start here |
| --- | --- |
| Local startup, LAN access and Docker contexts | [Local setup](docs/LOCAL_SETUP.md) |
| Everyday actions, limits and recovery | [Usage guide](docs/USER_GUIDE.md) |
| Config fields, mounts and manual installation | [Configuration](docs/CONFIGURATION.md) |
| Server installation and versioned CI delivery | [Deployment](docs/DEPLOYMENT.md) |
| User accounts, host permissions and access recovery | [Accounts](docs/USERS.md) |
| Timed checks, backups and cleanup policies | [Schedules](docs/SCHEDULES.md) |
| Volume archives, retention and restoration | [Volume backups](docs/VOLUME_BACKUPS.md) |
| Remote hosts and agent upgrades | [Agent setup](docs/AGENT_SETUP.md) |
| Architecture of remote operations | [Multi-host agents](docs/MULTI_HOST_AGENTS.md) |
| Development checks and versioning | [Development](docs/DEVELOPMENT.md) |
| Changes and upcoming work | [Changelog](CHANGELOG.md) · [Roadmap](docs/ROADMAP.md) |

Made by **ReefLabs** · Public mirror: [ReefHQ/reef](https://github.com/ReefHQ/reef).
Licensed under [GPL-3.0-only](LICENSE). Bundled xterm.js components retain their
MIT licenses in `web/vendor/`.


