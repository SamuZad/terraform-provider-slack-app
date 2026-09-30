package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccConversationResource(t *testing.T) {
	f := newFakeSlack(t, false)
	config := providerConfig(f) + `
resource "slack-app_conversation" "test" {
	name = "terraform-test"
}
`
	privateConfig := providerConfig(f) + `
resource "slack-app_conversation" "test" {
	name       = "terraform-test"
	is_private = true
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("slack-app_conversation.test", "id", regexp.MustCompile(`^C\d+$`)),
					resource.TestCheckResourceAttr("slack-app_conversation.test", "name", "terraform-test"),
					resource.TestCheckResourceAttr("slack-app_conversation.test", "is_private", "false"),
				),
			},
			{
				Config:      privateConfig,
				ExpectError: regexp.MustCompile("Cannot Change Conversation Privacy"),
			},
			{
				ResourceName:      "slack-app_conversation.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
