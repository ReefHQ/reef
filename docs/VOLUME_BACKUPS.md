# Volume backups ⚓

[← Back to Reef](../README.md) · [Volumes](USER_GUIDE.md#volumes) · [Agent setup](AGENT_SETUP.md)

Available from **v0.18.0**, on Local and agents advertising **volume-backups-v1**.
Upgrade central first, then the agents that need this feature. Existing agent
identities, CA and configuration stay in place; no re-enrollment is needed.

## Make a backup

1. Select the host, open **Volumes**, then **Backup** beside a volume or **Backups**.
2. Choose a volume and select **Review backup**.
3. Read the list of running containers that Reef will temporarily stop. Confirm
   only when application downtime is acceptable.
4. Wait for the result. A **succeeded** record means the gzip/tar archive was
   completely captured, validated and checksummed, and restart commands succeeded.

Reef prepares its isolated Docker helper before stopping applications. It records
each stop intent before sending it to Docker, requests a shutdown with a 20-second
grace period, and refuses to archive when shutdown fails, is killed with exit 137,
or a consumer remains running. Only containers that were running in the confirmed
preview are restarted. Already stopped containers remain stopped. Restart is also
attempted after an error, using a separate timeout from the archive operation.

The preview covers the exact volume fingerprint and container identities/states.
If they change, review again. Stop/start uses existing container IDs; Reef does not
run Compose down/up or change Compose files. Clean shutdown is application-specific:
this is a filesystem archive, **not a database dump or a guarantee of database
consistency**. Follow your application's backup procedure when it needs more than
stopping all volume consumers. Coordinate other Docker clients and external writers;
Reef cannot lock them out. Named-volume recreation is not atomic across Docker
clients, so avoid concurrent storage changes during backup or restoration.

## Restore without overwriting

Select **Restore to a new volume** on a succeeded backup and provide a **new,
unused volume name** on the same host. Review the host/name and confirm.

Reef checks the archive's SHA-256 and extraction paths before creating a normal
local volume. It verifies that volume's creation label/fingerprint and refuses
other container references before extraction. Existing volumes are never emptied
or overwritten. Failed restorations leave their new volume for inspection; Reef
does not delete potentially useful data automatically or attach applications to it.

After a successful restore, verify the data, permissions and application behavior
before changing your project's volume configuration. Reef leaves that choice to you.
Restoration also has a durable record, including its destination. Deleting that
record keeps the restored Docker volume.

## Where the archives live

Every host keeps its own archives. There is no implicit copy to central Reef or
another host, and archive bytes are not exposed by the UI or agent command relay.
By default they live in **`state_dir/volume-backups`**, inside the existing private
persistent state mount. This also applies to an agent's managed state directory.

Optional central or agent config fields:

```json
{
  "volume_backup_directory": "/var/lib/reef-backups",
  "volume_backup_retention": 7
}
```

Merge these fields into the existing config; do not replace the rest of it. Restart
the relevant Reef service after a config change. The directory must be an absolute,
canonical path, private (0700), persistent and free of symlink ancestors. For a
container installation, mount the desired persistent folder at that path inside
Reef or the agent. It need not match the host path: archive I/O uses the Docker
client's filesystem. Do not choose a folder inside a volume you intend to back up.
One process may use a backup directory at a time; separate hosts need separate
storage. Existing archives are not moved when the configured path changes.

Archives and metadata use private 0600 files. Archive names are random IDs ending
in `.tar.gz`; a neighboring `.json` identifies the volume, creation fingerprint,
checksum, timestamps, stopped container IDs and result. `.partial` files are
incomplete and cannot be restored from Reef. Keep the whole folder when moving
or independently copying backups; do not edit metadata while Reef is running.
These files contain application data and can contain secrets. They are private,
but **not encrypted**. Protect the disk and any independent copies accordingly.

## Retention and limits

- Keep **7 succeeded backups per volume identity** by default; configure 1–100.
  Retention runs only after a new successful backup, and never prunes the last
  successful copy after a failed attempt. A recreated volume has a new identity.
- Failed/interrupted archives and restoration records stay visible until explicit
  deletion. Review and recover applications before deleting an interrupted record.
- Maximum **20 GiB compressed per archive**, **100 GiB expanded**, and **100 GiB
  of archive/partial files per host folder**. A new attempt reserves room for its
  maximum size before stopping applications. Actual free disk space may be lower.
- At most 100 container consumers per backup, 10,000 catalog records, with 50 shown per page. Archive validation is
  bounded to one million entries. Operations have a 30-minute deadline; restart
  and helper cleanup have separate bounded attempts.

Keep an independent copy off the host: these backups do not protect against loss
of the host or its storage. Restore is currently limited to archives produced by
Reef on that host; importing/downloading archives and cross-host transfers are not
part of this version. From central v0.19.0, [Schedules](SCHEDULES.md) can run backups after explicit
approval of the volume identity and temporary-stop policy. No schedule is enabled
by default.

## Supported data and recovery

Only ordinary Docker **local** volumes with no driver options are eligible.
Bind mounts, plugin/NFS/CIFS volumes and Reef administration volumes are excluded.
The helper uses `docker:29.9.2-cli`, resolved to an immutable local image ID before
backup. It has no network, Docker socket or host bind mounts. Export mounts the
source read-only; restore uses a private helper layer for the archive.

Regular files, directories, numeric UID/GID, Unix permission modes, safe symlinks
and hard links are supported. ACLs, extended attributes, sparse-file layout,
devices, sockets and FIFOs are not supported. Unsupported file types or unsafe
extraction paths prevent a succeeded backup. Check NAS ACL requirements before
using filesystem archives as your application's recovery method.

If Reef is forcibly stopped or loses power, a pending record becomes
**interrupted** at the next startup. It is not replayed or reported as successful.
Inspect the recorded container IDs and restart stopped applications manually as
appropriate. An orphan helper may be named `reef-volume-backup-<record ID>` or
`reef-volume-restore-<record ID>`; inspect it before removing it. Do not run a
second backup until you have recovered the applications and explicitly deleted
the interrupted backup record. Interrupted restoration destinations require
manual inspection before use; choose another new name for another attempt.

Admins may back up, restore and delete archives on assigned hosts. Viewers may
read records only. History and webhooks receive action metadata/results, without
archive bytes or filenames from inside the volume. Other image/volume/network/
build/Compose mutations managed by Reef are excluded while a backup operation runs.
Monitoring treats these operations as host maintenance. A missing/corrupt/private-
permission failure in backup storage disables this feature, while other Reef
features remain available; resolve storage issues before retrying.
