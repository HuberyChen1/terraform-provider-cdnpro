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

const privateHashSecret = "hash_secret"

var (
	_ resource.Resource                = (*secretResource)(nil)
	_ resource.ResourceWithConfigure   = (*secretResource)(nil)
	_ resource.ResourceWithImportState = (*secretResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*secretResource)(nil)
)

// NewSecretResource returns the cdnpro_secret resource.
func NewSecretResource() resource.Resource { return &secretResource{} }

type secretResource struct {
	data *ProviderData
}

type secretModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	Secret            types.String `tfsdk:"secret"`
	UsageInProduction types.List   `tfsdk:"usage_in_production"`
	UsageInStaging    types.List   `tfsdk:"usage_in_staging"`
	CreationTime      types.String `tfsdk:"creation_time"`
	LastUpdateTime    types.String `tfsdk:"last_update_time"`
}

type secretUsageModel struct {
	PropertyID types.String `tfsdk:"property_id"`
}

var (
	secretSchemaOnce   = buildSecretSchema()
	secretUsageObjType = nestedObjectType(secretSchemaOnce.Attributes["usage_in_production"])
)

func (r *secretResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = secretSchemaOnce
}

func (r *secretResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildSecretSchema() schema.Schema {
	usage := schema.ListNestedAttribute{
		Computed: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"property_id": schema.StringAttribute{Computed: true},
			},
		},
	}
	return schema.Schema{
		MarkdownDescription: "A CDN Pro secret, referenced from Edge Logic as `$SECRET(name)` or from an origin's " +
			"`aws_s3.secret_key`. The value is write-only: the API only ever returns a masked form, so the provider " +
			"stores a hash and patches the secret in place when the configured value changes. Requires Terraform 1.11 or later. " +
			"Properties reference secrets by name as plain text, so add `depends_on` on the property.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 30),
					stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-zA-Z_.-]+$`), "may only contain letters, digits, '_', '.' and '-'"),
				},
				MarkdownDescription: "Secret name, unique within the account. This is what Edge Logic references.",
			},
			"description": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtMost(250)},
			},
			"secret": schema.StringAttribute{
				Required:            true,
				WriteOnly:           true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.LengthBetween(8, 3599)},
				MarkdownDescription: "Secret value (printable ASCII, 8 to 3599 characters). Never read back into state.",
			},
			"usage_in_production": usage,
			"usage_in_staging":    usage,
			"creation_time":       schema.StringAttribute{Computed: true},
			"last_update_time": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Becomes unknown in a plan whenever the configured `secret` differs from the stored hash.",
			},
		},
	}
}

func (r *secretResource) secretChanged(ctx context.Context, cfg *secretModel, priv privateGetter) bool {
	if cfg.Secret.IsUnknown() {
		return true
	}
	return sha256Hex(cfg.Secret.ValueString()) != getPrivate(ctx, priv, privateHashSecret)
}

// ModifyPlan turns a write-only change into a visible plan by marking
// last_update_time unknown; otherwise computed values are carried over.
func (r *secretResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var cfg, plan, state secretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("creation_time"), state.CreationTime)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("usage_in_production"), state.UsageInProduction)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("usage_in_staging"), state.UsageInStaging)...)
	// Any write bumps lastUpdateTime on the server, so keep the state value only
	// when nothing is going to be sent.
	lastUpdate := state.LastUpdateTime
	if r.secretChanged(ctx, &cfg, req.Private) || !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) {
		lastUpdate = types.StringUnknown()
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("last_update_time"), lastUpdate)...)
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, cfg secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.Client.CreateSecret(ctx, client.SecretRequest{
		Name:        plan.Name.ValueString(),
		Description: strPtr(plan.Description),
		Secret:      cfg.Secret.ValueString(),
	})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Creating secret", err)
		return
	}
	setPrivate(ctx, resp.Private, privateHashSecret, sha256Hex(cfg.Secret.ValueString()), &resp.Diagnostics)
	state, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok || state == nil {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior secretModel
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

func (r *secretResource) read(ctx context.Context, id string, prior *secretModel, diags *diag.Diagnostics) (*secretModel, string, bool) {
	s, lastModified, err := r.data.Client.GetSecret(ctx, id)
	if client.IsNotFound(err) {
		return nil, "", true
	}
	if err != nil {
		addAPIError(diags, "Reading secret "+id, err)
		return nil, "", false
	}
	m := &secretModel{
		ID:                types.StringValue(id),
		Name:              types.StringValue(s.Name),
		Description:       strOrNull(s.Description),
		Secret:            types.StringNull(),
		UsageInProduction: secretUsageList(ctx, s.UsageInProduction, diags),
		UsageInStaging:    secretUsageList(ctx, s.UsageInStaging, diags),
		CreationTime:      types.StringValue(s.CreationTime),
		LastUpdateTime:    types.StringValue(s.LastUpdateTime),
	}
	if !prior.Description.IsNull() && !prior.Description.IsUnknown() && prior.Description.ValueString() == "" && s.Description == "" {
		m.Description = prior.Description
	}
	if diags.HasError() {
		return nil, "", false
	}
	return m, lastModified, true
}

func secretUsageList(ctx context.Context, usage []client.SecretUsage, diags *diag.Diagnostics) types.List {
	models := make([]secretUsageModel, 0, len(usage))
	for _, u := range usage {
		models = append(models, secretUsageModel{PropertyID: types.StringValue(u.PropertyID)})
	}
	l, d := types.ListValueFrom(ctx, secretUsageObjType, models)
	diags.Append(d...)
	return l
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, cfg secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()

	secret := ""
	if r.secretChanged(ctx, &cfg, req.Private) {
		secret = cfg.Secret.ValueString()
	}
	if secret != "" || !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) {
		patch := client.SecretPatch(plan.Name.ValueString(), strPtr(plan.Description), secret)
		if err := r.data.Client.PatchSecret(ctx, id, patch, getPrivate(ctx, req.Private, privateLastModified)); err != nil {
			addAPIError(&resp.Diagnostics, "Updating secret "+id, err)
			return
		}
	}
	setPrivate(ctx, resp.Private, privateHashSecret, sha256Hex(cfg.Secret.ValueString()), &resp.Diagnostics)

	newState, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok {
		return
	}
	if newState == nil {
		resp.Diagnostics.AddError("Secret disappeared during update", "GET returned 404 right after a successful write.")
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.Client.DeleteSecret(ctx, state.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Deleting secret "+state.ID.ValueString(), err)
	}
}

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
