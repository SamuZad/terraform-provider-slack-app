resource "slack-app_install" "example" {
  app_id = slack-app_manifest.example.id

  # Wire the scopes from the manifest so scope changes re-install the app
  # in the same run. Omit to have them read from the app at install time.
  scopes = slack-app_manifest.example.scopes

  # Optional: setting a reason enables the admin-approval fallback when the
  # direct install fails (workspaces with app approval enabled). Approval is
  # requested for the provider's team_id (default: the token's workspace).
  approval_reason = "CI-managed app"
}

output "bot_token" {
  value     = slack-app_install.example.bot_token
  sensitive = true
}
