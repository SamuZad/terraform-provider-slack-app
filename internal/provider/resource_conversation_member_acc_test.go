package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccConversationMemberResource(t *testing.T) {
	f := newFakeSlack(t, false)
	config := providerConfig(f) + `
resource "slack-app_conversation" "test" {
  name       = "terraform-members-test"
  is_private = true
}

resource "slack-app_conversation_member" "test" {
  channel_id = slack-app_conversation.test.id
  user_id    = "U12345678"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("slack-app_conversation_member.test", "id", regexp.MustCompile(`^C\d+/U12345678$`)),
					resource.TestCheckResourceAttr("slack-app_conversation_member.test", "channel_id", "C0000001"),
					resource.TestCheckResourceAttr("slack-app_conversation_member.test", "user_id", "U12345678"),
				),
			},
			{
				ResourceName:      "slack-app_conversation_member.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
