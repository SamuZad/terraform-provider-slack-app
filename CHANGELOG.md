# Changelog

## [Unreleased]

### Added

- `slack-app_manifest` resource: manage a Slack app via its JSON manifest,
  with import support, credential backfill for imported apps,
  `export_credentials` to keep secrets out of state, and semantic JSON
  comparison (key order, whitespace, and array order changes are not diffs).
- `slack-app_collaborator` resource: manage app collaborators, with import
  support and drift detection. Refuses to manage the provider token's own
  user to prevent orphaning the app.
- `slack-app_install` resource: install an app and export its bot/user
  tokens. Bot scopes are read from the app manifest; scope changes plan a
  re-install. Falls back to submitting an admin approval request (polling
  every 10s until the provider's `approval_timeout_seconds`, default 1 hour)
  only when the direct install fails. Supports
  import by app ID, recovering tokens via the idempotent install endpoint.
- Client-side retries: rate limits (respecting `Retry-After`) and 5xx
  responses with exponential backoff, 404s exactly once.
- `base_url` provider setting for GovSlack or testing.
