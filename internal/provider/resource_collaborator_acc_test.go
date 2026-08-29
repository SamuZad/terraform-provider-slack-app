package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func collaboratorConfig(email string) string {
	return fmt.Sprintf(`
resource "slack-app_collaborator" "test" {
  app_id     = slack-app_manifest.test.id
  user_email = %q
}
`, email)
}

func TestAccCollaboratorResource(t *testing.T) {
	f := newFakeSlack(t, false)
	config := providerConfig(f) + manifestConfig("app", []string{"chat:write"}) + collaboratorConfig("dev@example.com")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			// Create.
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("slack-app_collaborator.test", "id", regexp.MustCompile(`^A\d+/dev@example\.com$`)),
					resource.TestCheckResourceAttr("slack-app_collaborator.test", "permission_type", "owner"),
				),
			},
			// Out-of-band removal is detected on refresh and repaired on apply.
			{
				PreConfig: func() { f.dropOwner("dev@example.com") },
				Config:    config,
				Check: func(*terraform.State) error {
					if !f.hasOwner("dev@example.com") {
						return fmt.Errorf("expected collaborator to be re-added after drift")
					}
					return nil
				},
			},
			// Import by "app_id/user_email"; permission_type is backfilled
			// from the live collaborator list.
			{
				ResourceName:      "slack-app_collaborator.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// A collaborator on a nonexistent app fails at plan time with a clear error.
func TestAccCollaboratorResourceAppNotFound(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(f) + `
resource "slack-app_collaborator" "test" {
  app_id     = "A0000000404"
  user_email = "dev@example.com"
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)App Not Found or Not Accessible.*does not recognize app "A0000000404"`),
			},
		},
	})
}

func TestAccCollaboratorResourceRefusesTokenUser(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      providerConfig(f) + manifestConfig("app", []string{"chat:write"}) + collaboratorConfig(fakeTokenUserEmail),
				ExpectError: regexp.MustCompile("Cannot Manage Token User"),
			},
		},
	})
}

func TestAccCollaboratorResourceAlreadyOwner(t *testing.T) {
	f := newFakeSlack(t, false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(f) + manifestConfig("app", []string{"chat:write"}),
			},
			// Someone added out-of-band: the PLAN already errors and points at
			// import (the app exists in state, so its ID is known at plan time).
			{
				PreConfig:   func() { f.addOwner("existing@example.com") },
				Config:      providerConfig(f) + manifestConfig("app", []string{"chat:write"}) + collaboratorConfig("existing@example.com"),
				ExpectError: regexp.MustCompile(`(?s)already a collaborator.*import`),
			},
		},
	})
}
