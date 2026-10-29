# Accounts and host permissions

[← Back to Reef](../README.md) · [Deployment](DEPLOYMENT.md) · [Agent setup](AGENT_SETUP.md)

Available from **v0.17.0**. Sign in with a username and password, then open your
username in the header to change your password or sign out everywhere. An
administrator with all-host access also sees user management in that dialog.

## Upgrade an existing installation

The first central startup creates username **admin**, using the existing
`QCU_PASSWORD_FILE` (or `QCU_PASSWORD`) value as its initial password. Old shared-
login cookies are invalidated; sign in again with the username. Existing projects,
registrations, agents and Docker resources are preserved. No agent upgrade or
re-enrollment is needed solely for user permissions. Do not downgrade central to
a pre-0.17 version while relying on these restrictions: older code implements
shared access and does not enforce individual-account permissions.

Once accounts exist, that bootstrap file no longer controls normal login. Changing
its value does not reset accounts or invalidate their sessions. The local
`./reef-local.sh password` command still prints the original bootstrap password;
it cannot reveal a password changed in the interface. Keep the file private and
preserve it for deliberate offline recovery.

## Roles and host scope

| Permission | Viewer | Admin with selected hosts | Admin with all hosts |
| --- | --- | --- | --- |
| Inventory, details, statistics and application logs | Assigned hosts | Assigned hosts | All hosts |
| Read managed Compose files, operation/build logs | Assigned hosts | Assigned hosts | All hosts |
| Read History and Alerts | Assigned hosts | Assigned hosts | All hosts |
| Save/apply/start/stop projects; image, volume, network and build operations | No | Assigned hosts | All hosts |
| Open/use container consoles; request agent updates | No | Assigned hosts | All hosts |
| Agent enrollment, rename/revoke; webhook settings; monitoring policy | No | No | Yes |
| Create/edit/disable/delete accounts; revoke another user’s sessions | No | No | Yes |
| Own password and sign out everywhere | Yes | Yes | Yes |

“All hosts” includes future hosts. Explicit assignments use stable host IDs,
including Local; renaming a host does not change access. An account with no
assignments has no Docker host access. Revoked/missing hosts do not fall back to
Local. Creating a host does not grant it to explicitly scoped users.

Read-only is an operation permission, not a secret-redaction guarantee. Compose
files and application/build/operation logs can contain secrets; grant read access
accordingly. Admin access to a Docker host is powerful: it permits commands and
changes to mounted data. A Docker/OS administrator of the machine running central
Reef can access its underlying account state. Host scope protects Reef’s HTTP
surface; it cannot isolate central Reef from that machine’s administrator.

Only all-host admins can administer global settings. Keep at least one enabled
all-host administrator: Reef rejects deleting, disabling or narrowing the last
one. Usernames are unique, lowercase, 3–64 characters (`a–z`, digits, `.`, `_`, `-`)
and remain fixed for an existing account. Up to 128 accounts are supported.

Use **Add user** to choose a role and explicit hosts or all-host access. Editing
permissions, resetting a password, disabling or deleting an account revokes that
account’s sessions. Account edits check the submitted revision, so a stale form
cannot overwrite a newer change. The acting administrator is checked again when
the account transaction commits. Password fields are write-only and cleared after
success/closing the dialog; no API returns passwords, salts, hashes or signing keys.

## Sessions, consoles and admitted operations

Sessions last 12 hours from sign-in, survive ordinary restarts and do not extend
expiry. There are at most 32 sessions per user and 256 centrally. Cookies retain
HttpOnly/SameSite protections, CSRF tokens and exact-origin checks. Logins are
rate-limited by IP, with at most four concurrent expensive credential operations.
Invalid username/password attempts use one generic error and stay on the login
page. Unknown usernames still perform the password KDF.

Every browser request checks the account’s current persisted revision and enabled
state. Role/scope/password changes and session revocation increment that revision.
Old cookies cannot regain access when an old password is reused. Active console
connections for the affected user are cancelled immediately. New relay dispatch,
console input and delayed response chunks recheck authorization. Session expiry
also prevents subsequent output/input. Already admitted detached Docker operations
are allowed to finish: interrupting them could leave an uncertain Docker state.
No mutation is replayed on revocation/reconnect. Closing a console cannot guarantee
termination of commands/background processes already started inside it.

The central route policy uses explicit paths/methods; hidden/disabled buttons are
only an interface aid. Agent machine credentials remain separate. Agents trust
central’s authenticated command channel rather than browser-supplied user claims;
central gates host/role access before dispatch. Existing agents keep their supported
features with no new capability for this account model.

Hosts are filtered before being returned. History host filtering happens before
grouping, counts and pagination; opaque encrypted page cursors are bound to the
user. Global webhook and agent enrollment/rename/revoke events are hidden from
restricted users so Local history cannot expose unrelated host names. Alerts only
returns authorized incident/coverage rows and hides global pending counts from
restricted users. Navigation/dismissal preferences are namespaced per user;
editor/log/console contents are not persisted in those browser preferences.

The action journal continues to use its existing event schema without claiming
individual actor attribution for legacy, automatic or remote operations. Individual
accounts do not retroactively turn that metadata into a complete user audit.

## Private state and backups

Central `state_dir/auth/` contains:

- `users.json`: schema/version, unique user IDs, password hashes/salts, role/host
  assignments, revisions and the random key signing per-user session state.
- `users.initialized`: an initialization marker. If an established users file
  disappears, startup fails instead of silently recreating a shared admin.
- `sessions.json`: authenticated session fingerprints, user/revision, CSRF tokens
  and absolute expiry times. Raw cookie tokens are never stored.

Keep the directory at 0700 and files at 0600. Preserve these files with the private
installation state; never commit, publish or edit them while Reef runs. User writes
are atomic and synced and reject external changes, unsafe modes, symlinks,
corruption, invalid schemas and excessive sizes. Account-state failure prevents
central startup rather than enabling a shared-login fallback. A bad signed session
registry resets to no sessions. Restoring an older auth backup restores older
account/security state: discard its session registry and deliberately recover/
revoke access before reopening the service.

Passwords use unique random 32-byte salts and **PBKDF2-HMAC-SHA256, 600,000
iterations**, via Go’s [standard-library implementation](https://pkg.go.dev/crypto/pbkdf2).
The work factor follows [OWASP’s PBKDF2 guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#pbkdf2).
Passwords must contain 16–1024 bytes; no password is recoverable from Reef.
Source builds now require Go 1.24+; the existing Go 1.27 container build works.

## Recover administrator access

Recovery is an **offline operator command**, not an HTTP endpoint. It takes the
same exclusive instance lock as normal startup and refuses to run while Reef uses
that state. It promotes an existing username to enabled all-host admin and resets
its password to the private bootstrap file/environment value, incrementing its
revision. It does not silently create users or reset Docker/project state.

For the supplied production Compose layout, from its installation folder:

```sh
# Let current operations finish, then stop only central Reef.
docker compose -f compose.production.yaml stop reef
# Uses the existing private QCU_PASSWORD_FILE mount; no password in argv.
docker compose -f compose.production.yaml run --rm --no-deps reef \
  -config /etc/reef/config.json -recover-admin admin
# Start the existing service after successful recovery.
docker compose -f compose.production.yaml up -d --no-deps reef
```

For a launcher installation, the helper preserves its exact Docker context,
engine and Compose project identity:

```sh
./reef-local.sh stop
./reef-local.sh recover-admin admin
./reef-local.sh up
```

Use the same Docker context as the installation. Replace `admin` with an existing
username if that initial account was deleted. Native installations use
`reef -config /private/path/config.json -recover-admin USERNAME` with the private
password-file environment. If account state is damaged, restore its private backup
first; do not delete auth files to bypass recovery.

## Scheduled authorizations (v0.19.0)

Admins can configure [Schedules](SCHEDULES.md) on assigned hosts; viewers read
metadata only. Saving a schedule binds it to that admin’s account revision. A
password/permission change, disable/delete or session revocation invalidates its
future grant; an authorized admin must edit and confirm the schedule again. No
password or browser session is stored for unattended execution.
