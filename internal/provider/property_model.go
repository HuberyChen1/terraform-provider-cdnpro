package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

// propertyModel is the Terraform state/plan shape of cdnpro_property. Property
// and version attributes are flattened; versioning is handled internally.
type propertyModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`

	Hostnames              types.List   `tfsdk:"hostnames"`
	Origins                types.List   `tfsdk:"origins"`
	EdgeLogic              types.String `tfsdk:"edge_logic"`
	SyntaxVersion          types.Int64  `tfsdk:"syntax_version"`
	HasBeian               types.Bool   `tfsdk:"has_beian"`
	BeianContentType       types.Int64  `tfsdk:"beian_content_type"`
	AllowProtocolDowngrade types.Bool   `tfsdk:"allow_protocol_downgrade"`
	EnableIPv6Origin       types.Bool   `tfsdk:"enable_ipv6_origin"`
	FollowClientIPVersion  types.String `tfsdk:"follow_client_ip_version"`
	CacheKeyHostname       types.String `tfsdk:"cache_key_hostname"`
	CacheKeyURI            types.String `tfsdk:"cache_key_uri"`
	SchemeInCacheKey       types.Bool   `tfsdk:"scheme_in_cache_key"`
	RedirectHTTPToHTTPS    types.String `tfsdk:"redirect_http_to_https"`
	DisableHTTP2           types.Bool   `tfsdk:"disable_http2"`
	EnableHTTP3            types.Bool   `tfsdk:"enable_http3"`
	ExtraServicePorts      types.Object `tfsdk:"extra_service_ports"`
	TLSCertificateID       types.String `tfsdk:"tls_certificate_id"`
	TLSCertificateID1      types.String `tfsdk:"tls_certificate_id1"`
	TLSMinVersion          types.String `tfsdk:"tls_min_version"`
	TLSMaxVersion          types.String `tfsdk:"tls_max_version"`
	TLSCiphers             types.String `tfsdk:"tls_ciphers"`
	EnableOCSPStapling     types.Bool   `tfsdk:"enable_ocsp_stapling"`
	TLS0RTT                types.Bool   `tfsdk:"tls_0rtt"`
	TLSSessionTimeout      types.Int64  `tfsdk:"tls_session_timeout"`
	DisableCertAutomation  types.Bool   `tfsdk:"disable_cert_automation"`
	AccessControlRules     types.List   `tfsdk:"access_control_rules"`
	RealTimeLog            types.Object `tfsdk:"real_time_log"`
	VideoSeek              types.Object `tfsdk:"video_seek"`

	LatestVersion       types.Int64 `tfsdk:"latest_version"`
	VersionInProduction types.Int64 `tfsdk:"version_in_production"`
	VersionInStaging    types.Int64 `tfsdk:"version_in_staging"`
}

type originModel struct {
	Name                   types.String `tfsdk:"name"`
	Servers                types.List   `tfsdk:"servers"`
	SupportedProtocol      types.String `tfsdk:"supported_protocol"`
	VerifyOrigin           types.Bool   `tfsdk:"verify_origin"`
	HostHeader             types.String `tfsdk:"host_header"`
	DirectConnection       types.String `tfsdk:"direct_connection"`
	Shield                 types.String `tfsdk:"shield"`
	KeepAliveTimeout       types.Int64  `tfsdk:"keep_alive_timeout"`
	PeerFailureTimeout     types.Object `tfsdk:"peer_failure_timeout"`
	PeerSelectionAlgorithm types.String `tfsdk:"peer_selection_algorithm"`
	MaxConnections         types.Int64  `tfsdk:"max_connections"`
	TLSCertificateID       types.String `tfsdk:"tls_certificate_id"`
	Authentication         types.Object `tfsdk:"authentication"`
}

type peerFailureTimeoutModel struct {
	Disabled         types.Bool  `tfsdk:"disabled"`
	FailureThreshold types.Int64 `tfsdk:"failure_threshold"`
	Timeout          types.Int64 `tfsdk:"timeout"`
}

type originAuthModel struct {
	MethodName types.String `tfsdk:"method_name"`
	AWSS3      types.Object `tfsdk:"aws_s3"`
}

type awsS3Model struct {
	Region    types.String `tfsdk:"region"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
}

type extraServicePortsModel struct {
	HTTP  types.List `tfsdk:"http"`
	HTTPS types.List `tfsdk:"https"`
}

type accessControlRuleModel struct {
	ID         types.String `tfsdk:"id"`
	Conditions types.Object `tfsdk:"conditions"`
	Action     types.Object `tfsdk:"action"`
}

type accessControlConditionsModel struct {
	Scheme        types.String `tfsdk:"scheme"`
	Hostname      types.String `tfsdk:"hostname"`
	URI           types.String `tfsdk:"uri"`
	ServerRegions types.List   `tfsdk:"server_regions"`
	ClientRegions types.List   `tfsdk:"client_regions"`
	ClientIPRange types.List   `tfsdk:"client_ip_range"`
}

type accessControlActionModel struct {
	StatusCode types.String `tfsdk:"status_code"`
	Message    types.String `tfsdk:"message"`
}

type realTimeLogModel struct {
	LogURL     types.String `tfsdk:"log_url"`
	SampleRate types.Int64  `tfsdk:"sample_rate"`
	Escape     types.String `tfsdk:"escape"`
	Format     types.String `tfsdk:"format"`
	Headers    types.List   `tfsdk:"headers"`
}

type nameValueModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}

type videoSeekModel struct {
	StartParameter types.String `tfsdk:"start_parameter"`
	EndParameter   types.String `tfsdk:"end_parameter"`
}

// Object types derived from the resource schema so the model conversions and
// the schema can never disagree.
var (
	propertySchemaOnce = buildPropertySchema()

	originObjectType             = nestedObjectType(propertySchemaOnce.Attributes["origins"])
	peerFailureTimeoutObjectType = nestedObjectType(originObjectType.AttrTypes["peer_failure_timeout"])
	originAuthObjectType         = nestedObjectType(originObjectType.AttrTypes["authentication"])
	awsS3ObjectType              = nestedObjectType(originAuthObjectType.AttrTypes["aws_s3"])
	extraServicePortsObjectType  = nestedObjectType(propertySchemaOnce.Attributes["extra_service_ports"])
	accessControlRuleObjectType  = nestedObjectType(propertySchemaOnce.Attributes["access_control_rules"])
	accessConditionsObjectType   = nestedObjectType(accessControlRuleObjectType.AttrTypes["conditions"])
	accessActionObjectType       = nestedObjectType(accessControlRuleObjectType.AttrTypes["action"])
	realTimeLogObjectType        = nestedObjectType(propertySchemaOnce.Attributes["real_time_log"])
	nameValueObjectType          = nestedObjectType(realTimeLogObjectType.AttrTypes["headers"])
	videoSeekObjectType          = nestedObjectType(propertySchemaOnce.Attributes["video_seek"])
)

// nestedObjectType extracts the object type of a nested attribute, whether it
// is a single nested attribute (object) or a list/set nested attribute.
func nestedObjectType(v any) types.ObjectType {
	var t attr.Type
	switch a := v.(type) {
	case attr.Type:
		t = a
	case interface{ GetType() attr.Type }:
		t = a.GetType()
	default:
		panic("nestedObjectType: unsupported value")
	}
	switch tt := t.(type) {
	case types.ObjectType:
		return tt
	case types.ListType:
		return tt.ElemType.(types.ObjectType)
	case types.SetType:
		return tt.ElemType.(types.ObjectType)
	default:
		panic("nestedObjectType: not an object, list or set type")
	}
}

// --- model -> API -------------------------------------------------------------

// toConfig renders the version-level attributes as an API property configuration.
func (m *propertyModel) toConfig(ctx context.Context, diags *diag.Diagnostics) client.PropertyConfig {
	cfg := client.PropertyConfig{
		Hostnames:              listToStrings(ctx, m.Hostnames, diags),
		CacheKeyHostname:       strPtrNonEmpty(m.CacheKeyHostname),
		CacheKeyURI:            strPtr(m.CacheKeyURI),
		TLSCertificateID:       strPtrNonEmpty(m.TLSCertificateID),
		TLSCertificateID1:      strPtrNonEmpty(m.TLSCertificateID1),
		TLSMinVersion:          strPtr(m.TLSMinVersion),
		TLSMaxVersion:          strPtr(m.TLSMaxVersion),
		TLSCiphers:             strPtrNonEmpty(m.TLSCiphers),
		SyntaxVersion:          int64Ptr(m.SyntaxVersion),
		Origins:                m.originsToAPI(ctx, diags),
		HasBeian:               boolPtr(m.HasBeian),
		BeianContentType:       int64Ptr(m.BeianContentType),
		EnableIPv6Origin:       boolPtr(m.EnableIPv6Origin),
		FollowClientIPVersion:  strPtr(m.FollowClientIPVersion),
		AccessControlRules:     m.accessControlRulesToAPI(ctx, diags),
		RealTimeLog:            m.realTimeLogToAPI(ctx, diags),
		SchemeInCacheKey:       boolPtr(m.SchemeInCacheKey),
		AllowProtocolDowngrade: boolPtr(m.AllowProtocolDowngrade),
		DisableHTTP2:           boolPtr(m.DisableHTTP2),
		EnableHTTP3:            boolPtr(m.EnableHTTP3),
		VideoSeek:              m.videoSeekToAPI(ctx, diags),
		TLSSessionTimeout:      int64Ptr(m.TLSSessionTimeout),
		EnableOCSPStapling:     boolPtr(m.EnableOCSPStapling),
		TLS0RTT:                boolPtr(m.TLS0RTT),
		DisableCertAutomation:  boolPtr(m.DisableCertAutomation),
		ExtraServicePorts:      m.extraServicePortsToAPI(ctx, diags),
	}
	if s := strPtr(m.EdgeLogic); s != nil {
		el := client.StringOrLines(*s)
		cfg.EdgeLogic = &el
	}
	if s := strPtr(m.RedirectHTTPToHTTPS); s != nil {
		v := client.BoolOrString(*s)
		cfg.RedirectHTTPToHTTPS = &v
	}
	return cfg
}

func (m *propertyModel) originsToAPI(ctx context.Context, diags *diag.Diagnostics) []client.Origin {
	if m.Origins.IsNull() || m.Origins.IsUnknown() {
		return nil
	}
	var models []originModel
	diags.Append(m.Origins.ElementsAs(ctx, &models, false)...)
	out := make([]client.Origin, 0, len(models))
	for _, o := range models {
		origin := client.Origin{
			Name:                   o.Name.ValueString(),
			Servers:                listToStrings(ctx, o.Servers, diags),
			SupportedProtocol:      strPtr(o.SupportedProtocol),
			VerifyOrigin:           boolPtr(o.VerifyOrigin),
			HostHeader:             strPtrNonEmpty(o.HostHeader),
			DirectConnection:       strPtr(o.DirectConnection),
			Shield:                 strPtrNonEmpty(o.Shield),
			KeepAliveTimeout:       int64Ptr(o.KeepAliveTimeout),
			PeerSelectionAlgorithm: strPtr(o.PeerSelectionAlgorithm),
			MaxConnections:         int64Ptr(o.MaxConnections),
			TLSCertificateID:       strPtrNonEmpty(o.TLSCertificateID),
		}
		if !o.PeerFailureTimeout.IsNull() && !o.PeerFailureTimeout.IsUnknown() {
			var p peerFailureTimeoutModel
			diags.Append(o.PeerFailureTimeout.As(ctx, &p, objectAsOptions)...)
			origin.PeerFailureTimeout = &client.PeerFailureTimeout{
				Off:              p.Disabled.ValueBool(),
				FailureThreshold: int64Ptr(p.FailureThreshold),
				Timeout:          int64Ptr(p.Timeout),
			}
		}
		if !o.Authentication.IsNull() && !o.Authentication.IsUnknown() {
			var a originAuthModel
			diags.Append(o.Authentication.As(ctx, &a, objectAsOptions)...)
			auth := &client.OriginAuthentication{MethodName: a.MethodName.ValueString()}
			if !a.AWSS3.IsNull() && !a.AWSS3.IsUnknown() {
				var s3 awsS3Model
				diags.Append(a.AWSS3.As(ctx, &s3, objectAsOptions)...)
				auth.AWSS3 = &client.AWSS3Auth{
					Region:    s3.Region.ValueString(),
					AccessKey: s3.AccessKey.ValueString(),
					SecretKey: s3.SecretKey.ValueString(),
				}
			}
			origin.Authentication = auth
		}
		out = append(out, origin)
	}
	return out
}

func (m *propertyModel) accessControlRulesToAPI(ctx context.Context, diags *diag.Diagnostics) []client.AccessControlRule {
	if m.AccessControlRules.IsNull() || m.AccessControlRules.IsUnknown() {
		return nil
	}
	var models []accessControlRuleModel
	diags.Append(m.AccessControlRules.ElementsAs(ctx, &models, false)...)
	out := make([]client.AccessControlRule, 0, len(models))
	for _, r := range models {
		rule := client.AccessControlRule{ID: strPtrNonEmpty(r.ID)}
		if !r.Conditions.IsNull() && !r.Conditions.IsUnknown() {
			var c accessControlConditionsModel
			diags.Append(r.Conditions.As(ctx, &c, objectAsOptions)...)
			rule.Conditions = client.AccessControlCondition{
				Scheme:        strPtrNonEmpty(c.Scheme),
				Hostname:      strPtrNonEmpty(c.Hostname),
				URI:           strPtrNonEmpty(c.URI),
				ServerRegions: listToStrings(ctx, c.ServerRegions, diags),
				ClientRegions: listToStrings(ctx, c.ClientRegions, diags),
				ClientIPRange: listToStrings(ctx, c.ClientIPRange, diags),
			}
		}
		if !r.Action.IsNull() && !r.Action.IsUnknown() {
			var a accessControlActionModel
			diags.Append(r.Action.As(ctx, &a, objectAsOptions)...)
			rule.Action = client.AccessControlAction{
				StatusCode: a.StatusCode.ValueString(),
				Message:    strPtrNonEmpty(a.Message),
			}
		}
		out = append(out, rule)
	}
	return out
}

func (m *propertyModel) realTimeLogToAPI(ctx context.Context, diags *diag.Diagnostics) *client.RealTimeLog {
	if m.RealTimeLog.IsNull() || m.RealTimeLog.IsUnknown() {
		return nil
	}
	var r realTimeLogModel
	diags.Append(m.RealTimeLog.As(ctx, &r, objectAsOptions)...)
	out := &client.RealTimeLog{
		LogURL:     r.LogURL.ValueString(),
		SampleRate: int64Ptr(r.SampleRate),
		Escape:     strPtr(r.Escape),
		Format:     r.Format.ValueString(),
	}
	if !r.Headers.IsNull() && !r.Headers.IsUnknown() {
		var hs []nameValueModel
		diags.Append(r.Headers.ElementsAs(ctx, &hs, false)...)
		for _, h := range hs {
			out.Headers = append(out.Headers, client.NameValue{Name: h.Name.ValueString(), Value: h.Value.ValueString()})
		}
	}
	return out
}

func (m *propertyModel) videoSeekToAPI(ctx context.Context, diags *diag.Diagnostics) *client.VideoSeek {
	if m.VideoSeek.IsNull() || m.VideoSeek.IsUnknown() {
		return nil
	}
	var v videoSeekModel
	diags.Append(m.VideoSeek.As(ctx, &v, objectAsOptions)...)
	return &client.VideoSeek{
		StartParameter: v.StartParameter.ValueString(),
		EndParameter:   strPtrNonEmpty(v.EndParameter),
	}
}

func (m *propertyModel) extraServicePortsToAPI(ctx context.Context, diags *diag.Diagnostics) *client.ServicePorts {
	if m.ExtraServicePorts.IsNull() || m.ExtraServicePorts.IsUnknown() {
		return nil
	}
	var p extraServicePortsModel
	diags.Append(m.ExtraServicePorts.As(ctx, &p, objectAsOptions)...)
	return &client.ServicePorts{
		HTTP:  listToInt64s(ctx, p.HTTP, diags),
		HTTPS: listToInt64s(ctx, p.HTTPS, diags),
	}
}

// --- API -> model -------------------------------------------------------------

// fromConfig fills the version-level attributes from an API configuration.
// prior supplies values whose API representation is lossy (e.g. "" vs null).
func (m *propertyModel) fromConfig(ctx context.Context, cfg client.PropertyConfig, prior *propertyModel, diags *diag.Diagnostics) {
	m.Hostnames = stringListOrEmpty(ctx, cfg.Hostnames, diags)
	m.Origins = originsFromAPI(ctx, cfg.Origins, diags)
	if cfg.EdgeLogic != nil {
		m.EdgeLogic = types.StringValue(string(*cfg.EdgeLogic))
	} else {
		m.EdgeLogic = types.StringNull()
	}
	m.SyntaxVersion = int64FromPtr(cfg.SyntaxVersion)
	m.HasBeian = boolFromPtr(cfg.HasBeian)
	m.BeianContentType = int64FromPtr(cfg.BeianContentType)
	m.AllowProtocolDowngrade = boolFromPtr(cfg.AllowProtocolDowngrade)
	m.EnableIPv6Origin = boolFromPtr(cfg.EnableIPv6Origin)
	m.FollowClientIPVersion = strFromPtr(cfg.FollowClientIPVersion)
	m.CacheKeyHostname = strFromPtrNonEmpty(cfg.CacheKeyHostname)
	m.CacheKeyURI = strFromPtr(cfg.CacheKeyURI)
	m.SchemeInCacheKey = boolFromPtr(cfg.SchemeInCacheKey)
	if cfg.RedirectHTTPToHTTPS != nil {
		m.RedirectHTTPToHTTPS = types.StringValue(string(*cfg.RedirectHTTPToHTTPS))
	} else {
		m.RedirectHTTPToHTTPS = types.StringNull()
	}
	m.DisableHTTP2 = boolFromPtr(cfg.DisableHTTP2)
	m.EnableHTTP3 = boolFromPtr(cfg.EnableHTTP3)
	m.ExtraServicePorts = extraServicePortsFromAPI(ctx, cfg.ExtraServicePorts, diags)
	m.TLSCertificateID = strFromPtrNonEmpty(cfg.TLSCertificateID)
	m.TLSCertificateID1 = strFromPtrNonEmpty(cfg.TLSCertificateID1)
	m.TLSMinVersion = strFromPtr(cfg.TLSMinVersion)
	m.TLSMaxVersion = strFromPtr(cfg.TLSMaxVersion)
	m.TLSCiphers = strFromPtrNonEmpty(cfg.TLSCiphers)
	m.EnableOCSPStapling = boolFromPtr(cfg.EnableOCSPStapling)
	m.TLS0RTT = boolFromPtr(cfg.TLS0RTT)
	m.TLSSessionTimeout = int64FromPtr(cfg.TLSSessionTimeout)
	m.DisableCertAutomation = boolFromPtr(cfg.DisableCertAutomation)
	m.AccessControlRules = accessControlRulesFromAPI(ctx, cfg.AccessControlRules, diags)
	m.RealTimeLog = realTimeLogFromAPI(ctx, cfg.RealTimeLog, diags)
	m.VideoSeek = videoSeekFromAPI(ctx, cfg.VideoSeek, diags)

	// Optional booleans the server reports as false when unset: keep null when
	// the prior state had null so an untouched attribute does not drift.
	if prior != nil {
		m.HasBeian = keepNullBool(prior.HasBeian, m.HasBeian)
		m.AllowProtocolDowngrade = keepNullBool(prior.AllowProtocolDowngrade, m.AllowProtocolDowngrade)
		m.EnableIPv6Origin = keepNullBool(prior.EnableIPv6Origin, m.EnableIPv6Origin)
		m.SchemeInCacheKey = keepNullBool(prior.SchemeInCacheKey, m.SchemeInCacheKey)
		m.DisableHTTP2 = keepNullBool(prior.DisableHTTP2, m.DisableHTTP2)
		m.EnableHTTP3 = keepNullBool(prior.EnableHTTP3, m.EnableHTTP3)
		m.EnableOCSPStapling = keepNullBool(prior.EnableOCSPStapling, m.EnableOCSPStapling)
		m.TLS0RTT = keepNullBool(prior.TLS0RTT, m.TLS0RTT)
		m.DisableCertAutomation = keepNullBool(prior.DisableCertAutomation, m.DisableCertAutomation)
	}
}

func strFromPtrNonEmpty(p *string) types.String {
	if p == nil || *p == "" {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

// keepNullBool returns null when prior was null and current is false.
func keepNullBool(prior, current types.Bool) types.Bool {
	if prior.IsNull() && !current.IsNull() && !current.ValueBool() {
		return types.BoolNull()
	}
	return current
}

func originsFromAPI(ctx context.Context, origins []client.Origin, diags *diag.Diagnostics) types.List {
	if origins == nil {
		return types.ListNull(originObjectType)
	}
	models := make([]originModel, 0, len(origins))
	for _, o := range origins {
		om := originModel{
			Name:                   types.StringValue(o.Name),
			Servers:                stringListOrEmpty(ctx, o.Servers, diags),
			SupportedProtocol:      strFromPtr(o.SupportedProtocol),
			VerifyOrigin:           boolFromPtr(o.VerifyOrigin),
			HostHeader:             strFromPtrNonEmpty(o.HostHeader),
			DirectConnection:       strFromPtr(o.DirectConnection),
			Shield:                 strFromPtrNonEmpty(o.Shield),
			KeepAliveTimeout:       int64FromPtr(o.KeepAliveTimeout),
			PeerSelectionAlgorithm: strFromPtr(o.PeerSelectionAlgorithm),
			MaxConnections:         int64FromPtr(o.MaxConnections),
			TLSCertificateID:       strFromPtrNonEmpty(o.TLSCertificateID),
			PeerFailureTimeout:     types.ObjectNull(peerFailureTimeoutObjectType.AttrTypes),
			Authentication:         types.ObjectNull(originAuthObjectType.AttrTypes),
		}
		if o.PeerFailureTimeout != nil {
			p := peerFailureTimeoutModel{
				Disabled:         types.BoolValue(o.PeerFailureTimeout.Off),
				FailureThreshold: int64FromPtr(o.PeerFailureTimeout.FailureThreshold),
				Timeout:          int64FromPtr(o.PeerFailureTimeout.Timeout),
			}
			obj, d := types.ObjectValueFrom(ctx, peerFailureTimeoutObjectType.AttrTypes, p)
			diags.Append(d...)
			om.PeerFailureTimeout = obj
		}
		if o.Authentication != nil {
			a := originAuthModel{
				MethodName: types.StringValue(o.Authentication.MethodName),
				AWSS3:      types.ObjectNull(awsS3ObjectType.AttrTypes),
			}
			if o.Authentication.AWSS3 != nil {
				s3, d := types.ObjectValueFrom(ctx, awsS3ObjectType.AttrTypes, awsS3Model{
					Region:    types.StringValue(o.Authentication.AWSS3.Region),
					AccessKey: types.StringValue(o.Authentication.AWSS3.AccessKey),
					SecretKey: types.StringValue(o.Authentication.AWSS3.SecretKey),
				})
				diags.Append(d...)
				a.AWSS3 = s3
			}
			obj, d := types.ObjectValueFrom(ctx, originAuthObjectType.AttrTypes, a)
			diags.Append(d...)
			om.Authentication = obj
		}
		models = append(models, om)
	}
	l, d := types.ListValueFrom(ctx, originObjectType, models)
	diags.Append(d...)
	return l
}

func accessControlRulesFromAPI(ctx context.Context, rules []client.AccessControlRule, diags *diag.Diagnostics) types.List {
	if rules == nil {
		return types.ListNull(accessControlRuleObjectType)
	}
	models := make([]accessControlRuleModel, 0, len(rules))
	for _, r := range rules {
		cond, d := types.ObjectValueFrom(ctx, accessConditionsObjectType.AttrTypes, accessControlConditionsModel{
			Scheme:        strFromPtrNonEmpty(r.Conditions.Scheme),
			Hostname:      strFromPtrNonEmpty(r.Conditions.Hostname),
			URI:           strFromPtrNonEmpty(r.Conditions.URI),
			ServerRegions: stringList(ctx, r.Conditions.ServerRegions, diags),
			ClientRegions: stringList(ctx, r.Conditions.ClientRegions, diags),
			ClientIPRange: stringList(ctx, r.Conditions.ClientIPRange, diags),
		})
		diags.Append(d...)
		act, d := types.ObjectValueFrom(ctx, accessActionObjectType.AttrTypes, accessControlActionModel{
			StatusCode: types.StringValue(r.Action.StatusCode),
			Message:    strFromPtrNonEmpty(r.Action.Message),
		})
		diags.Append(d...)
		models = append(models, accessControlRuleModel{ID: strFromPtrNonEmpty(r.ID), Conditions: cond, Action: act})
	}
	l, d := types.ListValueFrom(ctx, accessControlRuleObjectType, models)
	diags.Append(d...)
	return l
}

func realTimeLogFromAPI(ctx context.Context, r *client.RealTimeLog, diags *diag.Diagnostics) types.Object {
	if r == nil {
		return types.ObjectNull(realTimeLogObjectType.AttrTypes)
	}
	headers := types.ListNull(nameValueObjectType)
	if r.Headers != nil {
		hs := make([]nameValueModel, 0, len(r.Headers))
		for _, h := range r.Headers {
			hs = append(hs, nameValueModel{Name: types.StringValue(h.Name), Value: types.StringValue(h.Value)})
		}
		l, d := types.ListValueFrom(ctx, nameValueObjectType, hs)
		diags.Append(d...)
		headers = l
	}
	obj, d := types.ObjectValueFrom(ctx, realTimeLogObjectType.AttrTypes, realTimeLogModel{
		LogURL:     types.StringValue(r.LogURL),
		SampleRate: int64FromPtr(r.SampleRate),
		Escape:     strFromPtr(r.Escape),
		Format:     types.StringValue(r.Format),
		Headers:    headers,
	})
	diags.Append(d...)
	return obj
}

func videoSeekFromAPI(ctx context.Context, v *client.VideoSeek, diags *diag.Diagnostics) types.Object {
	if v == nil {
		return types.ObjectNull(videoSeekObjectType.AttrTypes)
	}
	obj, d := types.ObjectValueFrom(ctx, videoSeekObjectType.AttrTypes, videoSeekModel{
		StartParameter: types.StringValue(v.StartParameter),
		EndParameter:   strFromPtrNonEmpty(v.EndParameter),
	})
	diags.Append(d...)
	return obj
}

func extraServicePortsFromAPI(ctx context.Context, p *client.ServicePorts, diags *diag.Diagnostics) types.Object {
	if p == nil {
		return types.ObjectNull(extraServicePortsObjectType.AttrTypes)
	}
	obj, d := types.ObjectValueFrom(ctx, extraServicePortsObjectType.AttrTypes, extraServicePortsModel{
		HTTP:  int64List(ctx, p.HTTP, diags),
		HTTPS: int64List(ctx, p.HTTPS, diags),
	})
	diags.Append(d...)
	return obj
}

// configJSON renders a configuration for change detection.
func configJSON(cfg client.PropertyConfig) string {
	b, _ := json.Marshal(cfg)
	return string(b)
}
