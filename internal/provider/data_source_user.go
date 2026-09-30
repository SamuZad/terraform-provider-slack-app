package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &userDataSource{}
	_ datasource.DataSourceWithConfigure = &userDataSource{}
)

func NewUserDataSource() datasource.DataSource {
	return &userDataSource{}
}

type userDataSource struct {
	client *SlackClient
}

type userDataSourceModel struct {
	ID     types.String `tfsdk:"id"`
	Email  types.String `tfsdk:"email"`
	UserID types.String `tfsdk:"user_id"`
}

type userLookupResponse struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a Slack workspace user ID by email address using `users.lookupByEmail`. Requires the provider `bot_token` with the `users:read.email` scope.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"email": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The user's email address.",
			},
			"user_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The Slack user ID.",
			},
		},
	}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, _ *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	d.client = req.ProviderData.(*SlackClient)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config userDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var result userLookupResponse
	err := d.client.BotFormRequest(ctx, "users.lookupByEmail", url.Values{
		"email": {config.Email.ValueString()},
	}, &result)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to look up Slack user: %s", err))
		return
	}

	config.ID = types.StringValue(result.User.ID)
	config.UserID = types.StringValue(result.User.ID)
	if result.User.Email != "" {
		config.Email = types.StringValue(result.User.Email)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
