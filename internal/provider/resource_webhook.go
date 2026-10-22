package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

const privateHashWebhookSecretKey = "hash_secret_key"

var (
	_ resource.Resource                = (*webhookResource)(nil)
	_ resource.ResourceWithConfigure   = (*webhookResource)(nil)
	_ resource.ResourceWithImportState = (*webhookResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*webhookResource)(nil)
)

// NewWebhookResource returns the cdnpro_webhook resource.
func NewWebhookResource() resource.Resource { return &webhookResource{} }

type webhookResource struct {
	data *ProviderData
}

type webhookModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	URL            types.String `tfsdk:"url"`
	Credentials    types.Object `tfsdk:"credentials"`
	CreationTime   types.String `tfsdk:"creation_time"`
	LastUpdateTime types.String `tfsdk:"last_update_time"`
	LastCallTime   types.String `tfsdk:"last_call_time"`
	TotalCalls     types.Int64  `tfsdk:"total_calls"`
}

type webhookCredentialsModel struct {
	User      types.String `tfsdk:"user"`
	SecretKey types.String `tfsdk:"secret_key"`
}

var (
	webhookSchemaOnce         = buildWebhookSchema()
	webhookCredentialsObjType = nestedObjectType(webhookSchemaOnce.Attributes["credentials"])
	noSpaces                  = regexp.MustCompile(`^\S+$`)
)

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = webhookSchemaOnce
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildWebhookSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "A CDN Pro webhook, called when a deployment, purge or prefetch finishes. " +
			"`credentials.secret_key` is write-only and tracked by hash. The API cannot remove credentials once set, " +
			"so dropping the `credentials` block replaces the webhook. Requires Terraform 1.11 or later.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Webhook ID. Only available from the creation response, never from a later read.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:   true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 250)},
			},
			"description": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtMost(500)},
			},
			"url": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(250),
					stringvalidator.RegexMatches(regexp.MustCompile(`^https?://`), "must start with http:// or https://"),
				},
				MarkdownDescription: "HTTP or HTTPS endpoint that receives the POST.",
			},
			"credentials": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "HTTP Basic credentials sent with the callback. Both fields are required together.",
				Attributes: map[string]schema.Attribute{
					"user": schema.StringAttribute{
						Required: true,
						Validators: []validator.String{
							stringvalidator.LengthAtMost(64),
							stringvalidator.RegexMatches(noSpaces, "must not contain spaces"),
						},
					},
					"secret_key": schema.StringAttribute{
						Required:  true,
						WriteOnly: true,
						Sensitive: true,
						Validators: []validator.String{
							stringvalidator.LengthAtMost(64),
							stringvalidator.RegexMatches(noSpaces, "must not contain spaces"),
						},
						MarkdownDescription: "Password for the callback. Never read back into state.",
					},
				},
			},
			"creation_time": schema.StringAttribute{Computed: true},
			"last_update_time": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Also advanced by the API on every call of the webhook.",
			},
			"last_call_time": schema.StringAttribute{Computed: true},
			"total_calls":    schema.Int64Attribute{Computed: true},
		},
	}
}

func credentialsOf(ctx context.Context, obj types.Object, diags *diag.Diagnostics) *webhookCredentialsModel {
	if obj.IsNull() || obj.IsUnknown() {
		return nil
	}
	var c webhookCredentialsModel
	diags.Append(obj.As(ctx, &c, objectAsOptions)...)
	return &c
}

func (r *webhookResource) secretKeyChanged(ctx context.Context, cfg *webhookModel, priv privateGetter) bool {
	var diags diag.Diagnostics
	c := credentialsOf(ctx, cfg.Credentials, &diags)
	if c == nil {
		return getPrivate(ctx, priv, privateHashWebhookSecretKey) != ""
	}
	if c.SecretKey.IsUnknown() {
		return true
	}
	return sha256Hex(c.SecretKey.ValueString()) != getPrivate(ctx, priv, privateHashWebhookSecretKey)
}

func (r *webhookResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var cfg, plan, state webhookModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The API offers no way to delete credentials: replace the webhook instead.
	if !state.Credentials.IsNull() && plan.Credentials.IsNull() {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("credentials"))
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("creation_time"), state.CreationTime)...)
	// Any write bumps lastUpdateTime on the server, so keep the state value only
	// when nothing is going to be sent.
	lastUpdate := state.LastUpdateTime
	planUser, stateUser := types.StringNull(), types.StringNull()
	if c := credentialsOf(ctx, plan.Credentials, &resp.Diagnostics); c != nil {
		planUser = c.User
	}
	if c := credentialsOf(ctx, state.Credentials, &resp.Diagnostics); c != nil {
		stateUser = c.User
	}
	if r.secretKeyChanged(ctx, &cfg, req.Private) || !plan.Name.Equal(state.Name) || !plan.URL.Equal(state.URL) ||
		!plan.Description.Equal(state.Description) || !planUser.Equal(stateUser) {
		lastUpdate = types.StringUnknown()
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("last_update_time"), lastUpdate)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("last_call_time"), state.LastCallTime)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("total_calls"), state.TotalCalls)...)
}

func (r *webhookResource) storeHash(ctx context.Context, cfg *webhookModel, priv privateSetter, diags *diag.Diagnostics) {
	hash := ""
	if c := credentialsOf(ctx, cfg.Credentials, diags); c != nil && !c.SecretKey.IsNull() && !c.SecretKey.IsUnknown() {
		hash = sha256Hex(c.SecretKey.ValueString())
	}
	setPrivate(ctx, priv, privateHashWebhookSecretKey, hash, diags)
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, cfg webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiReq := client.WebhookRequest{
		Name:        strPtr(plan.Name),
		Description: strPtr(plan.Description),
		URL:         strPtr(plan.URL),
	}
	if c := credentialsOf(ctx, cfg.Credentials, &resp.Diagnostics); c != nil {
		apiReq.Credentials = &client.WebhookCredentials{User: c.User.ValueString(), SecretKey: c.SecretKey.ValueString()}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.Client.CreateWebhook(ctx, apiReq)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Creating webhook", err)
		return
	}
	r.storeHash(ctx, &cfg, resp.Private, &resp.Diagnostics)
	state, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok || state == nil {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state, lastModified, ok := r.read(ctx, prior.ID.ValueString(), &prior, &resp.Diagnostics)
	if !ok {
		return
	}
	if state == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *webhookResource) read(ctx context.Context, id string, prior *webhookModel, diags *diag.Diagnostics) (*webhookModel, string, bool) {
	w, lastModified, err := r.data.Client.GetWebhook(ctx, id)
	if client.IsNotFound(err) {
		return nil, "", true
	}
	if err != nil {
		addAPIError(diags, "Reading webhook "+id, err)
		return nil, "", false
	}
	m := &webhookModel{
		ID:             types.StringValue(id),
		Name:           types.StringValue(w.Name),
		Description:    strOrNull(w.Description),
		URL:            types.StringValue(w.URL),
		Credentials:    types.ObjectNull(webhookCredentialsObjType.AttrTypes),
		CreationTime:   types.StringValue(w.MetaData.CreationTime),
		LastUpdateTime: types.StringValue(w.MetaData.LastUpdateTime),
		LastCallTime:   types.StringValue(w.MetaData.LastCallTime),
		TotalCalls:     types.Int64Value(w.MetaData.TotalCalls),
	}
	if w.Credentials != nil {
		// secret_key is write-only and must stay null in state.
		obj, d := types.ObjectValueFrom(ctx, webhookCredentialsObjType.AttrTypes, webhookCredentialsModel{
			User:      types.StringValue(w.Credentials.User),
			SecretKey: types.StringNull(),
		})
		diags.Append(d...)
		m.Credentials = obj
	}
	if diags.HasError() {
		return nil, "", false
	}
	return m, lastModified, true
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, cfg webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()

	apiReq := client.WebhookRequest{}
	changed := false
	if !plan.Name.Equal(state.Name) {
		apiReq.Name, changed = strPtr(plan.Name), true
	}
	if !plan.URL.Equal(state.URL) {
		apiReq.URL, changed = strPtr(plan.URL), true
	}
	clearDescription := false
	if !plan.Description.Equal(state.Description) {
		changed = true
		if plan.Description.IsNull() {
			clearDescription = true
		} else {
			apiReq.Description = strPtr(plan.Description)
		}
	}
	planCreds := credentialsOf(ctx, cfg.Credentials, &resp.Diagnostics)
	stateCreds := credentialsOf(ctx, state.Credentials, &resp.Diagnostics)
	keyChanged := r.secretKeyChanged(ctx, &cfg, req.Private)
	if planCreds != nil && (keyChanged || stateCreds == nil || !planCreds.User.Equal(stateCreds.User)) {
		apiReq.Credentials = &client.WebhookCredentials{User: planCreds.User.ValueString(), SecretKey: planCreds.SecretKey.ValueString()}
		changed = true
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if changed {
		patch := client.WebhookPatch(apiReq, clearDescription)
		if err := r.data.Client.PatchWebhook(ctx, id, patch, getPrivate(ctx, req.Private, privateLastModified)); err != nil {
			addAPIError(&resp.Diagnostics, "Updating webhook "+id, err)
			return
		}
	}
	r.storeHash(ctx, &cfg, resp.Private, &resp.Diagnostics)

	newState, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok {
		return
	}
	if newState == nil {
		resp.Diagnostics.AddError("Webhook disappeared during update", "GET returned 404 right after a successful write.")
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.Client.DeleteWebhook(ctx, state.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Deleting webhook "+state.ID.ValueString(), err)
	}
}

func (r *webhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
