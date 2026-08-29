package provider

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// oauthScopes holds the OAuth scopes declared in an app manifest. User scopes
// matter as much as bot scopes: both are granted by an install and both
// require admin re-approval when they grow.
type oauthScopes struct {
	Bot  []string
	User []string
}

type scopesModel struct {
	Bot  types.Set `tfsdk:"bot"`
	User types.Set `tfsdk:"user"`
}

func scopesAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"bot":  types.SetType{ElemType: types.StringType},
		"user": types.SetType{ElemType: types.StringType},
	}
}

// scopesSchemaAttributes returns the nested attribute schema for a scopes
// object. Pass optional=true where the object may be set from config (the
// install resource); false where it is purely computed (the manifest).
func scopesSchemaAttributes(optional bool) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"bot": schema.SetAttribute{
			Optional:            optional,
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Bot scopes (`oauth_config.scopes.bot`).",
		},
		"user": schema.SetAttribute{
			Optional:            optional,
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "User scopes (`oauth_config.scopes.user`).",
		},
	}
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// scopesObject builds a scopes object value from scope slices.
func scopesObject(ctx context.Context, scopes oauthScopes) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	bot, d := types.SetValueFrom(ctx, types.StringType, emptyIfNil(scopes.Bot))
	diags.Append(d...)
	user, d := types.SetValueFrom(ctx, types.StringType, emptyIfNil(scopes.User))
	diags.Append(d...)
	if diags.HasError() {
		return types.ObjectNull(scopesAttrTypes()), diags
	}
	object, d := types.ObjectValue(scopesAttrTypes(), map[string]attr.Value{"bot": bot, "user": user})
	diags.Append(d...)
	return object, diags
}

// scopesFromObject extracts the scope slices from a known scopes object.
func scopesFromObject(ctx context.Context, object types.Object) (oauthScopes, diag.Diagnostics) {
	var model scopesModel
	var scopes oauthScopes
	diags := object.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return scopes, diags
	}
	if !model.Bot.IsNull() && !model.Bot.IsUnknown() {
		diags.Append(model.Bot.ElementsAs(ctx, &scopes.Bot, false)...)
	}
	if !model.User.IsNull() && !model.User.IsUnknown() {
		diags.Append(model.User.ElementsAs(ctx, &scopes.User, false)...)
	}
	return scopes, diags
}

func sameScopes(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func sameOAuthScopes(a, b oauthScopes) bool {
	return sameScopes(a.Bot, b.Bot) && sameScopes(a.User, b.User)
}
