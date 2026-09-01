package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                 = &manifestResource{}
	_ resource.ResourceWithConfigure    = &manifestResource{}
	_ resource.ResourceWithImportState  = &manifestResource{}
	_ resource.ResourceWithModifyPlan   = &manifestResource{}
	_ resource.ResourceWithUpgradeState = &manifestResource{}
)

func NewManifestResource() resource.Resource {
	return &manifestResource{}
}

type manifestResource struct {
	client *SlackClient
}

type manifestResourceModel struct {
	ID                types.String  `tfsdk:"id"`
	Manifest          types.Dynamic `tfsdk:"manifest"`
	Scopes            types.Object  `tfsdk:"scopes"`
	ExportCredentials types.Bool    `tfsdk:"export_credentials"`
	ClientID          types.String  `tfsdk:"client_id"`
	ClientSecret      types.String  `tfsdk:"client_secret"`
	VerificationToken types.String  `tfsdk:"verification_token"`
	SigningSecret     types.String  `tfsdk:"signing_secret"`
	OAuthAuthorizeURL types.String  `tfsdk:"oauth_authorize_url"`
}

type manifestRequest struct {
	AppID    string `json:"app_id,omitempty"`
	Manifest string `json:"manifest,omitempty"`
}

type manifestCreateResponse struct {
	AppID       string `json:"app_id"`
	Credentials struct {
		ClientID          string `json:"client_id"`
		ClientSecret      string `json:"client_secret"`
		VerificationToken string `json:"verification_token"`
		SigningSecret     string `json:"signing_secret"`
	} `json:"credentials"`
	OAuthAuthorizeURL string `json:"oauth_authorize_url"`
}

type manifestExportResponse struct {
	Manifest interface{} `json:"manifest"`
}

func (r *manifestResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_manifest"
}

func (r *manifestResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// Version 1: manifest changed from a string attribute to a dynamic
		// attribute (JSON string or HCL object).
		Version: 1,
		MarkdownDescription: "Manages a Slack app via its manifest.\n\n" +
			"Apps that were not created by this provider can be imported by app ID " +
			"(`terraform import slack-app_manifest.example A0123456789`). Any app where the " +
			"authenticated user is a collaborator can be imported. Credentials (`client_secret`, " +
			"`signing_secret`, etc.) are only returned by Slack at creation time, so they start out " +
			"unset for imported apps. They can be backfilled by setting the corresponding attributes " +
			"in config (copy the values from the app's Basic Information page); after one apply the " +
			"values persist in state and the attributes can be removed from config again. Only set " +
			"these attributes for imported apps.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The app ID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"manifest": schema.DynamicAttribute{
				Required: true,
				MarkdownDescription: "The app manifest as an HCL object (not a JSON string — no " +
					"`jsonencode`). Computed values inside it (e.g. a description built from another " +
					"resource) stay localized, so the scopes remain known at plan time and dependent " +
					"installs are not disturbed. Compared semantically: changes to object key order or " +
					"array element order (Slack treats manifest arrays such as scopes as sets) do not " +
					"produce a diff. Attributes Slack adds server-side (`always_online`, `pkce_enabled`, " +
					"`is_mcp_enabled`, `token_rotation_enabled`, ...) are normalized to their known default " +
					"values, so omitting them in config is not a diff — but a remote value changed away " +
					"from its default is.",
				PlanModifiers: []planmodifier.Dynamic{
					manifestSemanticEquality{},
				},
			},
			"scopes": schema.SingleNestedAttribute{
				Computed:   true,
				Attributes: scopesSchemaAttributes(false),
				MarkdownDescription: "The OAuth scopes declared in the manifest (`oauth_config.scopes`), " +
					"resolved at plan time. Wire `slack-app_install.scopes` to this attribute so scope " +
					"changes re-install the app in the same run.",
				PlanModifiers: []planmodifier.Object{
					manifestScopes{},
				},
			},
			"export_credentials": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				MarkdownDescription: "Whether to export generated credentials and the OAuth authorization URL " +
					"to Terraform state. Defaults to `true`; set to `false` to keep them out of state. " +
					"Credential attributes set explicitly in config are kept regardless, since they are " +
					"already in config.",
			},
			"client_id": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "OAuth client ID. Set by Slack at creation; set manually to backfill imported apps.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"client_secret": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "OAuth client secret. Set by Slack at creation; set manually to backfill imported apps.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"verification_token": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Deprecated Slack request verification token. Prefer `signing_secret`.",
				DeprecationMessage:  "We strongly recommend using the, more secure, `signing_secret` instead.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"signing_secret": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Secret used to verify that requests come from Slack. Set by Slack at creation; set manually to backfill imported apps.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"oauth_authorize_url": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Full URL for OAuth authorization.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *manifestResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*SlackClient)
}

// manifestResourceModelV0 is the schema-version-0 model, where manifest was a
// plain string attribute.
type manifestResourceModelV0 struct {
	ID                types.String `tfsdk:"id"`
	Manifest          types.String `tfsdk:"manifest"`
	Scopes            types.Object `tfsdk:"scopes"`
	ExportCredentials types.Bool   `tfsdk:"export_credentials"`
	ClientID          types.String `tfsdk:"client_id"`
	ClientSecret      types.String `tfsdk:"client_secret"`
	VerificationToken types.String `tfsdk:"verification_token"`
	SigningSecret     types.String `tfsdk:"signing_secret"`
	OAuthAuthorizeURL types.String `tfsdk:"oauth_authorize_url"`
}

// UpgradeState migrates version-0 state (manifest stored as a JSON string) to
// the dynamic manifest attribute, converting the string to object form.
func (r *manifestResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id":       schema.StringAttribute{Computed: true},
					"manifest": schema.StringAttribute{Required: true},
					"scopes": schema.SingleNestedAttribute{
						Computed:   true,
						Attributes: scopesSchemaAttributes(false),
					},
					"export_credentials": schema.BoolAttribute{Optional: true, Computed: true},
					"client_id":          schema.StringAttribute{Optional: true, Computed: true},
					"client_secret":      schema.StringAttribute{Optional: true, Computed: true, Sensitive: true},
					"verification_token": schema.StringAttribute{Optional: true, Computed: true, Sensitive: true},
					"signing_secret":     schema.StringAttribute{Optional: true, Computed: true, Sensitive: true},
					"oauth_authorize_url": schema.StringAttribute{
						Computed: true,
					},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior manifestResourceModelV0
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}
				var decoded interface{}
				if err := json.Unmarshal([]byte(prior.Manifest.ValueString()), &decoded); err != nil {
					resp.Diagnostics.AddError("State Upgrade Failed",
						fmt.Sprintf("The stored manifest is not valid JSON: %s", err))
					return
				}
				upgraded := manifestResourceModel{
					ID:                prior.ID,
					Manifest:          types.DynamicValue(goToAttr(ctx, decoded)),
					Scopes:            prior.Scopes,
					ExportCredentials: prior.ExportCredentials,
					ClientID:          prior.ClientID,
					ClientSecret:      prior.ClientSecret,
					VerificationToken: prior.VerificationToken,
					SigningSecret:     prior.SigningSecret,
					OAuthAuthorizeURL: prior.OAuthAuthorizeURL,
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

// manifestSemanticEquality suppresses plan diffs for manifest changes that are
// only cosmetic: key order or array element order. When the planned value is
// semantically equal to state, the prior state value is kept so no update is
// planned.
type manifestSemanticEquality struct{}

func (m manifestSemanticEquality) Description(ctx context.Context) string {
	return m.MarkdownDescription(ctx)
}

func (m manifestSemanticEquality) MarkdownDescription(context.Context) string {
	return "Ignores object key order and array element order when comparing manifests."
}

func (m manifestSemanticEquality) PlanModifyDynamic(_ context.Context, req planmodifier.DynamicRequest, resp *planmodifier.DynamicResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() {
		return
	}
	planJSON, err := dynamicManifestJSON(req.PlanValue)
	if err != nil {
		return // unknown (or unusable) planned value: nothing to compare
	}
	stateJSON, err := dynamicManifestJSON(req.StateValue)
	if err != nil {
		return
	}
	if jsonSemanticallyEqual(planJSON, stateJSON) {
		resp.PlanValue = req.StateValue
	}
}

// manifestScopes derives the computed scopes attribute from the planned
// manifest, so it is known at plan time and downstream references (an install
// wired to it) see scope changes in the same plan. Set semantics make element
// order irrelevant, so pure reordering never ripples into dependent resources.
type manifestScopes struct{}

func (m manifestScopes) Description(ctx context.Context) string {
	return m.MarkdownDescription(ctx)
}

func (m manifestScopes) MarkdownDescription(context.Context) string {
	return "Derives scopes from the planned manifest at plan time."
}

func (m manifestScopes) PlanModifyObject(ctx context.Context, req planmodifier.ObjectRequest, resp *planmodifier.ObjectResponse) {
	var manifest types.Dynamic
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("manifest"), &manifest)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A partially unknown object manifest still yields known scopes as long
	// as the oauth_config.scopes subtree itself is known.
	scopes, known := dynamicScopes(manifest)
	if !known {
		resp.PlanValue = types.ObjectUnknown(scopesAttrTypes())
		return
	}

	value, diags := scopesObject(ctx, scopes)
	resp.Diagnostics.Append(diags...)
	resp.PlanValue = value
}

// refreshScopes recomputes the model's scopes from its manifest.
func refreshScopes(ctx context.Context, model *manifestResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	manifestJSON, err := dynamicManifestJSON(model.Manifest)
	if err != nil {
		diags.AddError("Provider Error", fmt.Sprintf("Unable to render the manifest as JSON: %s", err))
		return diags
	}
	value, d := scopesObject(ctx, scopesFromManifest(manifestJSON))
	diags.Append(d...)
	model.Scopes = value
	return diags
}

func exportsCredentials(exportCredentials types.Bool) bool {
	return exportCredentials.IsNull() || exportCredentials.IsUnknown() || exportCredentials.ValueBool()
}

// ModifyPlan nulls out the credential outputs at plan time when export_credentials
// is false, so the applied state matches the plan. Credential attributes explicitly
// set in config are left alone — Terraform requires state to match config for them,
// and hiding a value that already lives in config would gain nothing.
func (r *manifestResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan, config manifestResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Manifest.IsNull() && !config.Manifest.IsUnknown() && !config.Manifest.IsUnderlyingValueUnknown() {
		switch config.Manifest.UnderlyingValue().(type) {
		case types.Object, types.Map:
		case types.String:
			resp.Diagnostics.AddAttributeError(path.Root("manifest"), "Invalid Manifest Type",
				"The manifest must be an HCL object. Remove the jsonencode(...) wrapper and pass the "+
					"object directly: manifest = { display_information = { ... }, ... }")
			return
		default:
			resp.Diagnostics.AddAttributeError(path.Root("manifest"), "Invalid Manifest Type",
				"The manifest must be an HCL object.")
			return
		}
	}

	if exportsCredentials(plan.ExportCredentials) {
		return
	}

	if config.ClientID.IsNull() {
		plan.ClientID = types.StringNull()
	}
	if config.ClientSecret.IsNull() {
		plan.ClientSecret = types.StringNull()
	}
	if config.VerificationToken.IsNull() {
		plan.VerificationToken = types.StringNull()
	}
	if config.SigningSecret.IsNull() {
		plan.SigningSecret = types.StringNull()
	}
	plan.OAuthAuthorizeURL = types.StringNull()

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan manifestResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	manifestJSON, err := dynamicManifestJSON(plan.Manifest)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Manifest", fmt.Sprintf("Unable to render the manifest as JSON: %s", err))
		return
	}

	var result manifestCreateResponse
	err = r.client.JSONRequest(ctx, "apps.manifest.create", manifestRequest{
		Manifest: normalizeManifest(manifestJSON),
	}, &result)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create app: %s", err))
		return
	}

	plan.ID = types.StringValue(result.AppID)
	if exportsCredentials(plan.ExportCredentials) {
		plan.ClientID = types.StringValue(result.Credentials.ClientID)
		plan.ClientSecret = types.StringValue(result.Credentials.ClientSecret)
		plan.VerificationToken = types.StringValue(result.Credentials.VerificationToken)
		plan.SigningSecret = types.StringValue(result.Credentials.SigningSecret)
		plan.OAuthAuthorizeURL = types.StringValue(result.OAuthAuthorizeURL)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state manifestResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var result manifestExportResponse
	err := r.client.JSONRequest(ctx, "apps.manifest.export", manifestRequest{
		AppID: state.ID.ValueString(),
	}, &result)
	if err != nil {
		if isAppAccessError(err) {
			// The app was deleted out-of-band (or access was lost); drop it
			// from state so the next apply recreates it.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read app manifest: %s", err))
		return
	}

	// Only rewrite state when the exported manifest genuinely differs; Slack
	// reorders keys and array elements, which is not drift.
	normalized, _ := json.Marshal(result.Manifest)
	stateJSON := ""
	if !state.Manifest.IsNull() {
		if rendered, err := dynamicManifestJSON(state.Manifest); err == nil {
			stateJSON = rendered
		}
	}
	if !jsonSemanticallyEqual(stateJSON, string(normalized)) {
		state.Manifest = types.DynamicValue(goToAttr(ctx, result.Manifest))
	}
	resp.Diagnostics.Append(refreshScopes(ctx, &state)...)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan manifestResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	manifestJSON, err := dynamicManifestJSON(plan.Manifest)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Manifest", fmt.Sprintf("Unable to render the manifest as JSON: %s", err))
		return
	}

	err = r.client.JSONRequest(ctx, "apps.manifest.update", manifestRequest{
		AppID:    plan.ID.ValueString(),
		Manifest: normalizeManifest(manifestJSON),
	}, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update app: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *manifestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state manifestResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.JSONRequest(ctx, "apps.manifest.delete", manifestRequest{
		AppID: state.ID.ValueString(),
	}, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete app: %s", err))
		return
	}

	resp.State.RemoveResource(ctx)
}

func (r *manifestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
