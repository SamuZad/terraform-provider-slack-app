package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
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
	Scopes         types.Object `tfsdk:"scopes"`
	BotToken       types.String `tfsdk:"bot_token"`
	UserToken      types.String `tfsdk:"user_token"`
	AppToken       types.String `tfsdk:"app_token"`
}

type installRequest struct {
	AppID      string   `json:"app_id"`
	BotScopes  []string `json:"bot_scopes"`
	UserScopes []string `json:"user_scopes,omitempty"`
}

type installResponse struct {
	APIAccessTokens struct {
		Bot      string `json:"bot"`
		User     string `json:"user"`
		AppLevel string `json:"app_level"`
	} `json:"api_access_tokens"`
}

type approvalCreateRequest struct {
	App        string `json:"app"`
	TeamID     string `json:"team_id"`
	BotScopes  string `json:"bot_scopes"`
	UserScopes string `json:"user_scopes,omitempty"`
	Reason     string `json:"reason,omitempty"`
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
			"The scopes (bot and user) are read from the app's manifest.\n\n" +
			"The install is attempted directly first; if the app is already approved (or the workspace does " +
			"not require admin approval), no approval request is submitted. Only when the direct install fails " +
			"and `approval_reason` is set does the resource submit an admin approval request and wait for " +
			"it — polling every 10 seconds until the provider's `approval_timeout_seconds` (default 1 hour) " +
			"elapses.\n\n" +
			"Scope changes (bot and user scopes alike) plan an in-place re-install; Slack re-issues the " +
			"same tokens with the new scope grants, and manifest changes that do not touch the scopes " +
			"never trigger a re-install. Wire `scopes = slack-app_manifest.example.scopes` so a scope " +
			"change re-installs in the same run; when omitted, the resource compares the app's live " +
			"scopes at plan time instead, picking up a scope change on the plan after the manifest " +
			"update is applied.\n\n" +
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
			},
			"scopes": schema.SingleNestedAttribute{
				Optional:   true,
				Computed:   true,
				Attributes: scopesSchemaAttributes(true),
				MarkdownDescription: "The bot and user scopes to install with. Wire this to the manifest " +
					"resource (`scopes = slack-app_manifest.example.scopes`) so scope changes re-install the " +
					"app in the same run. When omitted, the scopes are resolved from the app's manifest at " +
					"plan time (or at install time when the app is created in the same run), and changes are " +
					"detected on the plan after the manifest update is applied.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
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
			"app_token": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				MarkdownDescription: "The app-level token (`xapp-…`) issued by the installation, if applicable.",
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

// ModifyPlan resolves scopes at plan time and plans an in-place re-install
// when they change. When scopes is not set in config, the app's manifest is
// read and compared against the installed scopes — so an invalid app fails
// the plan, not the apply; a configured value (wired to the manifest
// resource) carries the change through the graph instead. Either way, a scope
// change means the tokens will be re-issued, so they are marked unknown.
func (r *installResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Nothing to plan on destroy.
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan, config installResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create: resolve the scopes from the app at plan time when the app ID is
	// already known (a literal or an existing app). An unknown app ID — the
	// manifest is being created in the same run — resolves at apply instead.
	if req.State.Raw.IsNull() {
		if !config.Scopes.IsNull() || plan.AppID.IsUnknown() {
			return
		}
		appID := plan.AppID.ValueString()
		current, err := r.appScopes(ctx, appID)
		if err != nil {
			if isAppAccessError(err) {
				resp.Diagnostics.AddError("App Not Found or Not Accessible", appAccessErrorDetail(appID))
				return
			}
			// Transient errors are left for the apply to surface.
			return
		}
		currentObject, diags := scopesObject(ctx, current)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("scopes"), currentObject)...)
		return
	}

	var state installResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A changed app_id already forces a replacement; the old app may be gone.
	if !plan.AppID.Equal(state.AppID) {
		return
	}

	installed, diags := scopesFromObject(ctx, state.Scopes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	planned := plan.Scopes
	if config.Scopes.IsNull() {
		current, err := r.appScopes(ctx, state.AppID.ValueString())
		if err != nil {
			if isAppAccessError(err) {
				// The app was deleted out-of-band; leave the plan alone and
				// let the manifest resource drive recreation.
				return
			}
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read scopes from the app manifest: %s", err))
			return
		}
		if sameOAuthScopes(installed, current) {
			return
		}
		currentObject, diags := scopesObject(ctx, current)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		planned = currentObject
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("scopes"), currentObject)...)
	}

	scopesChanged := true
	if !planned.IsNull() && !planned.IsUnknown() {
		newScopes, diags := scopesFromObject(ctx, planned)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		scopesChanged = !sameOAuthScopes(installed, newScopes)
	}
	if scopesChanged {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("bot_token"), types.StringUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("user_token"), types.StringUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("app_token"), types.StringUnknown())...)
	}
}

// appScopes reads the app's current bot and user scopes from its manifest.
func (r *installResource) appScopes(ctx context.Context, appID string) (oauthScopes, error) {
	var result manifestExportResponse
	err := r.client.JSONRequest(ctx, "apps.manifest.export", manifestRequest{AppID: appID}, &result)
	if err != nil {
		return oauthScopes{}, fmt.Errorf("apps.manifest.export: %w", err)
	}
	normalized, err := json.Marshal(result.Manifest)
	if err != nil {
		return oauthScopes{}, err
	}
	return scopesFromManifest(string(normalized)), nil
}

func (r *installResource) install(ctx context.Context, appID string, scopes oauthScopes) (*installResponse, error) {
	var result installResponse
	err := r.client.JSONRequest(ctx, "apps.developerInstall", installRequest{
		AppID:      appID,
		BotScopes:  emptyIfNil(scopes.Bot),
		UserScopes: scopes.User,
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
// until the install succeeds. The install attempt itself is the approval
// check: apps.developerInstall is idempotent and succeeds exactly once the
// app is approved, so polling it cannot miss an approval the way matching
// request IDs can (Slack re-uses or renames requests on repeat submissions).
// The request status is only consulted to fail fast on an explicit denial or
// cancellation.
func (r *installResource) waitForApproval(ctx context.Context, appID string, scopes oauthScopes, reason string) (*installResponse, error) {
	teamID, err := r.client.TeamID(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to determine the workspace to request approval for: %w", err)
	}

	var created approvalCreateResponse
	err = r.client.JSONRequest(ctx, "apps.approvals.requests.create", approvalCreateRequest{
		App:        appID,
		TeamID:     teamID,
		BotScopes:  strings.Join(scopes.Bot, ","),
		UserScopes: strings.Join(scopes.User, ","),
		Reason:     reason,
	}, &created)
	if err != nil {
		return nil, fmt.Errorf("unable to create approval request: %w", err)
	}

	deadline := time.Now().Add(r.client.ApprovalTimeout)
	for {
		if created.RequestID != "" {
			status, err := r.lookupApprovalStatus(ctx, appID, teamID, created.RequestID)
			if err != nil {
				return nil, fmt.Errorf("unable to check approval status: %w", err)
			}
			if status == "denied" || status == "cancelled" {
				return nil, fmt.Errorf("approval request %s was %s", created.RequestID, status)
			}
		}

		result, installErr := r.install(ctx, appID, scopes)
		if installErr == nil {
			return result, nil
		}

		if time.Now().After(deadline) {
			r.cancelApproval(ctx, appID)
			return nil, fmt.Errorf("the application was not approved within the timeout period (%s), "+
				"the last install attempt failed with %q; the approval request has been cancelled",
				r.client.ApprovalTimeout, installErr)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
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

// applyInstall resolves the scopes, performs the install (trying directly
// first, then falling back to admin approval when approval_reason is set),
// and fills the computed attributes on the model.
func (r *installResource) applyInstall(ctx context.Context, plan *installResourceModel, diags *diag.Diagnostics) {
	appID := plan.AppID.ValueString()

	var scopes oauthScopes
	if !plan.Scopes.IsNull() && !plan.Scopes.IsUnknown() {
		var d diag.Diagnostics
		scopes, d = scopesFromObject(ctx, plan.Scopes)
		diags.Append(d...)
		if diags.HasError() {
			return
		}
	} else {
		var err error
		scopes, err = r.appScopes(ctx, appID)
		if err != nil {
			if isAppAccessError(err) {
				diags.AddError("App Not Found or Not Accessible", appAccessErrorDetail(appID))
			} else {
				diags.AddError("Client Error", fmt.Sprintf("Unable to read scopes from the app manifest: %s", err))
			}
			return
		}
		scopesValue, d := scopesObject(ctx, scopes)
		diags.Append(d...)
		if diags.HasError() {
			return
		}
		plan.Scopes = scopesValue
	}

	// Try the direct install first: if the app is already approved (or the
	// workspace requires no approval), no approval request is needed.
	result, installErr := r.install(ctx, appID, scopes)
	if installErr != nil {
		if isAppAccessError(installErr) {
			diags.AddError("App Not Found or Not Accessible", appAccessErrorDetail(appID))
			return
		}
		if plan.ApprovalReason.IsNull() {
			diags.AddError("Client Error", fmt.Sprintf("Unable to install app: %s", installErr))
			return
		}

		var err error
		result, err = r.waitForApproval(ctx, appID, scopes, plan.ApprovalReason.ValueString())
		if err != nil {
			diags.AddError("Approval Failed",
				fmt.Sprintf("Direct install failed (%s) and the approval fallback did not succeed: %s", installErr, err))
			return
		}
	}

	if result.APIAccessTokens.Bot == "" {
		diags.AddError("Install Failed", "Installation succeeded but Slack returned no bot token.")
		return
	}

	plan.ID = types.StringValue(appID)
	setTokens(plan, result)
}

func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func setTokens(model *installResourceModel, result *installResponse) {
	model.BotToken = types.StringValue(result.APIAccessTokens.Bot)
	model.UserToken = stringOrNull(result.APIAccessTokens.User)
	model.AppToken = stringOrNull(result.APIAccessTokens.AppLevel)
}

func (r *installResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan installResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.applyInstall(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
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

func (r *installResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan installResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// ModifyPlan marks the tokens unknown exactly when the scopes changed and
	// a re-install is needed; other updates (e.g. approval_reason) are
	// state-only.
	if plan.BotToken.IsUnknown() {
		r.applyInstall(ctx, &plan, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *installResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

// ImportState recovers an existing installation by app ID. apps.developerInstall
// is idempotent — it re-issues the tokens already held by the installation — so
// re-running it is the read operation Slack gives us.
func (r *installResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	appID := req.ID

	scopes, err := r.appScopes(ctx, appID)
	if err != nil {
		if isAppAccessError(err) {
			resp.Diagnostics.AddError("Import Failed", appAccessErrorDetail(appID))
		} else {
			resp.Diagnostics.AddError("Import Failed", fmt.Sprintf("Unable to read scopes from the app manifest: %s", err))
		}
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
	}
	setTokens(&state, result)
	scopesValue, diags := scopesObject(ctx, scopes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Scopes = scopesValue

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
