package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const installConfig = `
resource "slack-app_install" "test" {
  app_id = slack-app_manifest.test.id
}
`

const installWithApprovalConfig = `
resource "slack-app_install" "test" {
  app_id          = slack-app_manifest.test.id
  approval_reason = "acceptance test"
}
`

func TestAccInstallResource(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			// Create: scopes come from the app manifest, not from config.
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write", "channels:read"}) + installConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack-app_install.test", "bot_token", "xoxb-fake-A0000001"),
					resource.TestCheckNoResourceAttr("slack-app_install.test", "user_token"),
					resource.TestCheckResourceAttr("slack-app_install.test", "scopes.bot.#", "2"),
					resource.TestCheckResourceAttr("slack-app_install.test", "scopes.user.#", "0"),
				),
			},
			// Import by app ID recovers the same tokens via the idempotent
			// developerInstall call.
			{
				ResourceName:      "slack-app_install.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Unchanged app: empty plan.
			{
				Config:   providerConfig(f) + manifestConfig("app", []string{"chat:write", "channels:read"}) + installConfig,
				PlanOnly: true,
			},
			// Scope reordering in the manifest: still an empty plan.
			{
				Config:   providerConfig(f) + manifestConfig("app", []string{"channels:read", "chat:write"}) + installConfig,
				PlanOnly: true,
			},
			// A scope change in the same apply updates the manifest first; the
			// install picks the change up on the next plan (the post-apply
			// plan is expectedly non-empty).
			{
				Config:             providerConfig(f) + manifestConfig("app", []string{"chat:write", "channels:read", "users:read"}) + installConfig,
				ExpectNonEmptyPlan: true,
			},
			// That next plan re-installs in place; Slack re-issues the same
			// token, now carrying the new scope grants.
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write", "channels:read", "users:read"}) + installConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack-app_install.test", "scopes.bot.#", "3"),
					resource.TestCheckResourceAttr("slack-app_install.test", "bot_token", "xoxb-fake-A0000001"),
				),
			},
		},
	})
}

const wiredInstallConfig = `
resource "slack-app_install" "test" {
  app_id     = slack-app_manifest.test.id
  scopes     = slack-app_manifest.test.scopes
}
`

// Wiring bot_scopes to the manifest resource makes a scope change re-install the
// install in the SAME run as the manifest update.
func TestAccInstallResourceWiredScopes(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write", "channels:read"}) + wiredInstallConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("slack-app_install.test", "bot_token"),
					resource.TestCheckResourceAttr("slack-app_install.test", "scopes.bot.#", "2"),
				),
			},
			// Scope change: manifest update and in-place re-install in one run.
			// The post-apply plan must be empty (no second apply needed).
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write", "channels:read", "channels:history"}) + wiredInstallConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("slack-app_install.test", "scopes.bot.#", "3"),
			},
			// Adding a USER scope re-installs the same way and issues a user
			// token.
			{
				Config: providerConfig(f) + manifestConfigWithUserScopes("app", []string{"chat:write", "channels:read", "channels:history"}, []string{"search:read"}) + wiredInstallConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack-app_install.test", "scopes.user.#", "1"),
					resource.TestCheckResourceAttr("slack-app_install.test", "user_token", "xoxp-fake-A0000001"),
				),
			},
			// Reordering scopes stays a no-op end to end.
			{
				Config:   providerConfig(f) + manifestConfigWithUserScopes("app", []string{"channels:history", "chat:write", "channels:read"}, []string{"search:read"}) + wiredInstallConfig,
				PlanOnly: true,
			},
		},
	})
}

// A nonexistent or inaccessible app produces a clear error, not a raw
// endpoint/error-code chain.
func TestAccInstallResourceAppNotFound(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(f) + `
resource "slack-app_install" "test" {
  app_id = "A0000000404"
}
`,
				ExpectError: regexp.MustCompile(`(?s)App Not Found or Not Accessible.*does not recognize app "A0000000404"`),
			},
		},
	})
}

func TestAccInstallResourceApprovalTimeout(t *testing.T) {
	f := newFakeSlack(t, true)
	f.stayPending = true
	config := fmt.Sprintf(`
provider "slack-app" {
  token                    = "xoxp-test-token"
  base_url                 = %q
  approval_timeout_seconds = 0
}
`, f.url()) + manifestConfig("app", []string{"chat:write"}) + installWithApprovalConfig
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)not approved within.*timeout`),
			},
		},
	})
}

func TestAccInstallResourceWithApproval(t *testing.T) {
	f := newFakeSlack(t, true)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			// The direct install fails, an approval request is submitted and
			// granted, and the install then succeeds.
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write"}) + installWithApprovalConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("slack-app_install.test", "bot_token"),
					func(*terraform.State) error {
						if got := f.approvalCount(); got != 1 {
							return fmt.Errorf("expected exactly 1 approval request, got %d", got)
						}
						return nil
					},
				),
			},
			// Apply the manifest scope change; the reinstall lands next plan.
			{
				Config:             providerConfig(f) + manifestConfig("app", []string{"chat:write", "users:read"}) + installWithApprovalConfig,
				ExpectNonEmptyPlan: true,
			},
			// A re-install of an already-approved app does not re-submit for
			// approval: the direct install succeeds first.
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write", "users:read"}) + installWithApprovalConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(*terraform.State) error {
					if got := f.approvalCount(); got != 1 {
						return fmt.Errorf("expected no new approval request, still got %d total", got)
					}
					return nil
				},
			},
		},
	})
}
