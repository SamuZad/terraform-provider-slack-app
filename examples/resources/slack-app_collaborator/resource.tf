resource "slack-app_collaborator" "example" {
  app_id          = slack-app_manifest.example.id
  user_email      = "user@example.com"
  permission_type = "owner" # or "reader"
}
