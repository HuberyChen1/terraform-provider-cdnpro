package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

var (
	_ resource.Resource                = (*edgeHostnameResource)(nil)
	_ resource.ResourceWithConfigure   = (*edgeHostnameResource)(nil)
	_ resource.ResourceWithImportState = (*edgeHostnameResource)(nil)
)

// NewEdgeHostnameResource returns the cdnpro_edge_hostname resource.
func NewEdgeHostnameResource() resource.Resource { return &edgeHostnameResource{} }

type edgeHostnameResource struct {
	data *ProviderData
}

type edgeHostnameModel struct {
	EdgeHostname       types.String `tfsdk:"edge_hostname"`
	Description        types.String `tfsdk:"description"`
	ClientZones        types.List   `tfsdk:"client_zones"`
	EstimatedBandwidth types.String `tfsdk:"estimated_bandwidth"`
	DeliverByHTTPS     types.Object `tfsdk:"deliver_by_https"`
	ForceDelete        types.Bool   `tfsdk:"force_delete"`
	HasBeian           types.Bool   `tfsdk:"has_beian"`
	History            types.List   `tfsdk:"history"`
}

type clientZoneModel struct {
	Region types.String `tfsdk:"region"`
	ISP    types.String `tfsdk:"isp"`
	Weight types.Int64  `tfsdk:"weight"`
	Action types.Object `tfsdk:"action"`
}

type clientZoneActionModel struct {
	Type       types.String `tfsdk:"type"`
	By         types.Set    `tfsdk:"by"`
	To         types.List   `tfsdk:"to"`
	EnableIPv6 types.Bool   `tfsdk:"enable_ipv6"`
}

type deliverByHTTPSModel struct {
	State types.String `tfsdk:"state"`
	ALPN  types.String `tfsdk:"alpn"`
	Port  types.Int64  `tfsdk:"port"`
	TTL   types.Int64  `tfsdk:"ttl"`
}

type edgeHostnameEventModel struct {
	Operation      types.String `tfsdk:"operation"`
	Status         types.String `tfsdk:"status"`
	StatusDetails  types.String `tfsdk:"status_details"`
	SubmissionTime types.String `tfsdk:"submission_time"`
	FinishTime     types.String `tfsdk:"finish_time"`
	Time           types.String `tfsdk:"time"`
	APIName        types.String `tfsdk:"api_name"`
	APIAccountID   types.String `tfsdk:"api_account_id"`
	APIRequestID   types.String `tfsdk:"api_request_id"`
}

var (
	edgeHostnameSchemaOnce   = buildEdgeHostnameSchema()
	clientZoneObjType        = nestedObjectType(edgeHostnameSchemaOnce.Attributes["client_zones"])
	clientZoneActionObjType  = nestedObjectType(clientZoneObjType.AttrTypes["action"])
	deliverByHTTPSObjType    = nestedObjectType(edgeHostnameSchemaOnce.Attributes["deliver_by_https"])
	edgeHostnameEventObjType = nestedObjectType(edgeHostnameSchemaOnce.Attributes["history"])
)

func (r *edgeHostnameResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_edge_hostname"
}

func (r *edgeHostnameResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = edgeHostnameSchemaOnce
}

func (r *edgeHostnameResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildEdgeHostnameSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "A CDN Pro edge hostname: the CNAME target that steers client traffic by region and ISP. " +
			"Every change is provisioned asynchronously; the provider waits for the operation to finish before returning. " +
			"The hostname itself is the resource ID and cannot be changed in place.",
		Attributes: map[string]schema.Attribute{
			"edge_hostname": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The edge hostname, for example `example.edgesuffix.qtlgslb.com`. Changing it replaces the resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtMost(250)},
			},
			"client_zones": schema.ListNestedAttribute{
				Required:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				MarkdownDescription: "Traffic steering rules. More specific regions take precedence over broader ones.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"region": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Client region code (e.g. `all`, `eu`, `as.cn`) from the client regions API.",
						},
						"isp": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "ISP code, `all`, or a comma-separated list of up to 10 codes.",
						},
						"weight": schema.Int64Attribute{
							Optional: true, Computed: true,
							Default:    int64default.StaticInt64(100),
							Validators: []validator.Int64{int64validator.Between(1, 1000)},
						},
						"action": schema.SingleNestedAttribute{
							Required: true,
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Required:   true,
									Validators: []validator.String{stringvalidator.OneOf("deliver", "redirect", "reject")},
								},
								"by": schema.SetAttribute{
									ElementType:         types.StringType,
									Optional:            true,
									MarkdownDescription: "Server groups for `deliver` actions (e.g. `standard`, `premium`). The API returns a canonical ordering, hence a set.",
								},
								"to": schema.ListAttribute{
									ElementType:         types.StringType,
									Optional:            true,
									MarkdownDescription: "Hostnames or IP addresses for `redirect` actions.",
								},
								"enable_ipv6": schema.BoolAttribute{
									Optional: true, Computed: true,
									Default:             booldefault.StaticBool(true),
									MarkdownDescription: "Only consulted for `deliver` actions.",
								},
							},
						},
					},
				},
			},
			"estimated_bandwidth": schema.StringAttribute{Optional: true},
			"deliver_by_https": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"state": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("on", "off")}},
					"alpn":  schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.OneOf("h2", "h3", "h2,h3")}},
					"port":  schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(443), Validators: []validator.Int64{int64validator.Between(80, 65535)}},
					"ttl":   schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1800), Validators: []validator.Int64{int64validator.Between(60, 86400)}},
				},
			},
			"force_delete": schema.BoolAttribute{
				Optional: true, Computed: true,
				Default: booldefault.StaticBool(false),
				MarkdownDescription: "Send `Check-Usage: no` on destroy, bypassing the API's refusal to delete an edge hostname " +
					"that served traffic in the last 24 hours.",
			},
			"has_beian": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the edge hostname was created with China server groups. Read-only.",
			},
			"history": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Provisioning history, newest first.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"operation":       schema.StringAttribute{Computed: true},
						"status":          schema.StringAttribute{Computed: true},
						"status_details":  schema.StringAttribute{Computed: true},
						"submission_time": schema.StringAttribute{Computed: true},
						"finish_time":     schema.StringAttribute{Computed: true},
						"time":            schema.StringAttribute{Computed: true},
						"api_name":        schema.StringAttribute{Computed: true},
						"api_account_id":  schema.StringAttribute{Computed: true},
						"api_request_id":  schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (m *edgeHostnameModel) toRequest(ctx context.Context, diags *diag.Diagnostics) client.EdgeHostnameRequest {
	req := client.EdgeHostnameRequest{
		EdgeHostname:       m.EdgeHostname.ValueString(),
		Description:        strPtr(m.Description),
		EstimatedBandwidth: strPtrNonEmpty(m.EstimatedBandwidth),
	}
	var zones []clientZoneModel
	diags.Append(m.ClientZones.ElementsAs(ctx, &zones, false)...)
	req.ClientZones = make([]client.ClientZone, 0, len(zones))
	for _, z := range zones {
		var a clientZoneActionModel
		diags.Append(z.Action.As(ctx, &a, objectAsOptions)...)
		action := client.ClientZoneAction{Type: a.Type.ValueString()}
		switch action.Type {
		case "deliver":
			action.By = setToStrings(ctx, a.By, diags)
			action.EnableIPv6 = boolPtr(a.EnableIPv6)
		case "redirect":
			action.To = listToStrings(ctx, a.To, diags)
		}
		req.ClientZones = append(req.ClientZones, client.ClientZone{
			Region: z.Region.ValueString(),
			ISP:    z.ISP.ValueString(),
			Weight: int64Ptr(z.Weight),
			Action: action,
		})
	}
	if !m.DeliverByHTTPS.IsNull() && !m.DeliverByHTTPS.IsUnknown() {
		var d deliverByHTTPSModel
		diags.Append(m.DeliverByHTTPS.As(ctx, &d, objectAsOptions)...)
		dh := &client.DeliverByHTTPS{State: d.State.ValueString()}
		if strings.EqualFold(dh.State, "on") {
			dh.ALPN = strPtrNonEmpty(d.ALPN)
			dh.Port = int64Ptr(d.Port)
			dh.TTL = int64Ptr(d.TTL)
		}
		req.DeliverByHTTPS = dh
	}
	return req
}

func (r *edgeHostnameResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan edgeHostnameModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiReq := plan.toRequest(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	name, err := r.data.Client.CreateEdgeHostname(ctx, apiReq)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Creating edge hostname", err)
		return
	}
	view, lastModified, err := r.data.Client.WaitForEdgeHostname(ctx, name, r.data.EdgeHostnamePoll)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Waiting for edge hostname "+name+" to be provisioned", err)
		return
	}
	state := r.fromView(ctx, name, view, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *edgeHostnameResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior edgeHostnameModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := prior.EdgeHostname.ValueString()
	view, lastModified, err := r.data.Client.GetEdgeHostname(ctx, name)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Reading edge hostname "+name, err)
		return
	}
	state := r.fromView(ctx, name, view, &prior, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *edgeHostnameResource) fromView(ctx context.Context, name string, view *client.EdgeHostnameView, prior *edgeHostnameModel, diags *diag.Diagnostics) *edgeHostnameModel {
	cfg := view.Configs
	m := &edgeHostnameModel{
		EdgeHostname:       types.StringValue(name),
		Description:        strOrNull(cfg.Description),
		EstimatedBandwidth: strOrNull(cfg.EstimatedBandwidth),
		HasBeian:           types.BoolValue(cfg.HasBeian),
		ForceDelete:        prior.ForceDelete,
	}
	if m.ForceDelete.IsNull() || m.ForceDelete.IsUnknown() {
		m.ForceDelete = types.BoolValue(false)
	}
	if prior.Description.IsNull() && cfg.Description == "" {
		m.Description = types.StringNull()
	}

	zones := make([]clientZoneModel, 0, len(cfg.ClientZones))
	for _, z := range cfg.ClientZones {
		a := clientZoneActionModel{
			Type:       types.StringValue(z.Action.Type),
			By:         types.SetNull(types.StringType),
			To:         types.ListNull(types.StringType),
			EnableIPv6: types.BoolNull(),
		}
		switch z.Action.Type {
		case "deliver":
			a.By = stringSet(ctx, z.Action.By, diags)
			if z.Action.EnableIPv6 != nil {
				a.EnableIPv6 = types.BoolValue(*z.Action.EnableIPv6)
			} else {
				a.EnableIPv6 = types.BoolValue(true)
			}
		case "redirect":
			a.To = stringList(ctx, z.Action.To, diags)
		}
		if a.EnableIPv6.IsNull() {
			// The attribute is Optional+Computed with a default, so it must always be known.
			a.EnableIPv6 = types.BoolValue(true)
		}
		action, d := types.ObjectValueFrom(ctx, clientZoneActionObjType.AttrTypes, a)
		diags.Append(d...)
		weight := int64(100)
		if z.Weight != nil {
			weight = *z.Weight
		}
		zones = append(zones, clientZoneModel{
			Region: types.StringValue(z.Region),
			ISP:    types.StringValue(z.ISP),
			Weight: types.Int64Value(weight),
			Action: action,
		})
	}
	zl, d := types.ListValueFrom(ctx, clientZoneObjType, zones)
	diags.Append(d...)
	m.ClientZones = zl

	m.DeliverByHTTPS = types.ObjectNull(deliverByHTTPSObjType.AttrTypes)
	if cfg.DeliverByHTTPS != nil {
		dm := deliverByHTTPSModel{
			State: types.StringValue(cfg.DeliverByHTTPS.State),
			ALPN:  strFromPtrNonEmpty(cfg.DeliverByHTTPS.ALPN),
			Port:  types.Int64Value(443),
			TTL:   types.Int64Value(1800),
		}
		if cfg.DeliverByHTTPS.Port != nil {
			dm.Port = types.Int64Value(*cfg.DeliverByHTTPS.Port)
		}
		if cfg.DeliverByHTTPS.TTL != nil {
			dm.TTL = types.Int64Value(*cfg.DeliverByHTTPS.TTL)
		}
		// When state is off the API stores only the state; keep what the user wrote.
		if strings.EqualFold(dm.State.ValueString(), "off") && !prior.DeliverByHTTPS.IsNull() && !prior.DeliverByHTTPS.IsUnknown() {
			var pd deliverByHTTPSModel
			diags.Append(prior.DeliverByHTTPS.As(ctx, &pd, objectAsOptions)...)
			if !pd.ALPN.IsUnknown() {
				dm.ALPN = pd.ALPN
			}
			if !pd.Port.IsNull() && !pd.Port.IsUnknown() {
				dm.Port = pd.Port
			}
			if !pd.TTL.IsNull() && !pd.TTL.IsUnknown() {
				dm.TTL = pd.TTL
			}
		}
		obj, d := types.ObjectValueFrom(ctx, deliverByHTTPSObjType.AttrTypes, dm)
		diags.Append(d...)
		m.DeliverByHTTPS = obj
	}

	events := make([]edgeHostnameEventModel, 0, len(view.History))
	for _, e := range view.History {
		events = append(events, edgeHostnameEventModel{
			Operation:      types.StringValue(e.Operation),
			Status:         types.StringValue(e.Status),
			StatusDetails:  types.StringValue(e.StatusDetails),
			SubmissionTime: types.StringValue(e.SubmissionTime),
			FinishTime:     types.StringValue(e.FinishTime),
			Time:           types.StringValue(e.Time),
			APIName:        types.StringValue(e.APIName),
			APIAccountID:   types.StringValue(e.APIAccountID),
			APIRequestID:   types.StringValue(e.APIRequestID),
		})
	}
	hl, d := types.ListValueFrom(ctx, edgeHostnameEventObjType, events)
	diags.Append(d...)
	m.History = hl
	return m
}

func (r *edgeHostnameResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state edgeHostnameModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := state.EdgeHostname.ValueString()
	apiReq := plan.toRequest(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	configChanged := !plan.ClientZones.Equal(state.ClientZones) || !plan.DeliverByHTTPS.Equal(state.DeliverByHTTPS) ||
		!plan.Description.Equal(state.Description) || !plan.EstimatedBandwidth.Equal(state.EstimatedBandwidth)

	var view *client.EdgeHostnameView
	var lastModified string
	if configChanged {
		lastModified = getPrivate(ctx, req.Private, privateLastModified)
		if err := r.data.Client.PutEdgeHostname(ctx, name, apiReq, lastModified); err != nil {
			addAPIError(&resp.Diagnostics, "Updating edge hostname "+name, err)
			return
		}
		var err error
		view, lastModified, err = r.data.Client.WaitForEdgeHostname(ctx, name, r.data.EdgeHostnamePoll)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Waiting for edge hostname "+name+" update", err)
			return
		}
	} else {
		var err error
		view, lastModified, err = r.data.Client.GetEdgeHostname(ctx, name)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Reading edge hostname "+name, err)
			return
		}
	}
	newState := r.fromView(ctx, name, view, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *edgeHostnameResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state edgeHostnameModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := state.EdgeHostname.ValueString()
	force := state.ForceDelete.ValueBool()

	err := r.data.Client.DeleteEdgeHostname(ctx, name, force)
	if client.HasCode(err, client.CodeEdgeHostnameBeingDeploy) {
		// A previous operation is still in flight: wait for it, then retry once.
		if _, _, waitErr := r.data.Client.WaitForEdgeHostname(ctx, name, r.data.EdgeHostnamePoll); waitErr != nil && !client.IsNotFound(waitErr) {
			addAPIError(&resp.Diagnostics, "Waiting for in-flight operation on edge hostname "+name, waitErr)
			return
		}
		err = r.data.Client.DeleteEdgeHostname(ctx, name, force)
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, fmt.Sprintf("Deleting edge hostname %s", name), err)
		return
	}
	if err := r.data.Client.WaitForEdgeHostnameDeletion(ctx, name, r.data.EdgeHostnamePoll); err != nil {
		addAPIError(&resp.Diagnostics, "Waiting for edge hostname "+name+" deletion", err)
	}
}

func (r *edgeHostnameResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("edge_hostname"), req, resp)
}
