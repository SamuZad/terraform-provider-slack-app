package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
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
			// they cannot be verified against the imported state, and the
			// imported manifest is Slack's enriched form (server-side
			// defaults added) rather than the authored one.
			{
				ResourceName:      "slack-app_manifest.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"client_id", "client_secret", "verification_token",
					"signing_secret", "oauth_authorize_url", "export_credentials",
					"manifest",
				},
			},
			// The authored config stays diff-free against the server-enriched
			// manifest (defaults normalization).
			{
				Config:   providerConfig(f) + manifestConfig("two", []string{"chat:write", "channels:read"}),
				PlanOnly: true,
			},
		},
	})
}

// Removing an attribute from the authored manifest must explicitly reset it
// to its default in the update sent to Slack, not merely omit it.
func TestAccManifestResourceRemovedAttributeResetsToDefault(t *testing.T) {
	f := newFakeSlack(t, false)
	config := func(settings string) string {
		return providerConfig(f) + fmt.Sprintf(`
resource "slack-app_manifest" "test" {
  manifest = jsonencode({
    display_information = { name = "mcp" }
    features            = { bot_user = { display_name = "mcp" } }
    oauth_config        = { scopes = { bot = ["chat:write"] } }
    settings            = %s
  })
}
`, settings)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(`{ is_mcp_enabled = true }`),
			},
			// Dropping the key plans an update that explicitly sends the
			// default value.
			{
				Config: config(`{}`),
				Check: func(*terraform.State) error {
					if !strings.Contains(f.lastUpdate(), `"is_mcp_enabled":false`) {
						return fmt.Errorf("expected update to explicitly reset is_mcp_enabled to false, got: %s", f.lastUpdate())
					}
					return nil
				},
			},
			// And the result is stable.
			{
				Config:   config(`{}`),
				PlanOnly: true,
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
