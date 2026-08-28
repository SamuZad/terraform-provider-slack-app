package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const approvalPollInterval = 10 * time.Second

var (
	_ resource.Resource                = &installResource{}
	_ resource.ResourceWithConfigure   = &installResource{}
	_ resource.ResourceWithModifyPlan  = &installResource{}
	_ resource.ResourceWithImportState = &installResource{}
)

func NewInstallResource() resource.Resource {
	return &installResource{}
}

type installResource struct {
	client *SlackClient
}

type installResourceModel struct {
	ID             types.String `tfsdk:"id"`
	AppID          types.String `tfsdk:"app_id"`
	ApprovalReason types.String `tfsdk:"approval_reason"`
	BotScopes      types.List   `tfsdk:"bot_scopes"`
	BotToken       types.String `tfsdk:"bot_token"`
	UserToken      types.String `tfsdk:"user_token"`
}

type installRequest struct {
	AppID     string   `json:"app_id"`
	BotScopes []string `json:"bot_scopes"`
}

type installResponse struct {
	APIAccessTokens struct {
		Bot  string `json:"bot"`
		User string `json:"user"`
	} `json:"api_access_tokens"`
}

type manifestScopesResponse struct {
	Manifest struct {
		OAuthConfig struct {
			Scopes struct {
				Bot []string `json:"bot"`
			} `json:"scopes"`
		} `json:"oauth_config"`
	} `json:"manifest"`
}

type approvalCreateRequest struct {
	App       string `json:"app"`
	TeamID    string `json:"team_id"`
	BotScopes string `json:"bot_scopes"`
	Reason    string `json:"reason,omitempty"`
}

type approvalCreateResponse struct {
	RequestID string `json:"request_id"`
}

type approvalListRequest struct {
	AppID          string   `json:"app_id"`
	RequestedTeams []string `json:"requested_teams"`
}

type approvalListResponse struct {
	Requests []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"requests"`
}

func (r *installResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_install"
}

func (r *installResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Installs a Slack app into the workspace via `apps.developerInstall` " +
			"(the same endpoint used by `slack install`), exporting the resulting bot and user tokens. " +
			"The bot scopes are read from the app's manifest.\n\n" +
			"The install is attempted directly first; if the app is already approved (or the workspace does " +
			"not require admin approval), no approval request is submitted. Only when the direct install fails " +
			"and `approval_reason` is set does the resource submit an admin approval request and wait for " +
			"it — polling every 10 seconds until the provider's `approval_timeout_seconds` (default 1 hour) " +
			"elapses.\n\n" +
			"At plan time the resource compares the app's current bot scopes against the ones it was " +
			"installed with, and plans a replacement (re-install) when they differ; Slack re-issues " +
			"the same tokens with the new scope grants. " +
			"Manifest changes that do not touch the bot scopes do not trigger a re-install. A scope change " +
			"applied in the same run as the manifest update is picked up by the next plan; to force the " +
			"re-install into the same run, add " +
			"`lifecycle { replace_triggered_by = [slack-app_manifest.example.manifest] }`.\n\n" +
			"Destroying this resource only removes it from state; the app remains installed until the app " +
			"itself is deleted.\n\n" +
			"Existing installations can be imported by app ID " +
			"(`terraform import slack-app_install.example A0123456789`): `apps.developerInstall` is " +
			"idempotent, so the import re-runs it to recover the tokens Slack already issued.",
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
			"approval_reason": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Setting this enables the admin-approval fallback when the direct " +
					"install fails; the value is the reason shown to the approving admin. The install then " +
					"fails only if the approval request is denied, cancelled, or not approved within the " +
					"provider's `approval_timeout_seconds`. Approval is requested for the provider's " +
					"`team_id` (defaulting to the token's own workspace).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"bot_scopes": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The bot scopes the app was installed with, read from the app manifest.",
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
			},
			"bot_token": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The bot token (`xoxb-…`) issued by the installation.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_token": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The user token (`xoxp-…`) issued by the installation, if user scopes were requested.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *installResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*SlackClient)
}

func sameScopes(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// ModifyPlan re-reads the app's bot scopes from its manifest and plans a
// replacement (re-install) when they no longer match the scopes the app was
// installed with.
func (r *installResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Nothing to compare on create (no state) or destroy (no plan).
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var state, plan installResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A changed app_id already forces a replacement; the old app may be gone.
	if !plan.AppID.Equal(state.AppID) {
		return
	}

	current, err := r.appBotScopes(ctx, state.AppID.ValueString())
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "app_not_found" {
			// The app was deleted out-of-band; leave the plan alone and let the
			// manifest resource drive recreation.
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read bot scopes from the app manifest: %s", err))
		return
	}

	var installed []string
	resp.Diagnostics.Append(state.BotScopes.ElementsAs(ctx, &installed, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if sameScopes(installed, current) {
		return
	}

	currentList, diags := types.ListValueFrom(ctx, types.StringType, current)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("bot_scopes"), currentList)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("bot_token"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("user_token"), types.StringUnknown())...)
	resp.RequiresReplace = append(resp.RequiresReplace, path.Root("bot_scopes"))
}

// appBotScopes reads the app's current bot scopes from its manifest.
func (r *installResource) appBotScopes(ctx context.Context, appID string) ([]string, error) {
	var result manifestScopesResponse
	err := r.client.JSONRequest(ctx, "apps.manifest.export", manifestRequest{AppID: appID}, &result)
	if err != nil {
		return nil, fmt.Errorf("apps.manifest.export: %w", err)
	}
	return result.Manifest.OAuthConfig.Scopes.Bot, nil
}

func (r *installResource) install(ctx context.Context, appID string, scopes []string) (*installResponse, error) {
	var result installResponse
	err := r.client.JSONRequest(ctx, "apps.developerInstall", installRequest{
		AppID:     appID,
		BotScopes: scopes,
	}, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *installResource) lookupApprovalStatus(ctx context.Context, appID, teamID, requestID string) (string, error) {
	var result approvalListResponse
	err := r.client.JSONRequest(ctx, "apps.approvals.requests.list", approvalListRequest{
		AppID:          appID,
		RequestedTeams: []string{teamID},
	}, &result)
	if err != nil {
		return "", err
	}
	for _, request := range result.Requests {
		if request.ID == requestID {
			return request.Status, nil
		}
	}
	return "", nil
}

// waitForApproval submits an admin approval request for the app and blocks
// until it is approved. It returns an error if the request is denied,
// cancelled, or still pending when the timeout elapses.
func (r *installResource) waitForApproval(ctx context.Context, appID string, scopes []string, reason string) error {
	teamID, err := r.client.TeamID(ctx)
	if err != nil {
		return fmt.Errorf("unable to determine the workspace to request approval for: %w", err)
	}

	var created approvalCreateResponse
	err = r.client.JSONRequest(ctx, "apps.approvals.requests.create", approvalCreateRequest{
		App:       appID,
		TeamID:    teamID,
		BotScopes: strings.Join(scopes, ","),
		Reason:    reason,
	}, &created)
	if err != nil {
		return fmt.Errorf("unable to create approval request: %w", err)
	}

	deadline := time.Now().Add(r.client.ApprovalTimeout)
	for {
		status, err := r.lookupApprovalStatus(ctx, appID, teamID, created.RequestID)
		if err != nil {
			return fmt.Errorf("unable to check approval status: %w", err)
		}

		switch status {
		case "approved":
			return nil
		case "denied", "cancelled":
			return fmt.Errorf("approval request %s was %s", created.RequestID, status)
		case "pending", "":
		default:
			return fmt.Errorf("slack returned an unknown approval status: %s", status)
		}

		if time.Now().After(deadline) {
			r.cancelApproval(ctx, appID)
			return fmt.Errorf("the application was not approved within the timeout period (%s); "+
				"approval request %s has been cancelled", r.client.ApprovalTimeout, created.RequestID)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(approvalPollInterval):
		}
	}
}

// cancelApproval makes a best-effort attempt to withdraw a pending approval
// request so it does not linger after a failed install.
func (r *installResource) cancelApproval(ctx context.Context, appID string) {
	_ = r.client.JSONRequest(ctx, "apps.approvals.requests.cancel", map[string]string{
		"app_id": appID,
	}, nil)
}

func (r *installResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan installResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appID := plan.AppID.ValueString()

	scopes, err := r.appBotScopes(ctx, appID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read bot scopes from the app manifest: %s", err))
		return
	}

	// Try the direct install first: if the app is already approved (or the
	// workspace requires no approval), no approval request is needed.
	result, installErr := r.install(ctx, appID, scopes)
	if installErr != nil {
		if plan.ApprovalReason.IsNull() {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to install app: %s", installErr))
			return
		}

		if err := r.waitForApproval(ctx, appID, scopes, plan.ApprovalReason.ValueString()); err != nil {
			resp.Diagnostics.AddError("Approval Failed",
				fmt.Sprintf("Direct install failed (%s) and the approval fallback did not succeed: %s", installErr, err))
			return
		}

		result, err = r.install(ctx, appID, scopes)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to install app after approval: %s", err))
			return
		}
	}

	if result.APIAccessTokens.Bot == "" {
		resp.Diagnostics.AddError("Install Failed", "Installation succeeded but Slack returned no bot token.")
		return
	}

	scopesList, diags := types.ListValueFrom(ctx, types.StringType, scopes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = types.StringValue(appID)
	plan.BotScopes = scopesList
	plan.BotToken = types.StringValue(result.APIAccessTokens.Bot)
	if result.APIAccessTokens.User != "" {
		plan.UserToken = types.StringValue(result.APIAccessTokens.User)
	} else {
		plan.UserToken = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *installResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state installResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *installResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

func (r *installResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

// ImportState recovers an existing installation by app ID. apps.developerInstall
// is idempotent — it re-issues the tokens already held by the installation — so
// re-running it is the read operation Slack gives us.
func (r *installResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	appID := req.ID

	scopes, err := r.appBotScopes(ctx, appID)
	if err != nil {
		resp.Diagnostics.AddError("Import Failed", fmt.Sprintf("Unable to read bot scopes from the app manifest: %s", err))
		return
	}
	result, err := r.install(ctx, appID, scopes)
	if err != nil {
		resp.Diagnostics.AddError("Import Failed",
			fmt.Sprintf("Unable to recover the installation of %q: %s. The app must already be installed "+
				"(or installable without approval) for the token user's workspace.", appID, err))
		return
	}
	if result.APIAccessTokens.Bot == "" {
		resp.Diagnostics.AddError("Import Failed", "Installation succeeded but Slack returned no bot token.")
		return
	}

	state := installResourceModel{
		ID:             types.StringValue(appID),
		AppID:          types.StringValue(appID),
		ApprovalReason: types.StringNull(),
		BotToken:       types.StringValue(result.APIAccessTokens.Bot),
	}
	if result.APIAccessTokens.User != "" {
		state.UserToken = types.StringValue(result.APIAccessTokens.User)
	} else {
		state.UserToken = types.StringNull()
	}
	scopesList, diags := types.ListValueFrom(ctx, types.StringType, scopes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.BotScopes = scopesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
