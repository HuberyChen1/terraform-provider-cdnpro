package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

var (
	_ action.Action                   = (*purgeAction)(nil)
	_ action.ActionWithConfigure      = (*purgeAction)(nil)
	_ action.ActionWithValidateConfig = (*purgeAction)(nil)
)

// NewPurgeAction returns the cdnpro_purge action.
func NewPurgeAction() action.Action { return &purgeAction{} }

type purgeAction struct {
	data *ProviderData
}

type purgeModel struct {
	Name          types.String `tfsdk:"name"`
	Target        types.String `tfsdk:"target"`
	Action        types.String `tfsdk:"action"`
	FileURLs      types.List   `tfsdk:"file_urls"`
	FileHeaders   types.List   `tfsdk:"file_headers"`
	DirURLs       types.List   `tfsdk:"dir_urls"`
	RegexPatterns types.List   `tfsdk:"regex_patterns"`
	Webhook       types.String `tfsdk:"webhook"`
}

func (a *purgeAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_purge"
}

func (a *purgeAction) Configure(_ context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	a.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (a *purgeAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Purges cached content from the CDN. Invoke it ad hoc with " +
			"`terraform apply -invoke=action.cdnpro_purge.NAME`, or attach it to a resource's `lifecycle { action_trigger {} }` " +
			"so a deployment automatically purges affected paths. Requires Terraform 1.14 or later. " +
			"The action waits for the purge to finish and fails when the API reports a success rate below 100%.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtMost(128)},
				MarkdownDescription: "Label for the purge task.",
			},
			"target": schema.StringAttribute{
				Required:   true,
				Validators: []validator.String{stringvalidator.OneOf(client.TargetStaging, client.TargetProduction)},
			},
			"action": schema.StringAttribute{
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf("invalidate", "delete")},
				MarkdownDescription: "`invalidate` (default) marks content stale; `delete` removes it. A directory URL or pattern ending in `**` makes the purge recursive.",
			},
			"file_urls": schema.ListAttribute{ElementType: types.StringType, Optional: true},
			"file_headers": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Request headers that are part of the cache key for every entry in `file_urls`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{Required: true},
						"value": schema.StringAttribute{Required: true},
					},
				},
			},
			"dir_urls":       schema.ListAttribute{ElementType: types.StringType, Optional: true},
			"regex_patterns": schema.ListAttribute{ElementType: types.StringType, Optional: true, MarkdownDescription: "At most two patterns."},
			"webhook": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Webhook ID (`cdnpro_webhook.x.id`) notified when the purge completes.",
			},
		},
	}
}

func (a *purgeAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var cfg purgeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	known := func(l types.List) bool { return !l.IsNull() && !l.IsUnknown() }
	if !known(cfg.FileURLs) && !known(cfg.DirURLs) && !known(cfg.RegexPatterns) {
		if cfg.FileURLs.IsUnknown() || cfg.DirURLs.IsUnknown() || cfg.RegexPatterns.IsUnknown() {
			return
		}
		resp.Diagnostics.AddError("Nothing to purge",
			"Set at least one of file_urls, dir_urls or regex_patterns; the API would accept an empty purge but it does nothing.")
		return
	}
	if len(cfg.FileURLs.Elements())+len(cfg.DirURLs.Elements())+len(cfg.RegexPatterns.Elements()) == 0 {
		resp.Diagnostics.AddError("Nothing to purge", "file_urls, dir_urls and regex_patterns are all empty.")
	}
	if known(cfg.RegexPatterns) && len(cfg.RegexPatterns.Elements()) > 2 {
		resp.Diagnostics.AddAttributeError(path.Root("regex_patterns"), "Too many patterns", "The API accepts at most two regex patterns per purge.")
	}
}

func nameValuesOf(ctx context.Context, l types.List, diags *diag.Diagnostics) []client.NameValue {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	var models []nameValueModel
	diags.Append(l.ElementsAs(ctx, &models, false)...)
	out := make([]client.NameValue, 0, len(models))
	for _, m := range models {
		out = append(out, client.NameValue{Name: m.Name.ValueString(), Value: m.Value.ValueString()})
	}
	return out
}

func (a *purgeAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg purgeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiReq := client.PurgeRequest{
		Name:          strPtrNonEmpty(cfg.Name),
		Target:        cfg.Target.ValueString(),
		Action:        strPtrNonEmpty(cfg.Action),
		FileURLs:      listToStrings(ctx, cfg.FileURLs, &resp.Diagnostics),
		FileHeaders:   nameValuesOf(ctx, cfg.FileHeaders, &resp.Diagnostics),
		DirURLs:       listToStrings(ctx, cfg.DirURLs, &resp.Diagnostics),
		RegexPatterns: listToStrings(ctx, cfg.RegexPatterns, &resp.Diagnostics),
		Webhook:       strPtrNonEmpty(cfg.Webhook),
	}
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := a.data.Client.CreatePurge(ctx, apiReq)
	if err != nil {
		addPurgeError(&resp.Diagnostics, "Submitting purge", err)
		return
	}
	progress(resp, fmt.Sprintf("purge %s submitted to %s, waiting for completion", id, apiReq.Target))
	task, err := a.data.Client.WaitForPurge(ctx, id, a.data.PurgePoll)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Waiting for purge "+id, err)
		return
	}
	if task.SuccessRate != nil && *task.SuccessRate < 100 {
		resp.Diagnostics.AddError("Purge partially failed",
			fmt.Sprintf("Purge %s finished with a success rate of %.1f%%. %s", id, *task.SuccessRate, formatErrorsHint(task.Errors)))
		return
	}
	progress(resp, fmt.Sprintf("purge %s finished (%s)", id, task.Status))
}

func progress(resp *action.InvokeResponse, msg string) {
	if resp.SendProgress != nil {
		resp.SendProgress(action.InvokeProgressEvent{Message: msg})
	}
}

func formatErrorsHint(errs []client.TaskError) string {
	if len(errs) == 0 {
		return ""
	}
	out := "Errors reported:"
	for _, e := range errs {
		out += fmt.Sprintf(" [%s] %s", e.ErrorCode, e.ErrorMessage)
	}
	return out
}

// addPurgeError adds a hint for the URL/host errors whose raw message does not
// say which input is wrong.
func addPurgeError(diags *diag.Diagnostics, summary string, err error) {
	if client.HasCode(err, "InvalidPurgeUrl") || client.HasCode(err, "NoSuchDomain") {
		diags.AddError(summary,
			err.Error()+". Check that each URL's hostname belongs to a property deployed to the chosen target, "+
				"and that `target` is the environment you meant.")
		return
	}
	addAPIError(diags, summary, err)
}
