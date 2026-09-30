package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &conversationMemberResource{}
	_ resource.ResourceWithConfigure   = &conversationMemberResource{}
	_ resource.ResourceWithImportState = &conversationMemberResource{}
)

func NewConversationMemberResource() resource.Resource {
	return &conversationMemberResource{}
}

type conversationMemberResource struct {
	client *SlackClient
}

type conversationMemberResourceModel struct {
	ID        types.String `tfsdk:"id"`
	ChannelID types.String `tfsdk:"channel_id"`
	UserID    types.String `tfsdk:"user_id"`
}

type conversationMembersResponse struct {
	Members []string `json:"members"`
}

func (r *conversationMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_conversation_member"
}

func (r *conversationMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adds a user to a Slack channel with `conversations.invite`. Destroying the resource removes the user with `conversations.kick`. Requires the provider `bot_token`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"channel_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the public or private channel.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace user to add to the channel.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *conversationMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*SlackClient)
}

func (r *conversationMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan conversationMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.BotJSONRequest(ctx, "conversations.invite", struct {
		Channel string `json:"channel"`
		Users   string `json:"users"`
	}{Channel: plan.ChannelID.ValueString(), Users: plan.UserID.ValueString()}, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add user to conversation: %s", err))
		return
	}

	plan.ID = types.StringValue(plan.ChannelID.ValueString() + "/" + plan.UserID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *conversationMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state conversationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var result conversationMembersResponse
	err := r.client.BotFormRequest(ctx, "conversations.members", url.Values{
		"channel": {state.ChannelID.ValueString()},
	}, &result)
	if err != nil {
		if isConversationMemberNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read conversation members: %s", err))
		return
	}

	for _, userID := range result.Members {
		if userID == state.UserID.ValueString() {
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *conversationMemberResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

func (r *conversationMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state conversationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.BotJSONRequest(ctx, "conversations.kick", struct {
		Channel string `json:"channel"`
		User    string `json:"user"`
	}{Channel: state.ChannelID.ValueString(), User: state.UserID.ValueString()}, nil)
	if err != nil && !isConversationMemberNotFoundError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove user from conversation: %s", err))
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *conversationMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	channelID, userID, ok := strings.Cut(req.ID, "/")
	if !ok || channelID == "" || userID == "" || strings.Contains(userID, "/") {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected an import ID in the form \"channel_id/user_id\", got %q.", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("channel_id"), channelID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), userID)...)
}

func isConversationMemberNotFoundError(err error) bool {
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	switch apiErr.Code {
	case "channel_not_found", "user_not_found", "not_in_channel", "user_not_in_channel":
		return true
	default:
		return false
	}
}
