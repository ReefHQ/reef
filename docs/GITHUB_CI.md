# Public GitHub workflow audit

[← Back to Reef](../README.md) · [Development](DEVELOPMENT.md)

The public mirror includes a [Plumber](https://github.com/getplumber/plumber)
workflow in `.github/workflows/plumber.yml`. It examines GitHub Actions workflows
and selected repository settings, then produces findings and an A–E score when
collection is complete. GitLab remains Reef's source of truth: contribute changes
there, and let the audited mirror publish them to GitHub.

## When it runs

- Pushes to `master` that change `.github/workflows/**`, `.github/actions/**` or
  `.plumber.yaml`.
- Pull requests that change those same paths.
- Every Monday at **03:17 UTC**, to refresh repository/action metadata.
- Manually from **Actions → Plumber → Run workflow** on GitHub.

GitHub schedules can be delayed by its runners and require the workflow on the
default branch. The default branch of the public Reef mirror is `master`.
Application tests and versioned delivery continue through GitLab's existing jobs.

## Read the results

Open a Plumber run under **Actions** for its job summary, findings and the
`plumber-report` artifact. The official action retains artifacts for 30 days;
the bundle includes JSON, SARIF and pipeline inventories (PBOM/CycloneDX).
After a complete successful scan step, trusted runs upload SARIF to
**Security → Code scanning**. An incomplete/failed scan keeps its downloadable
reports and does not upload an empty result that could clear earlier alerts.
Fork pull requests
keep downloadable reports but skip the SARIF upload.

**Initial mode is advisory.** The configured target grade is A, but findings
below that grade produce warnings/reports rather than fail the job (`soft-fail`).
A green job therefore means the action completed, not that every control passed.
Usage, download, authentication, incomplete collection and other runtime failures
still fail; `continue-on-error` is not used. Missing permissions/data can leave
individual checks unknown; read collection warnings alongside the score. Once the
first complete baseline is reviewed, the grade can be made blocking through a
separate source change.

## Permissions and dependencies

The workflow uses GitHub-hosted Ubuntu 24.04 with a 15-minute deadline and cancels
superseded runs for the same branch/PR. It grants only `contents: read`,
`actions: read` and `security-events: write` to its job token. Checkout does not
persist that token in Git configuration. No personal token, deploy key, application
secret or extra repository secret is required. Repository administration reads
beyond the job token's scope remain unknown rather than prompting for a PAT.

Checkout is pinned to commit `3d3c42e5aac5ba805825da76410c181273ba90b1`
(v7.0.1). The official Plumber action is pinned to
`e9ad2dc6d6faa2dfd84395aac18e461284062640` (v0.7.16), with an explicit matching
binary version and checksum/build-provenance verification enabled. Its nested
artifact-upload actions are also pinned in that upstream revision. The separate
Code Scanning upload uses `github/codeql-action` commit
`6f2ac81f5cd81fe351a197ba0b00d91d03cd2aee` (v4.39.0). Review the new
action source, release and provenance when updating these references.

Plumber's built-in controls apply without a local override. Hosted score publishing
is disabled (`score-push: false`); no OIDC token write permission or external score
badge is enabled. Findings remain in the workflow's GitHub logs/artifacts and
trusted-context Code Scanning uploads. The scanner still reads GitHub metadata
and downloads its verified release from the upstream project.

## Validate and publish a change

Validate workflow syntax locally with [actionlint](https://github.com/rhysd/actionlint):

```sh
actionlint .github/workflows/plumber.yml
```

The mirror regression suite checks that this public workflow survives the same
history rewrite that removes private source CI/runtime files. The GitLab merge
and release pipeline publish the workflow through that mirror. The first complete
GitHub run, including Code Scanning/artifact uploads, must be checked after that
publication; local static checks cannot prove GitHub token permissions or hosted
API availability. No agent upgrade is needed for this CI-only change.

References: [official Plumber action](https://github.com/getplumber/plumber/blob/e9ad2dc6d6faa2dfd84395aac18e461284062640/action.yml),
[GitHub SARIF uploads](https://docs.github.com/en/code-security/how-tos/find-and-fix-code-vulnerabilities/integrate-with-existing-tools/upload-sarif-file).
