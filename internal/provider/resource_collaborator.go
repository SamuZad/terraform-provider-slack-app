package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &collaboratorResource{}
	_ resource.ResourceWithConfigure   = &collaboratorResource{}
	_ resource.ResourceWithImportState = &collaboratorResource{}
)

func NewCollaboratorResource() resource.Resource {
	return &collaboratorResource{}
}

type collaboratorResource struct {
	client *SlackClient
}

type collaboratorResourceModel struct {
	ID             types.String `tfsdk:"id"`
	AppID          types.String `tfsdk:"app_id"`
	UserEmail      types.String `tfsdk:"user_email"`
	PermissionType types.String `tfsdk:"permission_type"`
}

func (r *collaboratorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_collaborator"
}

func (r *collaboratorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adds a collaborator to a Slack app, using the same undocumented " +
			"`developer.apps.owners.add` endpoint as the Slack CLI. Requires a Slack CLI service token.\n\n" +
			"The user the provider token belongs to cannot be managed by this resource: removing them as a " +
			"collaborator would revoke the provider's own access to the app and orphan its resources, so " +
			"create, delete, and import all refuse to touch the token user.\n\n" +
			"Existing collaborators can be imported by app ID and email " +
			"(`terraform import slack-app_collaborator.example A0123456789/user@example.com`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"app_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The app ID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_email": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Email address of the workspace user to add as a collaborator.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"permission_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("owner"),
				MarkdownDescription: "Collaborator permission: `owner` or `reader`.",
				Validators: []validator.String{
					stringvalidator.OneOf("owner", "reader"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *collaboratorResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*SlackClient)
}

// isTokenUser reports whether email belongs to the user the provider token is
// issued for. The token user must never be managed by this resource: removing
// them as a collaborator (which Terraform would do on destroy) would strip the
// provider of access to the app, orphaning every resource that depends on it.
func (r *collaboratorResource) isTokenUser(ctx context.Context, appID, email, action string, diags *diag.Diagnostics) bool {
	tokenEmail, err := r.client.TokenUserEmail(ctx, appID)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "app_not_found" {
			diags.AddError(
				"App Not Found or Not Accessible",
				fmt.Sprintf("Slack returned `app_not_found` for app %q while trying to %s collaborator %q. "+
					"Slack does not distinguish between an app that does not exist and one the token's user has "+
					"no access to, so either the app ID is wrong (or the app was deleted outside Terraform), or "+
					"the provider token's user is not a collaborator on this app. Verify the app ID and that the "+
					"token user is a collaborator on the app.", appID, action, email),
			)
			return true
		}
		diags.AddError(
			"Unable to Verify Token User",
			fmt.Sprintf("Refusing to %s collaborator %q: the identity of the provider token could not be "+
				"resolved, so the provider cannot rule out that this is the token's own user (removing them "+
				"would orphan the app). Error: %s", action, email, err),
		)
		return true
	}
	if strings.EqualFold(tokenEmail, email) {
		diags.AddError(
			"Cannot Manage Token User as Collaborator",
			fmt.Sprintf("%q is the user the provider token belongs to. Removing them as a collaborator would "+
				"revoke the provider's own access to the app and orphan its resources, so this resource refuses "+
				"to manage them. The token user is already a collaborator on every app it creates.", email),
		)
		return true
	}
	return false
}

func (r *collaboratorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan collaboratorResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.isTokenUser(ctx, plan.AppID.ValueString(), plan.UserEmail.ValueString(), "add", &resp.Diagnostics) {
		return
	}

	err := r.client.FormRequest(ctx, "developer.apps.owners.add", url.Values{
		"app_id":          {plan.AppID.ValueString()},
		"permission_type": {plan.PermissionType.ValueString()},
		"user_email":      {plan.UserEmail.ValueString()},
	}, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add collaborator: %s", err))
		return
	}

	plan.ID = types.StringValue(plan.AppID.ValueString() + "/" + plan.UserEmail.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *collaboratorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state collaboratorResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	owners, err := r.client.ListOwners(ctx, state.AppID.ValueString())
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "app_not_found" {
			// The app is gone (or no longer accessible), so the collaborator is too.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list collaborators: %s", err))
		return
	}

	for _, owner := range owners.Owners {
		if strings.EqualFold(owner.UserEmail, state.UserEmail.ValueString()) {
			state.PermissionType = types.StringValue(owner.PermissionType)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *collaboratorResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

func (r *collaboratorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state collaboratorResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.isTokenUser(ctx, state.AppID.ValueString(), state.UserEmail.ValueString(), "remove", &resp.Diagnostics) {
		return
	}

	err := r.client.FormRequest(ctx, "developer.apps.owners.remove", url.Values{
		"app_id":     {state.AppID.ValueString()},
		"user_email": {state.UserEmail.ValueString()},
	}, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove collaborator: %s", err))
		return
	}

	resp.State.RemoveResource(ctx)
}

func (r *collaboratorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	appID, email, ok := strings.Cut(req.ID, "/")
	if !ok || appID == "" || email == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected an import ID in the form \"app_id/user_email\" "+
				"(e.g. \"A0123456789/user@example.com\"), got %q.", req.ID),
		)
		return
	}

	if r.isTokenUser(ctx, appID, email, "import", &resp.Diagnostics) {
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_id"), appID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_email"), email)...)
}
