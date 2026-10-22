package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// EdgeHostnameRequest is the body of POST /cdn/edgeHostnames and PUT
// /cdn/edgeHostnames/{edgeHostname}.
type EdgeHostnameRequest struct {
	EdgeHostname       string          `json:"edgeHostname,omitempty"`
	Description        *string         `json:"description,omitempty"`
	ClientZones        []ClientZone    `json:"clientZones"`
	DeliverByHTTPS     *DeliverByHTTPS `json:"deliverByHttps,omitempty"`
	EstimatedBandwidth *string         `json:"estimatedBandwidth,omitempty"`
}

// ClientZone is one entry of clientZones.
type ClientZone struct {
	Region string           `json:"region"`
	ISP    string           `json:"isp"`
	Weight *int64           `json:"weight,omitempty"`
	Action ClientZoneAction `json:"action"`
}

// ClientZoneAction is clientZones[].action. Type selects which other fields
// apply: deliver uses By and EnableIPv6, redirect uses To, reject uses nothing.
type ClientZoneAction struct {
	Type       string   `json:"type"`
	By         []string `json:"by,omitempty"`
	To         []string `json:"to,omitempty"`
	EnableIPv6 *bool    `json:"enableIPv6,omitempty"`
}

// DeliverByHTTPS is the deliverByHttps object.
type DeliverByHTTPS struct {
	State string  `json:"state"`
	ALPN  *string `json:"alpn,omitempty"`
	Port  *int64  `json:"port,omitempty"`
	TTL   *int64  `json:"ttl,omitempty"`
}

// EdgeHostnameView is the body of GET /cdn/edgeHostnames/{edgeHostname}.
type EdgeHostnameView struct {
	OwnerCustomerID int64               `json:"ownerCustomerId"`
	Configs         EdgeHostnameConfigs `json:"configs"`
	CreationTime    string              `json:"creationTime"`
	LastUpdateTime  string              `json:"lastUpdateTime"`
	History         []EdgeHostnameEvent `json:"history"`
}

// EdgeHostnameConfigs is the effective configuration: the most recent succeeded
// one, or the latest submitted one if none has succeeded yet.
type EdgeHostnameConfigs struct {
	EdgeHostname       string          `json:"edgeHostname"`
	Description        string          `json:"description"`
	EstimatedBandwidth string          `json:"estimatedBandwidth"`
	HasBeian           bool            `json:"hasBeian"`
	ClientZones        []ClientZone    `json:"clientZones"`
	DeliverByHTTPS     *DeliverByHTTPS `json:"deliverByHttps"`
}

// EdgeHostnameEvent is one history entry, newest first in History.
type EdgeHostnameEvent struct {
	Operation      string               `json:"operation"`
	Status         string               `json:"status"`
	StatusDetails  string               `json:"statusDetails"`
	SubmissionTime string               `json:"submissionTime"`
	FinishTime     string               `json:"finishTime"`
	Time           string               `json:"time"`
	APIName        string               `json:"apiName"`
	APIAccountID   string               `json:"apiAccountId"`
	APIRequestID   string               `json:"apiRequestId"`
	Configuration  *EdgeHostnameConfigs `json:"configuration"`
}

// Latest returns the newest history entry, or nil.
func (v *EdgeHostnameView) Latest() *EdgeHostnameEvent {
	if v == nil || len(v.History) == 0 {
		return nil
	}
	return &v.History[0]
}

// InFlight reports whether the newest history entry is still pending.
func (v *EdgeHostnameView) InFlight() bool {
	e := v.Latest()
	return e != nil && Classify(KindEdgeHostnameHistory, e.Status) == Pending
}

const edgeHostnamesPath = "/cdn/edgeHostnames"

func edgeHostnamePath(name string) string {
	return edgeHostnamesPath + "/" + url.PathEscape(strings.ToLower(name))
}

// CreateEdgeHostname creates an edge hostname and returns its name from the
// Location header (the API may generate one when none was requested).
func (c *Client) CreateEdgeHostname(ctx context.Context, req EdgeHostnameRequest) (string, error) {
	resp, err := c.Post(ctx, edgeHostnamesPath, req, nil)
	if err != nil {
		return "", err
	}
	name := resp.LastID()
	if name == "" {
		name = req.EdgeHostname
	}
	if name == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", edgeHostnamesPath)
	}
	return name, nil
}

// GetEdgeHostname returns the edge hostname and its Last-Modified header.
func (c *Client) GetEdgeHostname(ctx context.Context, name string) (*EdgeHostnameView, string, error) {
	var out EdgeHostnameView
	resp, err := c.Get(ctx, edgeHostnamePath(name), &out)
	if err != nil {
		return nil, "", err
	}
	return &out, resp.LastModified(), nil
}

// PutEdgeHostname replaces the whole configuration. The body's edgeHostname
// must equal name. lastModified is sent as If-Unmodified-Since when non-empty.
func (c *Client) PutEdgeHostname(ctx context.Context, name string, req EdgeHostnameRequest, lastModified string) error {
	req.EdgeHostname = name
	_, err := c.Put(ctx, edgeHostnamePath(name), req, lastModified, nil)
	return err
}

// DeleteEdgeHostname deletes an edge hostname. When skipUsageCheck is true the
// Check-Usage: no header bypasses the recent-traffic guard. A 404 is success.
func (c *Client) DeleteEdgeHostname(ctx context.Context, name string, skipUsageCheck bool) error {
	var headers map[string]string
	if skipUsageCheck {
		headers = map[string]string{HeaderCheckUsage: "no"}
	}
	_, err := c.Delete(ctx, edgeHostnamePath(name), headers)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// EdgeHostnameFailedError is returned when history[0] reaches "failed".
type EdgeHostnameFailedError struct {
	EdgeHostname string
	Operation    string
	Details      string
}

func (e *EdgeHostnameFailedError) Error() string {
	msg := fmt.Sprintf("edge hostname %s: %s failed", e.EdgeHostname, e.Operation)
	if e.Details != "" {
		msg += ": " + e.Details
	}
	return msg
}

// WaitForEdgeHostname polls GET until history[0] leaves waiting/inprogress and
// returns the final view (with its Last-Modified). A failed entry is returned
// as *EdgeHostnameFailedError.
func (c *Client) WaitForEdgeHostname(ctx context.Context, name string, cfg PollConfig) (*EdgeHostnameView, string, error) {
	var view *EdgeHostnameView
	var lastModified string
	err := Poll(ctx, cfg, func(ctx context.Context) (bool, error) {
		v, lm, err := c.GetEdgeHostname(ctx, name)
		if err != nil {
			return false, err
		}
		view, lastModified = v, lm
		e := v.Latest()
		if e == nil {
			return true, nil
		}
		switch Classify(KindEdgeHostnameHistory, e.Status) {
		case Pending:
			return false, nil
		case Failed:
			return false, &EdgeHostnameFailedError{EdgeHostname: name, Operation: e.Operation, Details: e.StatusDetails}
		default:
			return true, nil
		}
	})
	if err != nil {
		return nil, "", err
	}
	return view, lastModified, nil
}

// WaitForEdgeHostnameDeletion polls GET until it returns 404 (the only success
// signal for deletion). A history[0] of "failed" while the resource still
// exists is returned as *EdgeHostnameFailedError.
func (c *Client) WaitForEdgeHostnameDeletion(ctx context.Context, name string, cfg PollConfig) error {
	return Poll(ctx, cfg, func(ctx context.Context) (bool, error) {
		v, _, err := c.GetEdgeHostname(ctx, name)
		if IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if e := v.Latest(); e != nil && Classify(KindEdgeHostnameHistory, e.Status) == Failed {
			return false, &EdgeHostnameFailedError{EdgeHostname: name, Operation: e.Operation, Details: e.StatusDetails}
		}
		return false, nil
	})
}
