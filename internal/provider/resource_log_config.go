package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

var (
	_ resource.Resource                = (*logConfigResource)(nil)
	_ resource.ResourceWithConfigure   = (*logConfigResource)(nil)
	_ resource.ResourceWithImportState = (*logConfigResource)(nil)
)

// NewLogConfigResource returns the cdnpro_log_config resource.
func NewLogConfigResource() resource.Resource { return &logConfigResource{} }

type logConfigResource struct {
	data *ProviderData
}

type logConfigModel struct {
	ID                         types.String `tfsdk:"id"`
	Description                types.String `tfsdk:"description"`
	Hostnames                  types.List   `tfsdk:"hostnames"`
	LogDownloadFormat          types.String `tfsdk:"log_download_format"`
	LogDownloadStorageDays     types.Int64  `tfsdk:"log_download_storage_days"`
	LogDownloadFileSpanMinutes types.Int64  `tfsdk:"log_download_file_span_minutes"`
	CreationTime               types.String `tfsdk:"creation_time"`
	LastUpdateTime             types.String `tfsdk:"last_update_time"`
}

func (r *logConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_log_config"
}

func (r *logConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A CDN Pro access log download configuration (`/cdn/report/logConfigs`). " +
			"The API's integer ID is carried as a string.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"description": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtMost(256)},
			},
			"hostnames": schema.ListAttribute{
				ElementType:         types.StringType,
				Required:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				MarkdownDescription: "Hostnames whose logs are collected. Wildcards and a lone `*` are accepted.",
			},
			"log_download_format": schema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString(client.DefaultLogDownloadFormat),
				MarkdownDescription: "Log line template using `%name` / `%{name}` variables. " +
					"Defaults to the API's Apache-style combined format.",
			},
			"log_download_storage_days": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:    int64default.StaticInt64(client.DefaultLogDownloadStorageDays),
				Validators: []validator.Int64{int64validator.Between(1, 30)},
			},
			"log_download_file_span_minutes": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:    int64default.StaticInt64(client.DefaultLogDownloadFileSpanMinutes),
				Validators: []validator.Int64{int64validator.OneOf(5, 15, 30, 60, 120, 240, 480, 1440)},
			},
			"creation_time":    schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"last_update_time": schema.StringAttribute{Computed: true},
		},
	}
}

func (r *logConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (m *logConfigModel) toRequest(ctx context.Context, diags *diag.Diagnostics) client.LogConfigRequest {
	return client.LogConfigRequest{
		Description:                strPtr(m.Description),
		Hostnames:                  listToStrings(ctx, m.Hostnames, diags),
		LogDownloadFormat:          strPtr(m.LogDownloadFormat),
		LogDownloadStorageDays:     int64Ptr(m.LogDownloadStorageDays),
		LogDownloadFileSpanMinutes: int64Ptr(m.LogDownloadFileSpanMinutes),
	}
}

func (r *logConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan logConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiReq := plan.toRequest(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.Client.CreateLogConfig(ctx, apiReq)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Creating log config", err)
		return
	}
	state, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok || state == nil {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *logConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior logConfigModel
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

func (r *logConfigResource) read(ctx context.Context, id string, prior *logConfigModel, diags *diag.Diagnostics) (*logConfigModel, string, bool) {
	lc, lastModified, err := r.data.Client.GetLogConfig(ctx, id)
	if client.IsNotFound(err) {
		return nil, "", true
	}
	if err != nil {
		addAPIError(diags, "Reading log config "+id, err)
		return nil, "", false
	}
	cfg := lc.Configs
	m := &logConfigModel{
		ID:                         types.StringValue(lc.ID.String()),
		Description:                strOrNull(cfg.Description),
		Hostnames:                  stringListOrEmpty(ctx, cfg.Hostnames, diags),
		LogDownloadFormat:          types.StringValue(cfg.LogDownloadFormat),
		LogDownloadStorageDays:     types.Int64Value(cfg.LogDownloadStorageDays),
		LogDownloadFileSpanMinutes: types.Int64Value(cfg.LogDownloadFileSpanMinutes),
		CreationTime:               types.StringValue(lc.CreationTime),
		LastUpdateTime:             types.StringValue(lc.LastUpdateTime),
	}
	if cfg.LogDownloadFormat == "" {
		m.LogDownloadFormat = types.StringValue(client.DefaultLogDownloadFormat)
	}
	if cfg.LogDownloadStorageDays == 0 {
		m.LogDownloadStorageDays = types.Int64Value(client.DefaultLogDownloadStorageDays)
	}
	if cfg.LogDownloadFileSpanMinutes == 0 {
		m.LogDownloadFileSpanMinutes = types.Int64Value(client.DefaultLogDownloadFileSpanMinutes)
	}
	if diags.HasError() {
		return nil, "", false
	}
	return m, lastModified, true
}

func (r *logConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state logConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	apiReq := plan.toRequest(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.Client.PatchLogConfig(ctx, id, apiReq, getPrivate(ctx, req.Private, privateLastModified)); err != nil {
		addAPIError(&resp.Diagnostics, "Updating log config "+id, err)
		return
	}
	newState, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok {
		return
	}
	if newState == nil {
		resp.Diagnostics.AddError("Log config disappeared during update", "GET returned 404 right after a successful write.")
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *logConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state logConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.Client.DeleteLogConfig(ctx, state.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Deleting log config "+state.ID.ValueString(), err)
	}
}

func (r *logConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
