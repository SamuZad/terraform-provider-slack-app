package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccManifestResource(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			// Create.
			{
				Config: providerConfig(f) + manifestConfig("one", []string{"chat:write", "channels:read"}),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("slack-app_manifest.test", "id", regexp.MustCompile(`^A\d+$`)),
					resource.TestCheckResourceAttrSet("slack-app_manifest.test", "client_id"),
					resource.TestCheckResourceAttrSet("slack-app_manifest.test", "client_secret"),
					resource.TestCheckResourceAttrSet("slack-app_manifest.test", "signing_secret"),
					resource.TestMatchResourceAttr("slack-app_manifest.test", "oauth_authorize_url", regexp.MustCompile(`^https://`)),
					resource.TestCheckResourceAttr("slack-app_manifest.test", "export_credentials", "true"),
				),
			},
			// Reordering array elements must be a no-op plan (no permadiff).
			{
				Config:   providerConfig(f) + manifestConfig("one", []string{"channels:read", "chat:write"}),
				PlanOnly: true,
			},
			// A real change updates in place.
			{
				Config: providerConfig(f) + manifestConfig("two", []string{"chat:write", "channels:read"}),
				Check:  resource.TestMatchResourceAttr("slack-app_manifest.test", "manifest", regexp.MustCompile(`two`)),
			},
			// Import by app ID. Credentials are only issued at creation, so
			// they cannot be verified against the imported state.
			{
				ResourceName:      "slack-app_manifest.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"client_id", "client_secret", "verification_token",
					"signing_secret", "oauth_authorize_url", "export_credentials",
				},
			},
		},
	})
}

func TestAccManifestResourceExportCredentialsDisabled(t *testing.T) {
	f := newFakeSlack(t, false)
	config := providerConfig(f) + `
resource "slack-app_manifest" "test" {
  manifest = jsonencode({
    display_information = { name = "secretless" }
    features            = { bot_user = { display_name = "secretless" } }
    oauth_config        = { scopes = { bot = ["chat:write"] } }
  })
  export_credentials = false
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("slack-app_manifest.test", "id", regexp.MustCompile(`^A\d+$`)),
					resource.TestCheckNoResourceAttr("slack-app_manifest.test", "client_id"),
					resource.TestCheckNoResourceAttr("slack-app_manifest.test", "client_secret"),
					resource.TestCheckNoResourceAttr("slack-app_manifest.test", "signing_secret"),
					resource.TestCheckNoResourceAttr("slack-app_manifest.test", "oauth_authorize_url"),
				),
			},
			// Still a stable no-op afterwards.
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
}
