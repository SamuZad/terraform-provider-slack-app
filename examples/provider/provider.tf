provider "slack-app" {
  # A Slack CLI service token (`slack auth token`), or an app configuration
  # token for manifest-only usage. Can also be set via SLACK_TOKEN.
  token = var.slack_token
}
