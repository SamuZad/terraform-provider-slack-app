# The manifest is an HCL object (no jsonencode). Computed values inside it
# (like a description referencing another resource) stay localized, so the
# scopes remain known at plan time and dependent installs are not disturbed.
resource "slack-app_manifest" "example" {
  manifest = {
    display_information = { name = "ci-bot" }
    features            = { bot_user = { display_name = "ci-bot" } }
    oauth_config        = { scopes = { bot = ["chat:write"] } }
  }
}
