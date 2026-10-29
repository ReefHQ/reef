# Feature roadmap

The user approved implementing these features in this order. Each feature is
prepared on a dedicated branch, with a SemVer bump and Local/remote parity.
The user signs commits and pushes; do not mix later features into a pending branch.
Use `Refs #IID` in commit messages and merge request descriptions, without automatic
closing keywords. After validation, move the issue to `Done` (removing `Doing`/
`To Do`), then close it explicitly. Merge alone does not complete publication or
remaining validation.

1. [#22](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/22) — **Network management — merged and published (0.13.0).**
   Inventory/details, local bridge creation, targeted unused-network deletion,
   batch cleanup and live container connections, with host isolation and protections.
2. [#23](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/23) — **Live container statistics — merged and published (0.14.0).** CPU, memory, network and block I/O on
   Local and agents; visible-page polling, bounded sampling and no disk history.
3. [#24](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/24) — **Action history in Reef — merged and published (0.15.0).** Consult operations/results in the UI, complementing
   external webhooks; keep secrets, terminal content and application logs excluded.
4. [#25](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/25) — **Monitoring alerts — merged and published (0.16.0).** Agent disconnection, unhealthy containers and restart
   loops, with recovery notifications, deduplication and configurable thresholds.
5. [#26](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/26) — **Multiple users and access permissions — merged and published (0.17.0).** Individual accounts, read-only/admin
   roles and per-host authorization enforced centrally and on every route/console.
6. [#27](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/27) — **Volume backup and restore — merged and published (0.18.0).** Host-local archives, explicit downtime review,
   retention and restoration into a new volume, with private storage and interrupted-run guidance.
7. [#28](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/28) — **Scheduled operations — merged and published (0.19.0).** Image checks and explicit cleanup policies with review,
   host availability, non-overlap and no automatic replay of uncertain mutations.
8. [#29](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/29) — **Agent diagnostics — merged and published (0.20.0).** Last connection, capabilities/version and actionable
   communication errors without exposing credentials or private transport data.

The source remains authoritative for which features are actually available.
Items 1–8 are merged and published.

## Follow-up features

The user approved #31 before #30, each on its own branch with review and publication
between features.

1. [#31](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/31) — **Sign-in persistence — merged and published (0.21.0).** Investigate frequent sign-outs, isolate installation cookies and offer opt-in 30-day sessions alongside the default 12 hours.
2. [#30](https://gitlab.renaudbaivier.com/reef/reef/-/work_items/30) — **Standalone container management — implemented, awaiting review and publication (0.22.0).** Start/stop/restart, image update with configuration preservation and reviewed recovery, and deletion retaining volumes by default; Local/remote parity.
