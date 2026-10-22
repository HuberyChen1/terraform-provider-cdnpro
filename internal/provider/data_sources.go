package provider

import (
	"context"
	"regexp"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

var startTimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

// ---- cdnpro_hostname ---------------------------------------------------------

var _ datasource.DataSourceWithConfigure = (*hostnameDataSource)(nil)

// NewHostnameDataSource returns the cdnpro_hostname data source.
func NewHostnameDataSource() datasource.DataSource { return &hostnameDataSource{} }

type hostnameDataSource struct {
	data *ProviderData
}

type hostnameModel struct {
	Hostname             types.String `tfsdk:"hostname"`
	Found                types.Bool   `tfsdk:"found"`
	PropertyInProduction types.Object `tfsdk:"property_in_production"`
	PropertyInStaging    types.Object `tfsdk:"property_in_staging"`
}

type hostnameDeploymentModel struct {
	PropertyID    types.String `tfsdk:"property_id"`
	Version       types.Int64  `tfsdk:"version"`
	CertificateID types.String `tfsdk:"certificate_id"`
}

var (
	hostnameSchemaOnce        = buildHostnameSchema()
	hostnameDeploymentObjType = nestedObjectType(hostnameSchemaOnce.Attributes["property_in_production"])
)

func (d *hostnameDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_hostname"
}

func (d *hostnameDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = hostnameSchemaOnce
}

func (d *hostnameDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildHostnameSchema() schema.Schema {
	deployment := schema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]schema.Attribute{
			"property_id":    schema.StringAttribute{Computed: true},
			"version":        schema.Int64Attribute{Computed: true},
			"certificate_id": schema.StringAttribute{Computed: true},
		},
	}
	return schema.Schema{
		MarkdownDescription: "Reports which property version currently serves a hostname in production and staging. " +
			"The API returns an empty result rather than an error for an unknown hostname; `found` is false in that case " +
			"and a warning is emitted.",
		Attributes: map[string]schema.Attribute{
			"hostname":               schema.StringAttribute{Required: true},
			"found":                  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the hostname is deployed to at least one environment."},
			"property_in_production": deployment,
			"property_in_staging":    deployment,
		},
	}
}

func (d *hostnameDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg hostnameModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	info, err := d.data.Client.GetHostname(ctx, cfg.Hostname.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Looking up hostname "+cfg.Hostname.ValueString(), err)
		return
	}
	cfg.Found = types.BoolValue(info.Found())
	cfg.PropertyInProduction = hostnameDeployment(ctx, info.PropertyInProduction, &resp.Diagnostics)
	cfg.PropertyInStaging = hostnameDeployment(ctx, info.PropertyInStaging, &resp.Diagnostics)
	if !info.Found() {
		resp.Diagnostics.AddWarning("Hostname not deployed",
			cfg.Hostname.ValueString()+" is not served by any deployed property in production or staging.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

func hostnameDeployment(ctx context.Context, dep *client.HostnameDeployment, diags *diag.Diagnostics) types.Object {
	if dep == nil {
		return types.ObjectNull(hostnameDeploymentObjType.AttrTypes)
	}
	obj, d := types.ObjectValueFrom(ctx, hostnameDeploymentObjType.AttrTypes, hostnameDeploymentModel{
		PropertyID:    types.StringValue(dep.PropertyID),
		Version:       types.Int64Value(dep.Version),
		CertificateID: strOrNull(dep.CertificateID),
	})
	diags.Append(d...)
	return obj
}

// ---- cdnpro_shields ----------------------------------------------------------

var _ datasource.DataSourceWithConfigure = (*shieldsDataSource)(nil)

// NewShieldsDataSource returns the cdnpro_shields data source.
func NewShieldsDataSource() datasource.DataSource { return &shieldsDataSource{} }

type shieldsDataSource struct {
	data *ProviderData
}

type shieldsModel struct {
	Search           types.String `tfsdk:"search"`
	UsedInProperties types.Bool   `tfsdk:"used_in_properties"`
	Shields          types.List   `tfsdk:"shields"`
}

type shieldModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Region           types.String `tfsdk:"region"`
	IPv4             types.List   `tfsdk:"ipv4"`
	IPv6             types.List   `tfsdk:"ipv6"`
	LastUpdated      types.String `tfsdk:"last_updated"`
	EffectiveDate    types.String `tfsdk:"effective_date"`
	UsedInProperties types.Bool   `tfsdk:"used_in_properties"`
}

var (
	shieldsSchemaOnce = buildShieldsSchema()
	shieldObjType     = nestedObjectType(shieldsSchemaOnce.Attributes["shields"])
)

func (d *shieldsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_shields"
}

func (d *shieldsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = shieldsSchemaOnce
}

func (d *shieldsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildShieldsSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "Lists the platform's origin shields. Use a shield's `id` in `cdnpro_property.origins[].shield`; " +
			"its `ipv4`/`ipv6` ranges are what the origin must allow-list. Results are sorted by `id`.",
		Attributes: map[string]schema.Attribute{
			"search": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Case-sensitive substring match on id, name, region or IP range.",
			},
			"used_in_properties": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Only shields that are (true) or are not (false) referenced by deployed properties.",
			},
			"shields": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                 schema.StringAttribute{Computed: true},
						"name":               schema.StringAttribute{Computed: true},
						"region":             schema.StringAttribute{Computed: true},
						"ipv4":               schema.ListAttribute{ElementType: types.StringType, Computed: true},
						"ipv6":               schema.ListAttribute{ElementType: types.StringType, Computed: true},
						"last_updated":       schema.StringAttribute{Computed: true},
						"effective_date":     schema.StringAttribute{Computed: true},
						"used_in_properties": schema.BoolAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *shieldsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg shieldsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	shields, err := d.data.Client.ListShields(ctx, client.ShieldFilter{
		Search:           cfg.Search.ValueString(),
		UsedInProperties: boolPtr(cfg.UsedInProperties),
	})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Listing shields", err)
		return
	}
	sort.Slice(shields, func(i, j int) bool { return shields[i].ID < shields[j].ID })
	models := make([]shieldModel, 0, len(shields))
	for _, s := range shields {
		models = append(models, shieldModel{
			ID:               types.StringValue(s.ID),
			Name:             types.StringValue(s.Name),
			Region:           types.StringValue(s.Region),
			IPv4:             stringListOrEmpty(ctx, s.IPv4, &resp.Diagnostics),
			IPv6:             stringListOrEmpty(ctx, s.IPv6, &resp.Diagnostics),
			LastUpdated:      types.StringValue(s.LastUpdated),
			EffectiveDate:    types.StringValue(s.EffectiveDate),
			UsedInProperties: types.BoolValue(s.UsedInProperties),
		})
	}
	l, diags := types.ListValueFrom(ctx, shieldObjType, models)
	resp.Diagnostics.Append(diags...)
	cfg.Shields = l
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ---- cdnpro_isps / cdnpro_client_regions ------------------------------------

type codeNameModel struct {
	Code types.String `tfsdk:"code"`
	Name types.String `tfsdk:"name"`
}

var codeNameObjType = types.ObjectType{AttrTypes: map[string]attr.Type{"code": types.StringType, "name": types.StringType}}

func codeNameList(ctx context.Context, items []client.CodeName, diags *diag.Diagnostics) types.List {
	sort.Slice(items, func(i, j int) bool { return items[i].Code < items[j].Code })
	models := make([]codeNameModel, 0, len(items))
	for _, it := range items {
		models = append(models, codeNameModel{Code: types.StringValue(it.Code), Name: types.StringValue(it.Name)})
	}
	l, d := types.ListValueFrom(ctx, codeNameObjType, models)
	diags.Append(d...)
	return l
}

func codeNameListAttribute(description string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: description,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"code": schema.StringAttribute{Computed: true},
				"name": schema.StringAttribute{Computed: true},
			},
		},
	}
}

var _ datasource.DataSourceWithConfigure = (*ispsDataSource)(nil)

// NewISPsDataSource returns the cdnpro_isps data source.
func NewISPsDataSource() datasource.DataSource { return &ispsDataSource{} }

type ispsDataSource struct {
	data *ProviderData
}

type ispsModel struct {
	Search     types.String `tfsdk:"search"`
	RegionCode types.String `tfsdk:"region_code"`
	ISPs       types.List   `tfsdk:"isps"`
}

func (d *ispsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_isps"
}

func (d *ispsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists ISP codes for `cdnpro_edge_hostname.client_zones[].isp`. Results are sorted by `code`.",
		Attributes: map[string]schema.Attribute{
			"search":      schema.StringAttribute{Optional: true, MarkdownDescription: "Substring match on code or name."},
			"region_code": schema.StringAttribute{Optional: true, MarkdownDescription: "Only ISPs operating in this client region (a `cdnpro_client_regions` code)."},
			"isps":        codeNameListAttribute("Matching ISPs."),
		},
	}
}

func (d *ispsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *ispsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ispsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.data.Client.ListISPs(ctx, cfg.Search.ValueString(), cfg.RegionCode.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Listing ISPs", err)
		return
	}
	cfg.ISPs = codeNameList(ctx, items, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var _ datasource.DataSourceWithConfigure = (*clientRegionsDataSource)(nil)

// NewClientRegionsDataSource returns the cdnpro_client_regions data source.
func NewClientRegionsDataSource() datasource.DataSource { return &clientRegionsDataSource{} }

type clientRegionsDataSource struct {
	data *ProviderData
}

type clientRegionsModel struct {
	Search  types.String `tfsdk:"search"`
	ISPCode types.String `tfsdk:"isp_code"`
	Regions types.List   `tfsdk:"regions"`
}

func (d *clientRegionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_client_regions"
}

func (d *clientRegionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists client region codes for `cdnpro_edge_hostname.client_zones[].region`. Results are sorted by `code`.",
		Attributes: map[string]schema.Attribute{
			"search":   schema.StringAttribute{Optional: true, MarkdownDescription: "Substring match on code or name."},
			"isp_code": schema.StringAttribute{Optional: true, MarkdownDescription: "Only regions where this ISP (a `cdnpro_isps` code) operates."},
			"regions":  codeNameListAttribute("Matching client regions."),
		},
	}
}

func (d *clientRegionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *clientRegionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg clientRegionsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.data.Client.ListClientRegions(ctx, cfg.Search.ValueString(), cfg.ISPCode.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Listing client regions", err)
		return
	}
	cfg.Regions = codeNameList(ctx, items, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
