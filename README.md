# Terraform Provider: slack-app

Manages the full developer lifecycle of Slack apps: the app manifest, app
collaborators, and developer installs (which return bot and user tokens, and
can wait for admin approval first).

```hcl
provider "slack-app" {
  configuration_token = var.configuration_token # or SLACK_CONFIGURATION_TOKEN
  bot_token           = var.bot_token           # or SLACK_BOT_TOKEN
}

resource "slack-app_manifest" "bot" {
  manifest = {
    display_information = { name = "ci-bot" }
    features            = { bot_user = { display_name = "ci-bot" } }
    oauth_config        = { scopes = { bot = ["chat:write"] } }
  }
}

resource "slack-app_collaborator" "dev" {
  app_id     = slack-app_manifest.bot.id
  user_email = "dev@example.com"
}

resource "slack-app_install" "bot" {
  app_id          = slack-app_manifest.bot.id
  scopes          = slack-app_manifest.bot.scopes # re-install on scope changes
  approval_reason = "CI-managed app"
}
```

## Credentials

Three credential roles are supported:

- `configuration_token` accepts a **Slack CLI service token** (`slack auth token`)
  for collaborator and installation resources, or an **app configuration token**
  ([config tokens](https://api.slack.com/authentication/config-tokens)) for the
  manifest resource.
- `bot_token` is the installed app's bot token and is used by the conversation
  and user data source resources. Set the `SLACK_BOT_TOKEN` environment variable
  or pass it directly. The app must request these bot scopes:
  `channels:manage`, `channels:read`, `groups:write`, `groups:read`, and
  `users:read.email`.

The token's user is a collaborator on every app it creates; the
`collaborator` resource refuses to remove that user, since doing so would
revoke the provider's own access and orphan its resources.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.5
- [Go](https://go.dev/doc/install) >= 1.25 (to build from source)

## Development

```sh
make build     # compile
make test      # unit + acceptance tests (offline, against a fake Slack API)
make generate  # regenerate docs/ from schema + examples/
```

To run a local build against real configuration, point Terraform at your
binary with a [`dev_overrides`](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers)
block in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "samuzad/slack-app" = "/path/to/your/go/bin"
  }
  direct {}
}
```

## Releasing

Push a `v*` tag. The release workflow builds and signs artifacts with
GoReleaser; the GitHub repository needs `GPG_PRIVATE_KEY` and `PASSPHRASE`
secrets configured, and the Terraform Registry needs the matching GPG public
key on the publishing namespace.
