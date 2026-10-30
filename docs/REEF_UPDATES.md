# Reef update notifications

Open **Reef updates** in the header to see the running version, latest public
stable version, changelog and last check attempt. When a newer version exists,
the button shows **Reef X.Y.Z available**. It stays visible across host changes.
Every signed-in user can read it, including viewers without Local host access.
Only a global administrator can **Check now**.

Central Reef checks at startup and every six hours. Browsers read the cached
result once a minute; they do not call GitHub. Manual checks share the same cache,
coalesce concurrent work and have a one-minute cooldown. No token is required.
The checker sends anonymous HTTPS requests only to the public ReefHQ/reef tags API
on api.github.com. It sends no host inventory, account information or credentials.
Your server's network address is visible to GitHub as with any outbound request.

Only stable `vMAJOR.MINOR.PATCH` tags count. Version components are compared
numerically across major, minor and patch releases. Prerelease tags are ignored;
development/prerelease running builds show an explicit comparison-unavailable
message. A running version ahead of the mirror is not called out of date.

The scan is bounded to five pages of 100 tags, 2 MiB per page, 10 seconds per
request and 20 seconds overall. Redirects and response pagination URLs are not
followed. An incomplete scan, rate limit, timeout, invalid response or lack of
stable tags is an unavailable check, never an assertion that Reef is current.
After a failed check, a previously found update remains visible with a warning;
its version is from an earlier successful check. Results are held in memory and
checked again after a restart.

GitHub currently mirrors source tags, without Release objects. The link opens
the changelog at the discovered tag. A tag can appear before the release pipeline
finishes building its container images. Check your deployment pipeline before
upgrading. This feature never pulls an image, updates Reef or updates an agent.
It requires only an upgrade of central Reef; agents need no change.
