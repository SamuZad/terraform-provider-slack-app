package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &conversationResource{}
	_ resource.ResourceWithConfigure   = &conversationResource{}
	_ resource.ResourceWithImportState = &conversationResource{}
	_ resource.ResourceWithModifyPlan  = &conversationResource{}
)

func NewConversationResource() resource.Resource {
	return &conversationResource{}
}

type conversationResource struct {
	client *SlackClient
}

type conversationResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	IsPrivate types.Bool   `tfsdk:"is_private"`
	TeamID    types.String `tfsdk:"team_id"`
}

type conversationCreateRequest struct {
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private,omitempty"`
	TeamID    string `json:"team_id,omitempty"`
}

type conversationResponse struct {
	Channel struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		IsPrivate bool   `json:"is_private"`
	} `json:"channel"`
}

func (r *conversationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_conversation"
}

func (r *conversationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a Slack public or private channel with `conversations.create`. " +
			"Destroying the resource archives the channel. Existing channels can be imported by channel ID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the public or private channel. Slack allows lowercase letters, numbers, hyphens, and underscores, up to 80 characters.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"is_private": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether to create a private channel. Defaults to false.",
			},
			"team_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The workspace ID, required when using an organization-level token.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *conversationResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var plan, state conversationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || plan.IsPrivate.IsUnknown() || state.IsPrivate.IsUnknown() {
		return
	}

	if !state.IsPrivate.Equal(plan.IsPrivate) {
		resp.Diagnostics.AddAttributeError(
			path.Root("is_private"),
			"Cannot Change Conversation Privacy",
			"The provider cannot change a conversation's public/private status. Do this manually in Slack, then update Terraform to match.",
		)
	}
}

func (r *conversationResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = req.ProviderData.(*SlackClient)
}

func (r *conversationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan conversationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var result conversationResponse
	err := r.client.BotJSONRequest(ctx, "conversations.create", conversationCreateRequest{
		Name:      plan.Name.ValueString(),
		IsPrivate: plan.IsPrivate.ValueBool(),
		TeamID:    plan.TeamID.ValueString(),
	}, &result)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create conversation: %s", err))
		return
	}

	plan.ID = types.StringValue(result.Channel.ID)
	plan.Name = types.StringValue(result.Channel.Name)
	plan.IsPrivate = types.BoolValue(result.Channel.IsPrivate)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *conversationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state conversationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var result conversationResponse
	err := r.client.BotFormRequest(ctx, "conversations.info", url.Values{
		"channel": {state.ID.ValueString()},
	}, &result)
	if err != nil {
		if isConversationNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read conversation: %s", err))
		return
	}

	state.Name = types.StringValue(result.Channel.Name)
	state.IsPrivate = types.BoolValue(result.Channel.IsPrivate)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *conversationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state conversationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *conversationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state conversationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.BotJSONRequest(ctx, "conversations.archive", struct {
		Channel string `json:"channel"`
	}{Channel: state.ID.ValueString()}, nil)
	if err != nil && !isConversationNotFoundError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive conversation: %s", err))
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *conversationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func isConversationNotFoundError(err error) bool {
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	switch apiErr.Code {
	case "channel_not_found", "conversation_not_found", "is_archived", "already_archived":
		return true
	default:
		return false
	}
}
