# Terraform Provider: slack-app

Manages the full developer lifecycle of Slack apps: the app manifest, app
collaborators, and developer installs (which return bot and user tokens, and
can wait for admin approval first).

```hcl
provider "slack-app" {
  token = var.slack_token # or SLACK_CLI_TOKEN / SLACK_TOKEN
}

resource "slack-app_manifest" "bot" {
  manifest = jsonencode({
    display_information = { name = "ci-bot" }
    features            = { bot_user = { display_name = "ci-bot" } }
    oauth_config        = { scopes = { bot = ["chat:write"] } }
  })
}

resource "slack-app_collaborator" "dev" {
  app_id     = slack-app_manifest.bot.id
  user_email = "dev@example.com"
}

resource "slack-app_install" "bot" {
  app_id          = slack-app_manifest.bot.id
  approval_reason = "CI-managed app"
}
```

## Credentials

Two token types work, both issued to a specific Slack user:

- A **Slack CLI service token** (`slack auth token`) works with every resource.
- An **app configuration token**
  ([config tokens](https://api.slack.com/authentication/config-tokens)) works
  with the `manifest` resource only.

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
