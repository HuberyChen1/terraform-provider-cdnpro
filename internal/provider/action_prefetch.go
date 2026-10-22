package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

var (
	_ action.Action                   = (*prefetchAction)(nil)
	_ action.ActionWithConfigure      = (*prefetchAction)(nil)
	_ action.ActionWithValidateConfig = (*prefetchAction)(nil)
)

// NewPrefetchAction returns the cdnpro_prefetch action.
func NewPrefetchAction() action.Action { return &prefetchAction{} }

type prefetchAction struct {
	data *ProviderData
}

type prefetchModel struct {
	Name          types.String `tfsdk:"name"`
	FileList      types.List   `tfsdk:"file_list"`
	Regions       types.Set    `tfsdk:"regions"`
	StartTime     types.String `tfsdk:"start_time"`
	Webhook       types.String `tfsdk:"webhook"`
	PrefetchLevel types.String `tfsdk:"prefetch_level"`
}

type prefetchFileModel struct {
	URL     types.String `tfsdk:"url"`
	Headers types.List   `tfsdk:"headers"`
}

var prefetchRegions = []string{"Africa", "Antarctica", "Asia", "Europe", "North America", "Oceania", "South America", "Mainland China"}

const startTimeLayout = "2006-01-02T15:04:05Z"

func (a *prefetchAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prefetch"
}

func (a *prefetchAction) Configure(_ context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	a.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (a *prefetchAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Warms the production cache with the given URLs. Prefetches are not scoped to an " +
			"environment: they always target production. Invoke ad hoc with `terraform apply -invoke=...` or from a " +
			"`lifecycle { action_trigger {} }`. Requires Terraform 1.14 or later. The action waits for completion and " +
			"fails when the success rate is below 100%.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtMost(1000)},
			},
			"file_list": schema.ListNestedAttribute{
				Required:   true,
				Validators: []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"url": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 2048)}},
						"headers": schema.ListNestedAttribute{
							Optional:            true,
							MarkdownDescription: "Request headers that vary the cache key for this URL only.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name":  schema.StringAttribute{Required: true},
									"value": schema.StringAttribute{Required: true},
								},
							},
						},
					},
				},
			},
			"regions": schema.SetAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf(prefetchRegions...))},
				MarkdownDescription: "Continents to prefetch into: " + fmt.Sprint(prefetchRegions) + ". Default: all.",
			},
			"start_time": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{stringvalidator.RegexMatches(
					startTimePattern, "must look like 2026-10-22T08:00:00Z")},
				MarkdownDescription: "Schedule the prefetch instead of running it now. Must be within the next 24 hours at apply time.",
			},
			"webhook": schema.StringAttribute{Optional: true},
			"prefetch_level": schema.StringAttribute{
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf("Edge", "Parent")},
				MarkdownDescription: "`Parent` (default) warms the parent cache layer; `Edge` warms edge servers as well.",
			},
		},
	}
}

func (a *prefetchAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var cfg prefetchModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.StartTime.IsNull() || cfg.StartTime.IsUnknown() {
		return
	}
	if _, err := time.Parse(startTimeLayout, cfg.StartTime.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("start_time"), "Invalid start_time", err.Error())
	}
}

func (a *prefetchAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg prefetchModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !cfg.StartTime.IsNull() {
		t, err := time.Parse(startTimeLayout, cfg.StartTime.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("start_time"), "Invalid start_time", err.Error())
			return
		}
		now := time.Now().UTC()
		if !t.After(now) || t.After(now.Add(24*time.Hour)) {
			resp.Diagnostics.AddAttributeError(path.Root("start_time"), "start_time out of range",
				fmt.Sprintf("%s must be after now (%s) and within the next 24 hours.", cfg.StartTime.ValueString(), now.Format(startTimeLayout)))
			return
		}
	}
	var files []prefetchFileModel
	resp.Diagnostics.Append(cfg.FileList.ElementsAs(ctx, &files, false)...)
	apiReq := client.PrefetchRequest{
		Name:          strPtrNonEmpty(cfg.Name),
		Regions:       setToStrings(ctx, cfg.Regions, &resp.Diagnostics),
		StartTime:     strPtrNonEmpty(cfg.StartTime),
		Webhook:       strPtrNonEmpty(cfg.Webhook),
		PrefetchLevel: strPtrNonEmpty(cfg.PrefetchLevel),
	}
	for _, f := range files {
		apiReq.FileList = append(apiReq.FileList, client.PrefetchFile{
			URL:     f.URL.ValueString(),
			Headers: nameValuesOf(ctx, f.Headers, &resp.Diagnostics),
		})
	}
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := a.data.Client.CreatePrefetch(ctx, apiReq)
	if err != nil {
		addPurgeError(&resp.Diagnostics, "Submitting prefetch", err)
		return
	}
	if apiReq.StartTime != nil {
		// A scheduled prefetch stays "waiting" until its start time; do not block the apply on it.
		progress(resp, fmt.Sprintf("prefetch %s scheduled for %s", id, *apiReq.StartTime))
		return
	}
	progress(resp, fmt.Sprintf("prefetch %s submitted, waiting for completion", id))
	task, err := a.data.Client.WaitForPrefetch(ctx, id, a.data.PurgePoll)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Waiting for prefetch "+id, err)
		return
	}
	md := task.MetaData
	if md.SuccessRate != nil && *md.SuccessRate < 100 {
		resp.Diagnostics.AddError("Prefetch partially failed",
			fmt.Sprintf("Prefetch %s finished with a success rate of %.1f%%. %s", id, *md.SuccessRate, formatErrorsHint(md.Errors)))
		return
	}
	progress(resp, fmt.Sprintf("prefetch %s finished (%s)", id, md.Status))
}
