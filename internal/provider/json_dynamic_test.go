package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func scopesTestObject(bot []attr.Value, extra map[string]attr.Value) types.Dynamic {
	botTypes := make([]attr.Type, len(bot))
	for i, v := range bot {
		botTypes[i] = v.Type(context.Background())
	}
	scopesObj := types.ObjectValueMust(
		map[string]attr.Type{"bot": types.TupleType{ElemTypes: botTypes}},
		map[string]attr.Value{"bot": types.TupleValueMust(botTypes, bot)},
	)
	oauthObj := types.ObjectValueMust(
		map[string]attr.Type{"scopes": scopesObj.Type(context.Background())},
		map[string]attr.Value{"scopes": scopesObj},
	)
	attrs := map[string]attr.Value{"oauth_config": oauthObj}
	for k, v := range extra {
		attrs[k] = v
	}
	attrTypes := map[string]attr.Type{}
	for k, v := range attrs {
		attrTypes[k] = v.Type(context.Background())
	}
	return types.DynamicValue(types.ObjectValueMust(attrTypes, attrs))
}

// The headline property of the object-form manifest: an unknown leaf outside
// the scopes subtree (e.g. a computed description) leaves the scopes known.
func TestDynamicScopesPartialUnknown(t *testing.T) {
	manifest := scopesTestObject(
		[]attr.Value{types.StringValue("chat:write"), types.StringValue("channels:read")},
		map[string]attr.Value{
			"display_information": types.ObjectValueMust(
				map[string]attr.Type{"description": types.StringType},
				map[string]attr.Value{"description": types.StringUnknown()},
			),
		},
	)

	scopes, known := dynamicScopes(manifest)
	if !known {
		t.Fatal("expected scopes to be known despite unknown description")
	}
	if !sameScopes(scopes.Bot, []string{"chat:write", "channels:read"}) {
		t.Fatalf("unexpected bot scopes: %v", scopes.Bot)
	}
}

func TestDynamicScopesUnknownScopeElement(t *testing.T) {
	manifest := scopesTestObject(
		[]attr.Value{types.StringValue("chat:write"), types.StringUnknown()},
		nil,
	)
	if _, known := dynamicScopes(manifest); known {
		t.Fatal("expected scopes to be unknown when a scope element is unknown")
	}
}

func TestDynamicManifestJSONRoundTrip(t *testing.T) {
	original := `{"display_information":{"name":"x"},"oauth_config":{"scopes":{"bot":["a","b"]}},"settings":{"socket_mode_enabled":true,"n":3}}`

	// object form round-trips semantically: JSON -> attr.Value -> JSON
	var decoded interface{}
	if err := json.Unmarshal([]byte(original), &decoded); err != nil {
		t.Fatal(err)
	}
	objectForm := types.DynamicValue(goToAttr(context.Background(), decoded))
	rendered, err := dynamicManifestJSON(objectForm)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonSemanticallyEqual(rendered, original) {
		t.Fatalf("round trip not semantically equal:\n%s\n%s", rendered, original)
	}
}

func TestDynamicManifestJSONUnknown(t *testing.T) {
	manifest := scopesTestObject(
		[]attr.Value{types.StringValue("chat:write")},
		map[string]attr.Value{"description": types.StringUnknown()},
	)
	if _, err := dynamicManifestJSON(manifest); err == nil {
		t.Fatal("expected error for partially unknown manifest")
	}
}
