package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// description is a raw HCL expression so tests can pass references.
func manifestObjectConfig(name, description string, botScopes []string) string {
	return fmt.Sprintf(`
resource "slack-app_manifest" "test" {
  manifest = {
    display_information = { name = %q, description = %s }
    features            = { bot_user = { display_name = %q } }
    oauth_config        = { scopes = { bot = [%s] } }
  }
}
`, name, description, name, quoteScopes(botScopes))
}

func TestAccManifestResourceObjectForm(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			// Create with an HCL object manifest.
			{
				Config: providerConfig(f) + manifestObjectConfig("obj", `"a description"`, []string{"chat:write", "channels:read"}) + wiredInstallConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack-app_manifest.test", "scopes.bot.#", "2"),
					resource.TestCheckResourceAttrSet("slack-app_install.test", "bot_token"),
				),
			},
			// Reordering scopes in the object is a no-op plan.
			{
				Config:   providerConfig(f) + manifestObjectConfig("obj", `"a description"`, []string{"channels:read", "chat:write"}) + wiredInstallConfig,
				PlanOnly: true,
			},
			// A description-only change updates the manifest and leaves the
			// install untouched.
			{
				Config: providerConfig(f) + manifestObjectConfig("obj", `"another description"`, []string{"chat:write", "channels:read"}) + wiredInstallConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_manifest.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionNoop),
					},
				},
			},
			// A scope change still re-installs in the same run.
			{
				Config: providerConfig(f) + manifestObjectConfig("obj", `"another description"`, []string{"chat:write", "channels:read", "users:read"}) + wiredInstallConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("slack-app_install.test", "scopes.bot.#", "3"),
			},
		},
	})
}

// The headline scenario: a manifest whose description references another
// resource's computed (unknown-at-plan) value. With the object form, the
// scopes stay known at plan time, so the wired install plans a no-op instead
// of cascading to unknown tokens.
func TestAccManifestResourceObjectFormUnknownDescription(t *testing.T) {
	f := newFakeSlack(t, false)
	config := func(depScopes []string) string {
		return providerConfig(f) + fmt.Sprintf(`
resource "slack-app_manifest" "dep" {
  manifest = {
    display_information = { name = "dep" }
    features            = { bot_user = { display_name = "dep" } }
    oauth_config        = { scopes = { bot = [%s] } }
  }
}

resource "slack-app_install" "dep" {
  app_id = slack-app_manifest.dep.id
  scopes = slack-app_manifest.dep.scopes
}

resource "slack-app_manifest" "test" {
  manifest = {
    display_information = { name = "main", description = slack-app_install.dep.bot_token }
    features            = { bot_user = { display_name = "main" } }
    oauth_config        = { scopes = { bot = ["chat:write"] } }
  }
}

resource "slack-app_install" "test" {
  app_id = slack-app_manifest.test.id
  scopes = slack-app_manifest.test.scopes
}
`, quoteScopes(depScopes))
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			// Even at create, with the description unknown (dep's token is
			// not issued yet), the main manifest's planned scopes are KNOWN.
			{
				Config: config([]string{"chat:write"}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue("slack-app_manifest.test", tfjsonpath.New("scopes"), knownvalue.NotNull()),
					},
				},
			},
			// Change dep's scopes: dep re-installs, its bot_token goes
			// unknown, so main's description is unknown at plan — but main's
			// scopes stay known and main's install plans a NO-OP. This is the
			// noise the object form eliminates.
			{
				Config: config([]string{"chat:write", "users:read"}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.dep", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("slack-app_manifest.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionNoop),
						plancheck.ExpectKnownValue("slack-app_manifest.test", tfjsonpath.New("scopes"), knownvalue.NotNull()),
					},
				},
			},
			// And everything settles.
			{
				Config:   config([]string{"chat:write", "users:read"}),
				PlanOnly: true,
			},
		},
	})
}

// jsonencode strings are rejected at plan time with a clear pointer to the fix.
func TestAccManifestResourceStringFormRejected(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(f) + `
resource "slack-app_manifest" "test" {
  manifest = jsonencode({
    display_information = { name = "legacy" }
  })
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)must be an HCL object.*Remove the jsonencode`),
			},
		},
	})
}
