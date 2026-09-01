package provider

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"slack-app": providerserver.NewProtocol6WithError(New("test")()),
}

func providerConfig(f *fakeSlack) string {
	return fmt.Sprintf(`
provider "slack-app" {
  token    = "xoxp-test-token"
  base_url = %q
}
`, f.url())
}

func quoteScopes(scopes []string) string {
	quoted := make([]string, len(scopes))
	for i, scope := range scopes {
		quoted[i] = strconv.Quote(scope)
	}
	return strings.Join(quoted, ", ")
}

func manifestConfig(name string, scopes []string) string {
	return fmt.Sprintf(`
resource "slack-app_manifest" "test" {
  manifest = {
    display_information = { name = %q }
    features            = { bot_user = { display_name = %q } }
    oauth_config        = { scopes = { bot = [%s] } }
  }
}
`, name, name, quoteScopes(scopes))
}

func manifestConfigWithUserScopes(name string, botScopes, userScopes []string) string {
	return fmt.Sprintf(`
resource "slack-app_manifest" "test" {
  manifest = {
    display_information = { name = %q }
    features            = { bot_user = { display_name = %q } }
    oauth_config        = { scopes = { bot = [%s], user = [%s] } }
  }
}
`, name, name, quoteScopes(botScopes), quoteScopes(userScopes))
}
