package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PropertyConfig is the API's propertyConfiguration object. It is used verbatim
// as the "version" of POST /cdn/properties, as the body of POST
// /cdn/properties/{id}/versions, and (as a merge patch) for PATCH
// /cdn/properties/{id}/versions/{n}. Pointer fields distinguish "not set" from
// zero values.
type PropertyConfig struct {
	Description            *string             `json:"description,omitempty"`
	Hostnames              []string            `json:"hostnames,omitempty"`
	CacheKeyHostname       *string             `json:"cacheKeyHostname,omitempty"`
	CacheKeyURI            *string             `json:"cacheKeyUri,omitempty"`
	TLSCertificateID       *string             `json:"tlsCertificateId,omitempty"`
	TLSCertificateID1      *string             `json:"tlsCertificateId1,omitempty"`
	TLSMinVersion          *string             `json:"tlsMinVersion,omitempty"`
	TLSMaxVersion          *string             `json:"tlsMaxVersion,omitempty"`
	TLSCiphers             *string             `json:"tlsCiphers,omitempty"`
	SyntaxVersion          *int64              `json:"syntaxVersion,omitempty"`
	Origins                []Origin            `json:"origins,omitempty"`
	EdgeLogic              *StringOrLines      `json:"edgeLogic,omitempty"`
	HasBeian               *bool               `json:"hasBeian,omitempty"`
	BeianContentType       *int64              `json:"beianContentType,omitempty"`
	EnableIPv6Origin       *bool               `json:"enableIpv6Origin,omitempty"`
	FollowClientIPVersion  *string             `json:"followClientIpVersion,omitempty"`
	AccessControlRules     []AccessControlRule `json:"accessControlRules,omitempty"`
	RealTimeLog            *RealTimeLog        `json:"realTimeLog,omitempty"`
	SchemeInCacheKey       *bool               `json:"schemeInCacheKey,omitempty"`
	AllowProtocolDowngrade *bool               `json:"allowProtocolDowngrade,omitempty"`
	RedirectHTTPToHTTPS    *BoolOrString       `json:"redirectHttpToHttps,omitempty"`
	DisableHTTP2           *bool               `json:"disableHttp2,omitempty"`
	EnableHTTP3            *bool               `json:"enableHttp3,omitempty"`
	VideoSeek              *VideoSeek          `json:"videoSeek,omitempty"`
	TLSSessionTimeout      *int64              `json:"tlsSessionTimeout,omitempty"`
	EnableOCSPStapling     *bool               `json:"enableOcspStapling,omitempty"`
	TLS0RTT                *bool               `json:"tls0Rtt,omitempty"`
	DisableCertAutomation  *bool               `json:"disableCertAutomation,omitempty"`
	ExtraServicePorts      *ServicePorts       `json:"extraServicePorts,omitempty"`
}

// propertyConfigKeys lists every top-level key of PropertyConfig. A merge patch
// built by MergePatch sends an explicit null for each key that is unset so that
// clearing an attribute in HCL actually clears it on the server.
var propertyConfigKeys = []string{
	"description", "hostnames", "cacheKeyHostname", "cacheKeyUri", "tlsCertificateId",
	"tlsCertificateId1", "tlsMinVersion", "tlsMaxVersion", "tlsCiphers", "syntaxVersion",
	"origins", "edgeLogic", "hasBeian", "beianContentType", "enableIpv6Origin",
	"followClientIpVersion", "accessControlRules", "realTimeLog", "schemeInCacheKey",
	"allowProtocolDowngrade", "redirectHttpToHttps", "disableHttp2", "enableHttp3",
	"videoSeek", "tlsSessionTimeout", "enableOcspStapling", "tls0Rtt",
	"disableCertAutomation", "extraServicePorts",
}

// MergePatch renders the config as an RFC 7396 merge patch that sets every
// known key, using null for keys that are unset. Keys listed in keep are left
// out entirely so the server keeps their current value.
func (c PropertyConfig) MergePatch(keep ...string) (map[string]any, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	patch := map[string]any{}
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, err
	}
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[k] = true
	}
	for _, k := range propertyConfigKeys {
		if keepSet[k] {
			delete(patch, k)
			continue
		}
		if _, ok := patch[k]; !ok {
			patch[k] = nil
		}
	}
	return patch, nil
}

// Origin is one entry of PropertyConfig.Origins.
type Origin struct {
	Name                   string                `json:"name"`
	Servers                []string              `json:"servers"`
	SupportedProtocol      *string               `json:"supportedProtocol,omitempty"`
	VerifyOrigin           *bool                 `json:"verifyOrigin,omitempty"`
	HostHeader             *string               `json:"hostHeader,omitempty"`
	DirectConnection       *string               `json:"directConnection,omitempty"`
	Shield                 *string               `json:"shield,omitempty"`
	KeepAliveTimeout       *int64                `json:"keepAliveTimeout,omitempty"`
	PeerFailureTimeout     *PeerFailureTimeout   `json:"peerFailureTimeout,omitempty"`
	PeerSelectionAlgorithm *string               `json:"peerSelectionAlgorithm,omitempty"`
	MaxConnections         *int64                `json:"maxConnections,omitempty"`
	TLSCertificateID       *string               `json:"tlsCertificateId,omitempty"`
	Authentication         *OriginAuthentication `json:"authentication,omitempty"`
}

// PeerFailureTimeout is either an object {failureThreshold, timeout} or the
// literal string "off". Off=true renders the string.
type PeerFailureTimeout struct {
	Off              bool
	FailureThreshold *int64
	Timeout          *int64
}

func (p PeerFailureTimeout) MarshalJSON() ([]byte, error) {
	if p.Off {
		return json.Marshal("off")
	}
	obj := map[string]any{}
	if p.FailureThreshold != nil {
		obj["failureThreshold"] = *p.FailureThreshold
	}
	if p.Timeout != nil {
		obj["timeout"] = *p.Timeout
	}
	return json.Marshal(obj)
}

func (p *PeerFailureTimeout) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*p = PeerFailureTimeout{Off: strings.EqualFold(s, "off")}
		return nil
	}
	var obj struct {
		FailureThreshold *int64 `json:"failureThreshold"`
		Timeout          *int64 `json:"timeout"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("peerFailureTimeout: %w", err)
	}
	*p = PeerFailureTimeout{FailureThreshold: obj.FailureThreshold, Timeout: obj.Timeout}
	return nil
}

// OriginAuthentication is origins[].authentication.
type OriginAuthentication struct {
	MethodName string     `json:"methodName"`
	AWSS3      *AWSS3Auth `json:"awsS3,omitempty"`
}

// AWSS3Auth is origins[].authentication.awsS3.
type AWSS3Auth struct {
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// AccessControlRule is one entry of PropertyConfig.AccessControlRules.
type AccessControlRule struct {
	ID         *string                `json:"id,omitempty"`
	Conditions AccessControlCondition `json:"conditions"`
	Action     AccessControlAction    `json:"action"`
}

// AccessControlCondition is accessControlRules[].conditions.
type AccessControlCondition struct {
	Scheme        *string  `json:"scheme,omitempty"`
	Hostname      *string  `json:"hostname,omitempty"`
	URI           *string  `json:"uri,omitempty"`
	ServerRegions []string `json:"serverRegions,omitempty"`
	ClientRegions []string `json:"clientRegions,omitempty"`
	ClientIPRange []string `json:"clientIpRange,omitempty"`
}

// AccessControlAction is accessControlRules[].action.
type AccessControlAction struct {
	StatusCode string  `json:"statusCode"`
	Message    *string `json:"message,omitempty"`
}

// RealTimeLog is PropertyConfig.RealTimeLog.
type RealTimeLog struct {
	LogURL     string      `json:"logUrl"`
	SampleRate *int64      `json:"sampleRate,omitempty"`
	Escape     *string     `json:"escape,omitempty"`
	Format     string      `json:"format"`
	Headers    []NameValue `json:"headers,omitempty"`
}

// NameValue is a {name, value} pair.
type NameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// VideoSeek is PropertyConfig.VideoSeek.
type VideoSeek struct {
	StartParameter string  `json:"startParameter"`
	EndParameter   *string `json:"endParameter,omitempty"`
}

// ServicePorts is PropertyConfig.ExtraServicePorts.
type ServicePorts struct {
	HTTP  []int64 `json:"http,omitempty"`
	HTTPS []int64 `json:"https,omitempty"`
}

// StringOrLines is a string the API may also return as an array of lines
// (edgeLogic). It always marshals as a single string.
type StringOrLines string

func (s StringOrLines) MarshalJSON() ([]byte, error) { return json.Marshal(string(s)) }

func (s *StringOrLines) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = StringOrLines(str)
		return nil
	}
	var lines []string
	if err := json.Unmarshal(b, &lines); err != nil {
		return fmt.Errorf("expected string or array of strings: %w", err)
	}
	*s = StringOrLines(strings.Join(lines, "\n"))
	return nil
}

// BoolOrString carries redirectHttpToHttps, which the API accepts as a JSON
// boolean or as one of the strings "302", "307", "308". The canonical text
// forms are "true", "false", "302", "307", "308".
type BoolOrString string

func (v BoolOrString) MarshalJSON() ([]byte, error) {
	switch strings.ToLower(string(v)) {
	case "true", "1":
		return json.Marshal(true)
	case "false", "0", "":
		return json.Marshal(false)
	default:
		return json.Marshal(string(v))
	}
}

func (v *BoolOrString) UnmarshalJSON(b []byte) error {
	var boolean bool
	if err := json.Unmarshal(b, &boolean); err == nil {
		*v = BoolOrString(strconv.FormatBool(boolean))
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("expected boolean or string: %w", err)
	}
	switch strings.ToLower(s) {
	case "1", "true":
		*v = "true"
	case "0", "false":
		*v = "false"
	default:
		*v = BoolOrString(s)
	}
	return nil
}

// PropertyCreate is the body of POST /cdn/properties.
type PropertyCreate struct {
	Name        string         `json:"name"`
	Description *string        `json:"description,omitempty"`
	Version     PropertyConfig `json:"version"`
}

// PropertyPatch is the body of PATCH /cdn/properties/{id} (metadata only).
type PropertyPatch struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// PropertySummary is the body of GET /cdn/properties/{id}.
type PropertySummary struct {
	Name                string `json:"name"`
	Description         string `json:"description"`
	Versions            int64  `json:"versions"`
	LatestVersion       *int64 `json:"latestVersion"`
	VersionInProduction *int64 `json:"versionInProduction"`
	VersionInStaging    *int64 `json:"versionInStaging"`
	CreationTime        string `json:"creationTime"`
	LastUpdateTime      string `json:"lastUpdateTime"`
}

// PropertyVersion is the body of GET /cdn/properties/{id}/versions/{n}.
type PropertyVersion struct {
	Version int64          `json:"version"`
	Configs PropertyConfig `json:"configs"`
	Status  VersionStatus  `json:"status"`
}

// VersionStatus is PropertyVersion.Status.
type VersionStatus struct {
	Frozen               bool    `json:"frozen"`
	LastValidationStatus string  `json:"lastValidationStatus"`
	LastValidationTime   *string `json:"lastValidationTime"`
	InProduction         bool    `json:"inProduction"`
	InStaging            bool    `json:"inStaging"`
	CreationTime         string  `json:"creationTime"`
	LastUpdateTime       string  `json:"lastUpdateTime"`
}

// Values of VersionStatus.LastValidationStatus.
const (
	ValidationNotValidated = "NotValidated"
	ValidationInProgress   = "InProgress"
	ValidationValidated    = "Validated"
	ValidationFailed       = "Failed"
)

// IsValidated reports whether the version can be deployed without validating.
func (s VersionStatus) IsValidated() bool {
	return strings.EqualFold(s.LastValidationStatus, ValidationValidated)
}

const propertiesPath = "/cdn/properties"

func propertyPath(id string) string { return propertiesPath + "/" + id }

func propertyVersionPath(id, version string) string {
	return propertyPath(id) + "/versions/" + version
}

// CreateProperty creates a property with version 1 and returns the new ID,
// taken from the Location header.
func (c *Client) CreateProperty(ctx context.Context, req PropertyCreate) (string, error) {
	resp, err := c.Post(ctx, propertiesPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", propertiesPath)
	}
	return id, nil
}

// GetProperty returns the property summary and its Last-Modified header.
func (c *Client) GetProperty(ctx context.Context, id string) (*PropertySummary, string, error) {
	var out PropertySummary
	resp, err := c.Get(ctx, propertyPath(id), &out)
	if err != nil {
		return nil, "", err
	}
	return &out, resp.LastModified(), nil
}

// GetPropertyVersion returns one version. version may be a number or "latest".
func (c *Client) GetPropertyVersion(ctx context.Context, id, version string) (*PropertyVersion, error) {
	var out PropertyVersion
	if _, err := c.Get(ctx, propertyVersionPath(id, version), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetLatestPropertyVersion is GetPropertyVersion(id, "latest").
func (c *Client) GetLatestPropertyVersion(ctx context.Context, id string) (*PropertyVersion, error) {
	return c.GetPropertyVersion(ctx, id, "latest")
}

// PatchProperty updates name/description. lastModified, when non-empty, is sent
// as If-Unmodified-Since.
func (c *Client) PatchProperty(ctx context.Context, id string, req PropertyPatch, lastModified string) error {
	_, err := c.Patch(ctx, propertyPath(id), req, lastModified, nil)
	return err
}

// PatchPropertyVersion applies a merge patch to a non-frozen version.
func (c *Client) PatchPropertyVersion(ctx context.Context, id string, version int64, patch map[string]any, lastModified string) error {
	_, err := c.Patch(ctx, propertyVersionPath(id, strconv.FormatInt(version, 10)), patch, lastModified, nil)
	return err
}

// CreatePropertyVersion creates a new version from a complete configuration and
// returns the new version number from the Location header.
func (c *Client) CreatePropertyVersion(ctx context.Context, id string, cfg PropertyConfig) (int64, error) {
	path := propertyPath(id) + "/versions"
	resp, err := c.Post(ctx, path, cfg, nil)
	if err != nil {
		return 0, err
	}
	n, convErr := strconv.ParseInt(resp.LastID(), 10, 64)
	if convErr != nil {
		return 0, fmt.Errorf("POST %s: cannot read new version number from Location %q", path, resp.Location())
	}
	return n, nil
}

// DeleteProperty deletes a property. A 404 is treated as success.
func (c *Client) DeleteProperty(ctx context.Context, id string) error {
	_, err := c.Delete(ctx, propertyPath(id), nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
