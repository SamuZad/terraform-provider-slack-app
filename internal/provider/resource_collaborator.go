package provider

import (
	"context"
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
	_ resource.ResourceWithModifyPlan  = &collaboratorResource{}
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
				MarkdownDescription: "Collaborator permission: `owner` or `reader`. Defaults to `owner`.",
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
		if isAppAccessError(err) {
			diags.AddError("App Not Found or Not Accessible",
				fmt.Sprintf("Cannot %s collaborator %q: %s", action, email, appAccessErrorDetail(appID)))
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

// ModifyPlan validates a planned create against the live app at plan time,
// so an invalid app, the token user, or an already-existing collaborator
// fails the plan instead of the apply. Skipped when the app ID is not yet
// known (the app is created in the same run); the apply-time guards remain
// as the backstop for that case.
func (r *collaboratorResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Only creates: destroys are guarded in Delete, and existing resources
	// are validated by Read during refresh.
	if req.Plan.Raw.IsNull() || !req.State.Raw.IsNull() {
		return
	}

	var plan collaboratorResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.AppID.IsUnknown() || plan.UserEmail.IsUnknown() {
		return
	}
	appID, email := plan.AppID.ValueString(), plan.UserEmail.ValueString()

	owners, err := r.client.ListOwners(ctx, appID)
	if err != nil {
		if isAppAccessError(err) {
			resp.Diagnostics.AddError("App Not Found or Not Accessible", appAccessErrorDetail(appID))
		}
		// Transient errors are left for the apply to surface.
		return
	}

	if tokenEmail, err := r.client.TokenUserEmail(ctx, appID); err == nil && strings.EqualFold(tokenEmail, email) {
		resp.Diagnostics.AddError(
			"Cannot Manage Token User as Collaborator",
			fmt.Sprintf("%q is the user the provider token belongs to. Removing them as a collaborator would "+
				"revoke the provider's own access to the app and orphan its resources, so this resource refuses "+
				"to manage them. The token user is already a collaborator on every app it creates.", email),
		)
		return
	}

	for _, owner := range owners.Owners {
		if strings.EqualFold(owner.UserEmail, email) {
			resp.Diagnostics.AddError(
				"Collaborator Already Exists",
				fmt.Sprintf("%q is already a collaborator on app %s. Adopt it instead of recreating it: "+
					"terraform import <resource address> %s/%s", email, appID, appID, email),
			)
			return
		}
	}
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
		if isAppAccessError(err) {
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
