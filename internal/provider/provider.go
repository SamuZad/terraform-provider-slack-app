package provider

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &slackAppProvider{}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &slackAppProvider{version: version}
	}
}

type slackAppProvider struct {
	version string
}

type slackAppProviderModel struct {
	Token                  types.String `tfsdk:"token"`
	BaseURL                types.String `tfsdk:"base_url"`
	TeamID                 types.String `tfsdk:"team_id"`
	ApprovalTimeoutSeconds types.Int64  `tfsdk:"approval_timeout_seconds"`
}

func (p *slackAppProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "slack-app"
	resp.Version = p.version
}

func (p *slackAppProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the full developer lifecycle of Slack apps: the app manifest, app collaborators, " +
			"and developer installs (which return bot and user tokens, and can wait for admin approval first).\n\n" +
			"Existing apps that were not created by this provider can be imported; any app where the " +
			"authenticated user is a collaborator can be imported.\n\n" +
			"**Token requirements:** the provider works with two types of credentials. A Slack CLI service token " +
			"(obtained via `slack auth token`) works with all resources in this provider. An app configuration token " +
			"(https://api.slack.com/authentication/config-tokens) can **only** be used for the `manifest` resource.\n\n" +
			"Both credential types are issued to a specific Slack user. That user is a collaborator on every app " +
			"managed through this provider, and the `collaborator` resource refuses to remove them: doing so would " +
			"revoke the provider's own access to the app and orphan its resources.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "A Slack CLI service token from `slack auth token`, or an app " +
					"configuration token (manifest resource only). " +
					"Can be set via the `SLACK_TOKEN` environment variable.",
			},
			"base_url": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Base URL of the Slack API, for GovSlack or testing. " +
					"Defaults to `https://slack.com/api/`.",
			},
			"team_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "The workspace (team) ID installation approvals are requested for. " +
					"Defaults to the workspace the token belongs to; only needs setting in Enterprise Grid " +
					"setups where the token user spans multiple workspaces.",
			},
			"approval_timeout_seconds": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "How long `slack-app_install` waits for an admin approval request " +
					"to be granted before failing. Defaults to 3600 (1 hour).",
			},
		},
	}
}

func (p *slackAppProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config slackAppProviderModel
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}
	if config.Token.IsNull() {
		config.Token = types.StringValue(os.Getenv("SLACK_TOKEN"))
	}
	client := NewSlackClient(config.Token.ValueString())
	if !config.BaseURL.IsNull() {
		base := config.BaseURL.ValueString()
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		client.baseURL = base
	}
	if !config.TeamID.IsNull() {
		client.teamID = config.TeamID.ValueString()
	}
	if !config.ApprovalTimeoutSeconds.IsNull() {
		client.ApprovalTimeout = time.Duration(config.ApprovalTimeoutSeconds.ValueInt64()) * time.Second
	}
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *slackAppProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func (p *slackAppProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewManifestResource,
		NewCollaboratorResource,
		NewInstallResource,
	}
}
