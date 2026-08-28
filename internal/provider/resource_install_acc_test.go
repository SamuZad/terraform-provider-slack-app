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
					resource.TestCheckResourceAttr("slack-app_install.test", "user_token", "xoxp-fake-A0000001"),
					resource.TestCheckResourceAttr("slack-app_install.test", "bot_scopes.#", "2"),
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
			// That next plan replaces the install; Slack re-issues the same
			// token, now carrying the new scope grants.
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write", "channels:read", "users:read"}) + installConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack-app_install.test", "bot_scopes.#", "3"),
					resource.TestCheckResourceAttr("slack-app_install.test", "bot_token", "xoxb-fake-A0000001"),
				),
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
			// A reinstall of an already-approved app does not re-submit for
			// approval: the direct install succeeds first.
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write", "users:read"}) + installWithApprovalConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack-app_install.test", plancheck.ResourceActionReplace),
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
