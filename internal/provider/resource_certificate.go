package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

// Private state keys holding SHA-256 hashes of the write-only attributes.
const (
	privateHashPrivateKey  = "hash_private_key"
	privateHashCertificate = "hash_certificate"
	privateHashChainCert   = "hash_chain_cert"
)

var (
	_ resource.Resource                = (*certificateResource)(nil)
	_ resource.ResourceWithConfigure   = (*certificateResource)(nil)
	_ resource.ResourceWithImportState = (*certificateResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*certificateResource)(nil)
)

// NewCertificateResource returns the cdnpro_certificate resource.
func NewCertificateResource() resource.Resource { return &certificateResource{} }

type certificateResource struct {
	data *ProviderData
}

type certificateModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	AutoRenew   types.String `tfsdk:"auto_renew"`
	ForceRenew  types.Bool   `tfsdk:"force_renew"`
	Comments    types.String `tfsdk:"comments"`

	PrivateKey         types.String `tfsdk:"private_key"`
	Certificate        types.String `tfsdk:"certificate"`
	ChainCert          types.String `tfsdk:"chain_cert"`
	IdentificationInfo types.Object `tfsdk:"identification_info"`

	LatestVersion       types.Int64 `tfsdk:"latest_version"`
	VersionInProduction types.Int64 `tfsdk:"version_in_production"`
	VersionInStaging    types.Int64 `tfsdk:"version_in_staging"`
	UsageInProduction   types.List  `tfsdk:"usage_in_production"`
	UsageInStaging      types.List  `tfsdk:"usage_in_staging"`

	ExpirationTime          types.String `tfsdk:"expiration_time"`
	Subject                 types.String `tfsdk:"subject"`
	Issuer                  types.String `tfsdk:"issuer"`
	SignatureAlgo           types.String `tfsdk:"signature_algo"`
	SerialNumber            types.String `tfsdk:"serial_number"`
	Fingerprint             types.String `tfsdk:"fingerprint"`
	Algorithm               types.String `tfsdk:"algorithm"`
	KeyLength               types.Int64  `tfsdk:"key_length"`
	SubjectAlternativeNames types.List   `tfsdk:"subject_alternative_names"`
	ChainCertificates       types.List   `tfsdk:"chain_certificates"`
}

type identificationInfoModel struct {
	Country                 types.String `tfsdk:"country"`
	State                   types.String `tfsdk:"state"`
	City                    types.String `tfsdk:"city"`
	Company                 types.String `tfsdk:"company"`
	Department              types.String `tfsdk:"department"`
	CommonName              types.String `tfsdk:"common_name"`
	Email                   types.String `tfsdk:"email"`
	SubjectAlternativeNames types.List   `tfsdk:"subject_alternative_names"`
}

type certificateUsageModel struct {
	CustomerID types.Int64  `tfsdk:"customer_id"`
	PropertyID types.String `tfsdk:"property_id"`
	Hostnames  types.List   `tfsdk:"hostnames"`
	Origins    types.List   `tfsdk:"origins"`
}

type chainCertModel struct {
	Subject        types.String `tfsdk:"subject"`
	Issuer         types.String `tfsdk:"issuer"`
	ExpirationTime types.String `tfsdk:"expiration_time"`
	SignatureAlgo  types.String `tfsdk:"signature_algo"`
}

var (
	certificateSchemaOnce     = buildCertificateSchema()
	identificationInfoObjType = nestedObjectType(certificateSchemaOnce.Attributes["identification_info"])
	certificateUsageObjType   = nestedObjectType(certificateSchemaOnce.Attributes["usage_in_production"])
	chainCertObjType          = nestedObjectType(certificateSchemaOnce.Attributes["chain_certificates"])
)

func (r *certificateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_certificate"
}

func (r *certificateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = certificateSchemaOnce
}

func (r *certificateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildCertificateSchema() schema.Schema {
	usage := schema.ListNestedAttribute{
		Computed: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"customer_id": schema.Int64Attribute{Computed: true},
				"property_id": schema.StringAttribute{Computed: true},
				"hostnames":   schema.ListAttribute{ElementType: types.StringType, Computed: true},
				"origins":     schema.ListAttribute{ElementType: types.StringType, Computed: true},
			},
		},
	}
	return schema.Schema{
		MarkdownDescription: "A CDN Pro TLS certificate together with its versions. Certificate material " +
			"(`private_key`, `certificate`, `chain_cert`) is write-only: the API never returns it, so the provider " +
			"stores only hashes and mints a new certificate version whenever any of them changes. Requires Terraform 1.11 or later. " +
			"A new version must keep the key algorithm of the existing versions; to switch between RSA and EC, create a new resource.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name":        schema.StringAttribute{Required: true},
			"description": schema.StringAttribute{Optional: true},
			"auto_renew": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("Off"),
				Validators:          []validator.String{stringvalidator.OneOfCaseInsensitive("LE", "Off")},
				MarkdownDescription: "`LE` requests Let's Encrypt renewal near expiration; `Off` disables it.",
			},
			"force_renew": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"comments": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Comment stored on the certificate version. Changing it creates a new version.",
			},
			"private_key": schema.StringAttribute{
				Required:  true,
				WriteOnly: true,
				Sensitive: true,
				MarkdownDescription: "Private key in PEM format, or the literal `RSA2048` / `ECC256` to have a key generated " +
					"(with `certificate = \"GENERATE\"`). Encrypted client-side before upload.",
			},
			"certificate": schema.StringAttribute{
				Required:            true,
				WriteOnly:           true,
				Sensitive:           true,
				MarkdownDescription: "Certificate in PEM format, or the literal `GENERATE` for a ten-day self-signed test certificate.",
			},
			"chain_cert": schema.StringAttribute{
				Optional:            true,
				WriteOnly:           true,
				Sensitive:           true,
				MarkdownDescription: "Intermediate chain in PEM format.",
			},
			"identification_info": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Subject of a generated certificate. Ignored for uploaded certificates.",
				Attributes: map[string]schema.Attribute{
					"country":                   schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthBetween(2, 2)}},
					"state":                     schema.StringAttribute{Optional: true},
					"city":                      schema.StringAttribute{Optional: true},
					"company":                   schema.StringAttribute{Optional: true},
					"department":                schema.StringAttribute{Optional: true},
					"common_name":               schema.StringAttribute{Optional: true},
					"email":                     schema.StringAttribute{Optional: true},
					"subject_alternative_names": schema.ListAttribute{ElementType: types.StringType, Optional: true},
				},
			},
			"latest_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of the newest version. Reference this from `cdnpro_deployment`.",
			},
			"version_in_production": schema.Int64Attribute{Computed: true},
			"version_in_staging":    schema.Int64Attribute{Computed: true},
			"usage_in_production":   usage,
			"usage_in_staging":      usage,
			"expiration_time": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Metadata of the latest version, not necessarily of the version deployed.",
			},
			"subject":                   schema.StringAttribute{Computed: true},
			"issuer":                    schema.StringAttribute{Computed: true},
			"signature_algo":            schema.StringAttribute{Computed: true},
			"serial_number":             schema.StringAttribute{Computed: true},
			"fingerprint":               schema.StringAttribute{Computed: true},
			"algorithm":                 schema.StringAttribute{Computed: true},
			"key_length":                schema.Int64Attribute{Computed: true},
			"subject_alternative_names": schema.ListAttribute{ElementType: types.StringType, Computed: true},
			"chain_certificates": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"subject":         schema.StringAttribute{Computed: true},
						"issuer":          schema.StringAttribute{Computed: true},
						"expiration_time": schema.StringAttribute{Computed: true},
						"signature_algo":  schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

// writeOnlyHashes returns the hashes of the three write-only attributes as
// found in config. Null attributes hash to "".
func writeOnlyHashes(m *certificateModel) (pk, cert, chain string) {
	if !m.PrivateKey.IsNull() && !m.PrivateKey.IsUnknown() {
		pk = sha256Hex(m.PrivateKey.ValueString())
	}
	if !m.Certificate.IsNull() && !m.Certificate.IsUnknown() {
		cert = sha256Hex(m.Certificate.ValueString())
	}
	if !m.ChainCert.IsNull() && !m.ChainCert.IsUnknown() {
		chain = sha256Hex(m.ChainCert.ValueString())
	}
	return
}

// materialChanged compares config hashes against private state.
func (r *certificateResource) materialChanged(ctx context.Context, cfg *certificateModel, priv privateGetter) bool {
	if cfg.PrivateKey.IsUnknown() || cfg.Certificate.IsUnknown() || cfg.ChainCert.IsUnknown() {
		return true
	}
	pk, cert, chain := writeOnlyHashes(cfg)
	return pk != getPrivate(ctx, priv, privateHashPrivateKey) ||
		cert != getPrivate(ctx, priv, privateHashCertificate) ||
		chain != getPrivate(ctx, priv, privateHashChainCert)
}

func (r *certificateResource) storeHashes(ctx context.Context, cfg *certificateModel, priv privateSetter, diags *diag.Diagnostics) {
	pk, cert, chain := writeOnlyHashes(cfg)
	setPrivate(ctx, priv, privateHashPrivateKey, pk, diags)
	setPrivate(ctx, priv, privateHashCertificate, cert, diags)
	setPrivate(ctx, priv, privateHashChainCert, chain, diags)
}

// ModifyPlan drives updates for write-only changes: when the configured
// material differs from the stored hashes, latest_version (and the version
// metadata) become unknown so Terraform plans an update.
func (r *certificateResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var cfg, plan, state certificateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	newVersion := r.materialChanged(ctx, &cfg, req.Private) ||
		!plan.Comments.Equal(state.Comments) ||
		!plan.IdentificationInfo.Equal(state.IdentificationInfo)

	// Computed attributes that describe the latest version.
	versionAttrs := map[string]struct {
		unknown attr.Value
		state   attr.Value
	}{
		"latest_version":            {types.Int64Unknown(), state.LatestVersion},
		"expiration_time":           {types.StringUnknown(), state.ExpirationTime},
		"subject":                   {types.StringUnknown(), state.Subject},
		"issuer":                    {types.StringUnknown(), state.Issuer},
		"signature_algo":            {types.StringUnknown(), state.SignatureAlgo},
		"serial_number":             {types.StringUnknown(), state.SerialNumber},
		"fingerprint":               {types.StringUnknown(), state.Fingerprint},
		"algorithm":                 {types.StringUnknown(), state.Algorithm},
		"key_length":                {types.Int64Unknown(), state.KeyLength},
		"subject_alternative_names": {types.ListUnknown(types.StringType), state.SubjectAlternativeNames},
		"chain_certificates":        {types.ListUnknown(chainCertObjType), state.ChainCertificates},
	}
	// Computed attributes that describe deployments, never changed by this resource.
	deploymentAttrs := map[string]attr.Value{
		"version_in_production": state.VersionInProduction,
		"version_in_staging":    state.VersionInStaging,
		"usage_in_production":   state.UsageInProduction,
		"usage_in_staging":      state.UsageInStaging,
	}
	for name, v := range versionAttrs {
		value := v.state
		if newVersion {
			value = v.unknown
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(name), value)...)
	}
	for name, value := range deploymentAttrs {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(name), value)...)
	}
}

func (r *certificateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, cfg certificateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiReq := r.metadataRequest(&plan)
	apiReq.NewVersion = r.newVersion(ctx, &plan, &cfg, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.data.Client.CreateCertificate(ctx, apiReq)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Creating certificate", err)
		return
	}
	r.storeHashes(ctx, &cfg, resp.Private, &resp.Diagnostics)
	state, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok || state == nil {
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *certificateResource) metadataRequest(m *certificateModel) client.CertificateRequest {
	desc := m.Description.ValueString()
	return client.CertificateRequest{
		Name:        strPtr(m.Name),
		Description: &desc,
		AutoRenew:   strPtr(m.AutoRenew),
		ForceRenew:  boolPtr(m.ForceRenew),
	}
}

func (r *certificateResource) newVersion(ctx context.Context, plan, cfg *certificateModel, diags *diag.Diagnostics) *client.CertVersion {
	nv := &client.CertVersion{
		Comments:    strPtr(plan.Comments),
		PrivateKey:  strPtr(cfg.PrivateKey),
		Certificate: strPtr(cfg.Certificate),
		ChainCert:   strPtrNonEmpty(cfg.ChainCert),
	}
	if !plan.IdentificationInfo.IsNull() && !plan.IdentificationInfo.IsUnknown() {
		var info identificationInfoModel
		diags.Append(plan.IdentificationInfo.As(ctx, &info, objectAsOptions)...)
		nv.IdentificationInfo = &client.IdentificationInfo{
			Country:                 strPtrNonEmpty(info.Country),
			State:                   strPtrNonEmpty(info.State),
			City:                    strPtrNonEmpty(info.City),
			Company:                 strPtrNonEmpty(info.Company),
			Department:              strPtrNonEmpty(info.Department),
			CommonName:              strPtrNonEmpty(info.CommonName),
			Email:                   strPtrNonEmpty(info.Email),
			SubjectAlternativeNames: listToStrings(ctx, info.SubjectAlternativeNames, diags),
		}
	}
	return nv
}

func (r *certificateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior certificateModel
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

func (r *certificateResource) read(ctx context.Context, id string, prior *certificateModel, diags *diag.Diagnostics) (*certificateModel, string, bool) {
	summary, lastModified, err := r.data.Client.GetCertificate(ctx, id)
	if client.IsNotFound(err) {
		return nil, "", true
	}
	if err != nil {
		addAPIError(diags, "Reading certificate "+id, err)
		return nil, "", false
	}
	detail, err := r.data.Client.GetCertificateVersion(ctx, id, "latest")
	if err != nil {
		addAPIError(diags, "Reading latest version of certificate "+id, err)
		return nil, "", false
	}

	m := &certificateModel{
		ID:          types.StringValue(id),
		Name:        types.StringValue(summary.Name),
		Description: strOrNull(summary.Description),
		AutoRenew:   keepCase(prior.AutoRenew, summary.AutoRenew),
		ForceRenew:  types.BoolValue(summary.ForceRenew),
		Comments:    strOrNull(detail.Comments),
		// Write-only attributes are never stored.
		PrivateKey:         types.StringNull(),
		Certificate:        types.StringNull(),
		ChainCert:          types.StringNull(),
		IdentificationInfo: prior.IdentificationInfo,
	}
	if prior.Comments.IsNull() && detail.Comments == "" {
		m.Comments = types.StringNull()
	}
	if m.IdentificationInfo.IsUnknown() {
		m.IdentificationInfo = types.ObjectNull(identificationInfoObjType.AttrTypes)
	}
	latest := detail.Version
	if latest == 0 {
		latest = summary.LatestVersion()
	}
	m.LatestVersion = types.Int64Value(latest)
	m.VersionInProduction = int64FromPtr(summary.VersionInProduction)
	m.VersionInStaging = int64FromPtr(summary.VersionInStaging)
	m.UsageInProduction = usageList(ctx, summary.UsageInProduction, diags)
	m.UsageInStaging = usageList(ctx, summary.UsageInStaging, diags)

	m.ExpirationTime = strOrNull(detail.ExpirationTime)
	m.Subject = strOrNull(detail.Subject)
	m.Issuer = strOrNull(detail.Issuer)
	m.SignatureAlgo = strOrNull(detail.SignatureAlgo)
	m.SerialNumber = strOrNull(detail.SerialNumber)
	m.Fingerprint = strOrNull(detail.Fingerprint)
	m.Algorithm = strOrNull(detail.Algorithm)
	m.KeyLength = types.Int64Value(detail.KeyLength)
	m.SubjectAlternativeNames = stringListOrEmpty(ctx, detail.SubjectAlternativeNames, diags)
	chain := make([]chainCertModel, 0, len(detail.ChainCertificates))
	for _, c := range detail.ChainCertificates {
		chain = append(chain, chainCertModel{
			Subject:        types.StringValue(c.Subject),
			Issuer:         types.StringValue(c.Issuer),
			ExpirationTime: types.StringValue(c.ExpirationTime),
			SignatureAlgo:  types.StringValue(c.SignatureAlgo),
		})
	}
	l, d := types.ListValueFrom(ctx, chainCertObjType, chain)
	diags.Append(d...)
	m.ChainCertificates = l
	if diags.HasError() {
		return nil, "", false
	}
	return m, lastModified, true
}

func usageList(ctx context.Context, usage []client.CertificateUsage, diags *diag.Diagnostics) types.List {
	models := make([]certificateUsageModel, 0, len(usage))
	for _, u := range usage {
		models = append(models, certificateUsageModel{
			CustomerID: types.Int64Value(u.CustomerID),
			PropertyID: types.StringValue(u.PropertyID),
			Hostnames:  stringListOrEmpty(ctx, u.Hostnames, diags),
			Origins:    stringListOrEmpty(ctx, u.Origins, diags),
		})
	}
	l, d := types.ListValueFrom(ctx, certificateUsageObjType, models)
	diags.Append(d...)
	return l
}

func (r *certificateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, cfg certificateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()

	apiReq := client.CertificateRequest{}
	metadataChanged := !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) ||
		!plan.AutoRenew.Equal(state.AutoRenew) || !plan.ForceRenew.Equal(state.ForceRenew)
	if metadataChanged {
		apiReq = r.metadataRequest(&plan)
	}
	newVersion := r.materialChanged(ctx, &cfg, req.Private) ||
		!plan.Comments.Equal(state.Comments) ||
		!plan.IdentificationInfo.Equal(state.IdentificationInfo)
	if newVersion {
		apiReq.NewVersion = r.newVersion(ctx, &plan, &cfg, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if metadataChanged || newVersion {
		lastModified := getPrivate(ctx, req.Private, privateLastModified)
		if _, err := r.data.Client.PatchCertificate(ctx, id, apiReq, lastModified); err != nil {
			addAPIError(&resp.Diagnostics, fmt.Sprintf("Updating certificate %s", id), err)
			return
		}
	}
	r.storeHashes(ctx, &cfg, resp.Private, &resp.Diagnostics)

	newState, lastModified, ok := r.read(ctx, id, &plan, &resp.Diagnostics)
	if !ok {
		return
	}
	if newState == nil {
		resp.Diagnostics.AddError("Certificate disappeared during update", "GET returned 404 right after a successful write.")
		return
	}
	setPrivate(ctx, resp.Private, privateLastModified, lastModified, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *certificateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state certificateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.Client.DeleteCertificate(ctx, state.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Deleting certificate "+state.ID.ValueString(), err)
	}
}

func (r *certificateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
