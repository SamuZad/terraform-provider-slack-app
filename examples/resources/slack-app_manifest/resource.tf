resource "slack-app_manifest" "example" {
  manifest = jsonencode({
    display_information = { name = "ci-bot" }
    features            = { bot_user = { display_name = "ci-bot" } }
    oauth_config        = { scopes = { bot = ["chat:write"] } }
  })
}
