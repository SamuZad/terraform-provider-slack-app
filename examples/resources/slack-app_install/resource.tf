resource "slack-app_install" "example" {
  app_id = slack-app_manifest.example.id

  # Optional: setting a reason enables the admin-approval fallback when the
  # direct install fails (workspaces with app approval enabled). Approval is
  # requested for the provider's team_id (default: the token's workspace).
  approval_reason = "CI-managed app"
}

output "bot_token" {
  value     = slack-app_install.example.bot_token
  sensitive = true
}
