// Package client is a thin HTTP client for the CDN Pro API. It owns request
// signing, the Date header, JSON encoding, error decoding and the Last-Modified /
// If-Unmodified-Since plumbing. Resource-specific calls live in the sibling files
// (property.go, certificate.go, ...).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	HeaderIfUnmodifiedSince = "If-Unmodified-Since"
	HeaderLastModified      = "Last-Modified"
	HeaderCheckUsage        = "Check-Usage"
	HeaderCheckCertificate  = "Check-Certificate"
	HeaderRequestID         = "X-Request-Id"

	defaultUserAgent = "terraform-provider-cdnpro"
	defaultTimeout   = 60 * time.Second
)

// Config configures a Client.
type Config struct {
	// Server is the API origin, e.g. "https://ngapi.cdnetworks.com". A trailing
	// slash and a trailing "/cdn" are both tolerated.
	Server  string
	APIUser string
	APIKey  string
	// HTTPClient is optional; a client with a 60s timeout is used when nil.
	HTTPClient *http.Client
	UserAgent  string
	// Now is optional and exists so tests can pin the Date header.
	Now func() time.Time
}

// Client talks to one CDN Pro API server as one API user.
type Client struct {
	baseURL    *url.URL
	apiUser    string
	apiKey     string
	httpClient *http.Client
	userAgent  string
	now        func() time.Time
}

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Server) == "" {
		return nil, errors.New("server must not be empty")
	}
	if strings.TrimSpace(cfg.APIUser) == "" {
		return nil, errors.New("api_user must not be empty")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("api_key must not be empty")
	}
	server := strings.TrimRight(strings.TrimSpace(cfg.Server), "/")
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	server = strings.TrimSuffix(server, "/cdn")
	base, err := url.Parse(server)
	if err != nil {
		return nil, fmt.Errorf("invalid server %q: %w", cfg.Server, err)
	}
	if base.Host == "" {
		return nil, fmt.Errorf("invalid server %q: missing host", cfg.Server)
	}
	c := &Client{
		baseURL:    base,
		apiUser:    cfg.APIUser,
		apiKey:     cfg.APIKey,
		httpClient: cfg.HTTPClient,
		userAgent:  cfg.UserAgent,
		now:        cfg.Now,
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if c.userAgent == "" {
		c.userAgent = defaultUserAgent
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

// Date returns the Date header value for a request sent right now. Callers that
// must encrypt a payload with the request date (certificate private keys) take
// the value from here and pass it back through Request.Date so both match.
func (c *Client) Date() string {
	return HTTPDate(c.now())
}

// Request describes one API call.
type Request struct {
	Method string
	// Path is relative to the server, e.g. "/cdn/properties/abc".
	Path  string
	Query url.Values
	// Body is JSON encoded when non-nil.
	Body any
	// Headers are extra request headers (If-Unmodified-Since, Check-Usage, ...).
	Headers map[string]string
	// Date overrides the Date header. Leave empty unless the body was encrypted
	// against a specific date.
	Date string
}

// Response is the decoded outcome of a successful (2xx) call.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Location returns the Location response header, or "".
func (r *Response) Location() string {
	if r == nil {
		return ""
	}
	return r.Header.Get("Location")
}

// LastID returns the final path segment of the Location header, which is how
// the API reports the ID of a newly created resource.
func (r *Response) LastID() string {
	loc := r.Location()
	if loc == "" {
		return ""
	}
	if u, err := url.Parse(loc); err == nil && u.Path != "" {
		loc = u.Path
	}
	loc = strings.TrimRight(loc, "/")
	if i := strings.LastIndex(loc, "/"); i >= 0 {
		return loc[i+1:]
	}
	return loc
}

// LastModified returns the Last-Modified response header, or "".
func (r *Response) LastModified() string {
	if r == nil {
		return ""
	}
	return r.Header.Get(HeaderLastModified)
}

// Decode unmarshals the body into out. A nil out or empty body is a no-op.
func (r *Response) Decode(out any) error {
	if out == nil || len(bytes.TrimSpace(r.Body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Body, out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

// Do performs req and decodes a 2xx JSON body into out (when non-nil). Non-2xx
// responses are returned as *APIError.
func (c *Client) Do(ctx context.Context, req Request, out any) (*Response, error) {
	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := resp.Decode(out); err != nil {
		return resp, err
	}
	return resp, nil
}

func (c *Client) do(ctx context.Context, req Request) (*Response, error) {
	if req.Method == "" {
		return nil, errors.New("request method is required")
	}
	u := c.baseURL.JoinPath(req.Path)
	if len(req.Query) > 0 {
		u.RawQuery = req.Query.Encode()
	}

	var body io.Reader
	if req.Body != nil {
		buf, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(buf)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), body)
	if err != nil {
		return nil, err
	}

	date := req.Date
	if date == "" {
		date = c.Date()
	}
	httpReq.Header.Set("Date", date)
	httpReq.Header.Set("Authorization", BasicAuthorization(c.apiUser, c.apiKey, date))
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range req.Headers {
		if v != "" {
			httpReq.Header.Set(k, v)
		}
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.Path, err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", req.Method, req.Path, err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		return nil, decodeAPIError(req, httpResp, raw)
	}
	return &Response{StatusCode: httpResp.StatusCode, Header: httpResp.Header, Body: raw}, nil
}

func decodeAPIError(req Request, resp *http.Response, raw []byte) error {
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Method:     req.Method,
		Path:       req.Path,
		RequestID:  resp.Header.Get(HeaderRequestID),
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &body) == nil && (body.Code != "" || body.Message != "") {
		apiErr.Code = body.Code
		apiErr.Message = body.Message
	} else {
		apiErr.Message = strings.TrimSpace(string(raw))
		if apiErr.Message == "" {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
	}
	return apiErr
}

// Get is shorthand for a GET with no body.
func (c *Client) Get(ctx context.Context, path string, out any) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodGet, Path: path}, out)
}

// Post is shorthand for a JSON POST.
func (c *Client) Post(ctx context.Context, path string, body, out any) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodPost, Path: path, Body: body}, out)
}

// Patch is shorthand for a JSON PATCH guarded by If-Unmodified-Since when
// lastModified is non-empty.
func (c *Client) Patch(ctx context.Context, path string, body any, lastModified string, out any) (*Response, error) {
	return c.Do(ctx, Request{
		Method:  http.MethodPatch,
		Path:    path,
		Body:    body,
		Headers: ifUnmodifiedSince(lastModified),
	}, out)
}

// Put is shorthand for a JSON PUT guarded by If-Unmodified-Since when
// lastModified is non-empty.
func (c *Client) Put(ctx context.Context, path string, body any, lastModified string, out any) (*Response, error) {
	return c.Do(ctx, Request{
		Method:  http.MethodPut,
		Path:    path,
		Body:    body,
		Headers: ifUnmodifiedSince(lastModified),
	}, out)
}

// Delete is shorthand for a DELETE with optional extra headers.
func (c *Client) Delete(ctx context.Context, path string, headers map[string]string) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodDelete, Path: path, Headers: headers}, nil)
}

func ifUnmodifiedSince(lastModified string) map[string]string {
	if lastModified == "" {
		return nil
	}
	return map[string]string{HeaderIfUnmodifiedSince: lastModified}
}
