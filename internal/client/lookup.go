package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// HostnameInfo is the body of GET /cdn/hostnames/{hostname}. An unknown
// hostname yields 200 with both property fields null.
type HostnameInfo struct {
	Hostname             string              `json:"hostname"`
	PropertyInProduction *HostnameDeployment `json:"propertyInProduction"`
	PropertyInStaging    *HostnameDeployment `json:"propertyInStaging"`
}

// HostnameDeployment describes the property version serving a hostname.
type HostnameDeployment struct {
	PropertyID    string `json:"propertyId"`
	Version       int64  `json:"version"`
	CertificateID string `json:"certificateId"`
}

// Found reports whether the hostname is deployed anywhere.
func (h *HostnameInfo) Found() bool {
	return h != nil && (h.PropertyInProduction != nil || h.PropertyInStaging != nil)
}

// GetHostname looks up where a hostname is deployed.
func (c *Client) GetHostname(ctx context.Context, hostname string) (*HostnameInfo, error) {
	var out HostnameInfo
	path := "/cdn/hostnames/" + url.PathEscape(strings.ToLower(hostname))
	if _, err := c.Get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Shield is one entry of GET /cdn/shields.
type Shield struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Region           string   `json:"region"`
	IPv4             []string `json:"ipV4"`
	IPv6             []string `json:"ipV6"`
	LastUpdated      string   `json:"lastUpdated"`
	EffectiveDate    string   `json:"effectiveDate"`
	UsedInProperties bool     `json:"usedInProperties"`
}

// ShieldFilter holds the optional query parameters of GET /cdn/shields.
type ShieldFilter struct {
	Search           string
	UsedInProperties *bool
}

// ListShields returns origin shields matching the filter.
func (c *Client) ListShields(ctx context.Context, f ShieldFilter) ([]Shield, error) {
	q := url.Values{}
	if f.Search != "" {
		q.Set("search", f.Search)
	}
	if f.UsedInProperties != nil {
		q.Set("usedInProperties", strconv.FormatBool(*f.UsedInProperties))
	}
	var out struct {
		Shields []Shield `json:"shields"`
	}
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/cdn/shields", Query: q}, &out); err != nil {
		return nil, err
	}
	return out.Shields, nil
}

// CodeName is an {code, name} pair returned by the ISP and client region lists.
type CodeName struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// ListISPs returns ISPs, optionally filtered by substring and client region code.
func (c *Client) ListISPs(ctx context.Context, search, regionCode string) ([]CodeName, error) {
	q := url.Values{}
	if search != "" {
		q.Set("search", search)
	}
	if regionCode != "" {
		q.Set("regionCode", regionCode)
	}
	var out []CodeName
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/cdn/isps", Query: q}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListClientRegions returns client regions, optionally filtered by substring
// and ISP code.
func (c *Client) ListClientRegions(ctx context.Context, search, ispCode string) ([]CodeName, error) {
	q := url.Values{}
	if search != "" {
		q.Set("search", search)
	}
	if ispCode != "" {
		q.Set("ispCode", ispCode)
	}
	var out []CodeName
	if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/cdn/clientRegions", Query: q}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
