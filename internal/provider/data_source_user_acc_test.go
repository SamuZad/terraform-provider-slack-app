package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccUserDataSource(t *testing.T) {
	f := newFakeSlack(t, false)
	config := providerConfig(f) + `
data "slack-app_user" "member" {
  email = "member@example.com"
}

resource "slack-app_conversation" "test" {
  name       = "terraform-user-data-source-test"
  is_private = true
}

resource "slack-app_conversation_member" "test" {
  channel_id = slack-app_conversation.test.id
  user_id    = data.slack-app_user.member.user_id
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack-app_conversation_member.test", "user_id", "U12345678"),
				),
			},
		},
	})
}
