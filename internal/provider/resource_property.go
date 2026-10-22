package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

var objectAsOptions = basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}

var (
	_ resource.Resource                = (*propertyResource)(nil)
	_ resource.ResourceWithConfigure   = (*propertyResource)(nil)
	_ resource.ResourceWithImportState = (*propertyResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*propertyResource)(nil)
)

// NewPropertyResource returns the cdnpro_property resource.
func NewPropertyResource() resource.Resource { return &propertyResource{} }

type propertyResource struct {
	data *ProviderData
}

func (r *propertyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_property"
}

func (r *propertyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = propertySchemaOnce
}

func (r *propertyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildPropertySchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "A CDN Pro property together with its configuration. The API keeps configuration in " +
			"immutable numbered versions; this resource hides that. Changing a configuration attribute patches the " +
			"latest version in place when it has never been deployed, and otherwise creates a new version. " +
			"Creating a version never deploys it: use `cdnpro_deployment` for that.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Property ID.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Property name. Must be unique within the account.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-form description.",
			},
			"hostnames": schema.ListAttribute{
				ElementType:         types.StringType,
				Required:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				MarkdownDescription: "Hostnames served by this property.",
			},
			"origins": originsAttribute(),
			"edge_logic": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Edge Logic (NGINX-style DSL). Passed through unparsed; syntax errors surface at apply time with the API's error code.",
			},
			"syntax_version": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "Edge Logic syntax version. Currently always `1`.",
			},
			"has_beian": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether the hostnames hold an ICP Beian licence.",
			},
			"beian_content_type": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Beian content type code. Required when `has_beian` is true.",
			},
			"allow_protocol_downgrade": schema.BoolAttribute{Optional: true},
			"enable_ipv6_origin":       schema.BoolAttribute{Optional: true},
			"follow_client_ip_version": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("off"),
				Validators: []validator.String{stringvalidator.OneOf("auto", "strict", "off")},
			},
			"cache_key_hostname": schema.StringAttribute{Optional: true},
			"cache_key_uri": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("preRewrite"),
				Validators: []validator.String{stringvalidator.OneOf("preRewrite", "postRewrite")},
			},
			"scheme_in_cache_key": schema.BoolAttribute{Optional: true},
			"redirect_http_to_https": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("false"),
				Validators: []validator.String{stringvalidator.OneOf("true", "false", "302", "307", "308")},
				MarkdownDescription: "`true`/`false`, or one of `302`, `307`, `308` to redirect with that status code. " +
					"HCL booleans are accepted and converted to strings.",
			},
			"disable_http2": schema.BoolAttribute{Optional: true},
			"enable_http3":  schema.BoolAttribute{Optional: true},
			"extra_service_ports": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Extra HTTP/HTTPS service ports. Values must be from the account's allowed list.",
				Attributes: map[string]schema.Attribute{
					"http":  schema.ListAttribute{ElementType: types.Int64Type, Optional: true},
					"https": schema.ListAttribute{ElementType: types.Int64Type, Optional: true},
				},
			},
			"tls_certificate_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Certificate served to clients over HTTPS (`cdnpro_certificate.id`).",
			},
			"tls_certificate_id1": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Second certificate of the other key type (RSA vs EC). Requires `tls_certificate_id`.",
			},
			"tls_min_version": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("1"),
				Validators: []validator.String{stringvalidator.OneOf("1", "1.1", "1.2", "1.3")},
			},
			"tls_max_version": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("1.3"),
				Validators: []validator.String{stringvalidator.OneOf("1.1", "1.2", "1.3")},
			},
			"tls_ciphers":          schema.StringAttribute{Optional: true},
			"enable_ocsp_stapling": schema.BoolAttribute{Optional: true},
			"tls_0rtt":             schema.BoolAttribute{Optional: true},
			"tls_session_timeout": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:    int64default.StaticInt64(1800),
				Validators: []validator.Int64{int64validator.Between(300, 86400)},
			},
			"disable_cert_automation": schema.BoolAttribute{Optional: true},
			"access_control_rules":    accessControlRulesAttribute(),
			"real_time_log":           realTimeLogAttribute(),
			"video_seek": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"start_parameter": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 31)}},
					"end_parameter":   schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthAtMost(31)}},
				},
			},
			"latest_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of the newest configuration version. Reference this from `cdnpro_deployment`.",
			},
			"version_in_production": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Version currently deployed to production, if any.",
			},
			"version_in_staging": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Version currently deployed to staging, if any.",
			},
		},
	}
}

func originsAttribute() schema.ListNestedAttribute {
	peerFailureDefault, _ := types.ObjectValue(
		map[string]attr.Type{"disabled": types.BoolType, "failure_threshold": types.Int64Type, "timeout": types.Int64Type},
		map[string]attr.Value{"disabled": types.BoolValue(false), "failure_threshold": types.Int64Value(6), "timeout": types.Int64Value(1)},
	)
	return schema.ListNestedAttribute{
		Required:   true,
		Validators: []validator.List{listvalidator.SizeAtLeast(1)},
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"name": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "Origin name, unique within the property.",
				},
				"servers": schema.ListAttribute{
					ElementType: types.StringType,
					Required:    true,
					Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
					MarkdownDescription: "Origin servers as `host`, `host:httpPort` or `host:httpPort,httpsPort`, " +
						"optionally followed by ` backup` or ` weight=N`.",
				},
				"supported_protocol": schema.StringAttribute{
					Optional: true, Computed: true,
					Default:    stringdefault.StaticString("both"),
					Validators: []validator.String{stringvalidator.OneOf("http", "https", "both")},
				},
				"verify_origin": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				"host_header":   schema.StringAttribute{Optional: true},
				"direct_connection": schema.StringAttribute{
					Optional: true, Computed: true,
					Default:    stringdefault.StaticString("auto"),
					Validators: []validator.String{stringvalidator.OneOf("noDirect", "auto", "alwaysDirect")},
				},
				"shield": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "Origin shield ID from the shields API.",
				},
				"keep_alive_timeout": schema.Int64Attribute{
					Optional: true, Computed: true,
					Default:    int64default.StaticInt64(60),
					Validators: []validator.Int64{int64validator.Between(5, 600)},
				},
				"peer_failure_timeout": schema.SingleNestedAttribute{
					Optional: true, Computed: true,
					Default:             objectdefault.StaticValue(peerFailureDefault),
					MarkdownDescription: "Peer failure detection. Set `disabled = true` to send the API's literal `\"off\"`.",
					Attributes: map[string]schema.Attribute{
						"disabled":          schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
						"failure_threshold": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(6), Validators: []validator.Int64{int64validator.Between(1, 999)}},
						"timeout":           schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), Validators: []validator.Int64{int64validator.Between(1, 1200)}},
					},
				},
				"peer_selection_algorithm": schema.StringAttribute{
					Optional: true, Computed: true,
					Default:    stringdefault.StaticString("round_robin"),
					Validators: []validator.String{stringvalidator.OneOf("round_robin", "consistent_hash", "sorted_list")},
				},
				"max_connections": schema.Int64Attribute{
					Optional: true, Computed: true,
					Default:    int64default.StaticInt64(0),
					Validators: []validator.Int64{int64validator.AtLeast(0)},
				},
				"tls_certificate_id": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "Client certificate presented to the origin (`cdnpro_certificate.id`).",
				},
				"authentication": schema.SingleNestedAttribute{
					Optional: true,
					Attributes: map[string]schema.Attribute{
						"method_name": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("awsS3")}},
						"aws_s3": schema.SingleNestedAttribute{
							Optional: true,
							Attributes: map[string]schema.Attribute{
								"region":     schema.StringAttribute{Required: true},
								"access_key": schema.StringAttribute{Required: true},
								"secret_key": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "Must be a `$SECRET(name)` reference to a CDN Pro secret, never a literal key.",
								},
							},
						},
					},
				},
			},
		},
	}
}

func accessControlRulesAttribute() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Optional: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthAtMost(60)}},
				"conditions": schema.SingleNestedAttribute{
					Required: true,
					Attributes: map[string]schema.Attribute{
						"scheme":          schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.OneOf("http", "https")}},
						"hostname":        schema.StringAttribute{Optional: true},
						"uri":             schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthAtMost(500)}},
						"server_regions":  schema.ListAttribute{ElementType: types.StringType, Optional: true},
						"client_regions":  schema.ListAttribute{ElementType: types.StringType, Optional: true},
						"client_ip_range": schema.ListAttribute{ElementType: types.StringType, Optional: true, Validators: []validator.List{listvalidator.SizeBetween(2, 2)}},
					},
				},
				"action": schema.SingleNestedAttribute{
					Required: true,
					Attributes: map[string]schema.Attribute{
						"status_code": schema.StringAttribute{Required: true},
						"message":     schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthAtMost(200)}},
					},
				},
			},
		},
	}
}

func realTimeLogAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Attributes: map[string]schema.Attribute{
			"log_url": schema.StringAttribute{Required: true},
			"sample_rate": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:    int64default.StaticInt64(10),
				Validators: []validator.Int64{int64validator.Between(1, 65536)},
			},
			"escape": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("none"),
				Validators: []validator.String{stringvalidator.OneOf("json", "none", "compactJson")},
			},
			"format": schema.StringAttribute{Required: true},
			"headers": schema.ListNestedAttribute{
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{Required: true},
						"value": schema.StringAttribute{Required: true},
					},
				},
			},
		},
	}
}

// ModifyPlan keeps latest_version stable when only metadata changes, so that
// dependent deployments are not re-planned for a rename.
func (r *propertyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state propertyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var diags diag.Diagnostics
	if configJSON(plan.toConfig(ctx, &diags)) == configJSON(state.toConfig(ctx, &diags)) && !diags.HasError() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("latest_version"), state.LatestVersion)...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("version_in_production"), state.VersionInProduction)...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("version_in_staging"), state.VersionInStaging)...)
	}
}

func (r *propertyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan propertyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg := plan.toConfig(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.Client.CreateProperty(ctx, client.PropertyCreate{
		Name:        plan.Name.ValueString(),
		Description: strPtr(plan.Description),
		Version:     cfg,
	})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Creating property", err)
		return
	}
	plan.ID = types.StringValue(id)
	state, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *propertyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior propertyModel
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

// read fetches the property and its latest version. It returns (nil, "", true)
// when the property no longer exists.
func (r *propertyResource) read(ctx context.Context, id string, prior *propertyModel, diags *diag.Diagnostics) (*propertyModel, string, bool) {
	summary, lastModified, err := r.data.Client.GetProperty(ctx, id)
	if client.IsNotFound(err) {
		return nil, "", true
	}
	if err != nil {
		addAPIError(diags, "Reading property "+id, err)
		return nil, "", false
	}
	version, err := r.data.Client.GetLatestPropertyVersion(ctx, id)
	if err != nil {
		addAPIError(diags, "Reading latest version of property "+id, err)
		return nil, "", false
	}

	m := &propertyModel{ID: types.StringValue(id)}
	m.Name = types.StringValue(summary.Name)
	m.Description = strOrNull(summary.Description)
	if prior != nil && prior.Description.IsNull() && summary.Description == "" {
		m.Description = types.StringNull()
	}
	m.fromConfig(ctx, version.Configs, prior, diags)
	m.LatestVersion = types.Int64Value(version.Version)
	m.VersionInProduction = int64FromPtr(summary.VersionInProduction)
	m.VersionInStaging = int64FromPtr(summary.VersionInStaging)
	if diags.HasError() {
		return nil, "", false
	}
	return m, lastModified, true
}

func (r *propertyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state propertyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	lastModified := getPrivate(ctx, req.Private, privateLastModified)

	// 1. Metadata.
	if !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) {
		patch := client.PropertyPatch{Name: strPtr(plan.Name)}
		desc := plan.Description.ValueString()
		patch.Description = &desc
		if err := r.data.Client.PatchProperty(ctx, id, patch, lastModified); err != nil {
			addAPIError(&resp.Diagnostics, "Updating property "+id, err)
			return
		}
		// The metadata patch bumps Last-Modified; refresh it before a version write.
		if _, lm, err := r.data.Client.GetProperty(ctx, id); err == nil {
			lastModified = lm
		}
	}

	// 2. Configuration.
	desired := plan.toConfig(ctx, &resp.Diagnostics)
	current := state.toConfig(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if configJSON(desired) != configJSON(current) {
		latest, err := r.data.Client.GetLatestPropertyVersion(ctx, id)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Reading latest version of property "+id, err)
			return
		}
		if latest.Status.Frozen {
			if _, err := r.data.Client.CreatePropertyVersion(ctx, id, desired); err != nil {
				addAPIError(&resp.Diagnostics, fmt.Sprintf("Creating a new version of property %s", id), err)
				return
			}
		} else {
			patch, err := desired.MergePatch("description")
			if err != nil {
				resp.Diagnostics.AddError("Building configuration patch", err.Error())
				return
			}
			if err := r.data.Client.PatchPropertyVersion(ctx, id, latest.Version, patch, lastModified); err != nil {
				addAPIError(&resp.Diagnostics, fmt.Sprintf("Updating version %d of property %s", latest.Version, id), err)
				return
			}
		}
	}

	newState, newLastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok {
		return
	}
	if newState == nil {
		resp.Diagnostics.AddError("Property disappeared during update", "GET returned 404 right after a successful write.")
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, newLastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *propertyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state propertyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.Client.DeleteProperty(ctx, state.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Deleting property "+state.ID.ValueString(), err)
	}
}

func (r *propertyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
