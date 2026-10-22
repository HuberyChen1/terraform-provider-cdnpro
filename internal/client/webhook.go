package client

import (
	"context"
	"fmt"
)

// WebhookRequest is the body of POST and PATCH /cdn/webhooks[/{id}]. On PATCH a
// missing field means "no change", so pointer fields are used throughout;
// Description supports an explicit null (see WebhookPatch).
type WebhookRequest struct {
	Name        *string             `json:"name,omitempty"`
	Description *string             `json:"description,omitempty"`
	URL         *string             `json:"url,omitempty"`
	Credentials *WebhookCredentials `json:"credentials,omitempty"`
}

// WebhookCredentials is the credentials object. The API requires user and
// secretKey to be set together.
type WebhookCredentials struct {
	User      string `json:"user"`
	SecretKey string `json:"secretKey,omitempty"`
}

// WebhookPatch renders a PATCH body where clearDescription sends an explicit
// null for description.
func WebhookPatch(req WebhookRequest, clearDescription bool) map[string]any {
	patch := map[string]any{}
	if req.Name != nil {
		patch["name"] = *req.Name
	}
	if req.URL != nil {
		patch["url"] = *req.URL
	}
	if clearDescription {
		patch["description"] = nil
	} else if req.Description != nil {
		patch["description"] = *req.Description
	}
	if req.Credentials != nil {
		patch["credentials"] = req.Credentials
	}
	return patch
}

// Webhook is the body of GET /cdn/webhooks/{id}. The API does not echo the ID;
// the client fills it in. Credentials.SecretKey is returned by the API but the
// provider never stores it.
type Webhook struct {
	ID          string              `json:"-"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	URL         string              `json:"url"`
	Credentials *WebhookCredentials `json:"credentials"`
	MetaData    WebhookMetaData     `json:"metaData"`
}

// WebhookMetaData is Webhook.MetaData.
type WebhookMetaData struct {
	CreationTime   string `json:"creationTime"`
	LastUpdateTime string `json:"lastUpdateTime"`
	LastCallTime   string `json:"lastCallTime"`
	TotalCalls     int64  `json:"totalCalls"`
}

const webhooksPath = "/cdn/webhooks"

func webhookPath(id string) string { return webhooksPath + "/" + id }

// CreateWebhook creates a webhook and returns its ID from the Location header.
func (c *Client) CreateWebhook(ctx context.Context, req WebhookRequest) (string, error) {
	resp, err := c.Post(ctx, webhooksPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", webhooksPath)
	}
	return id, nil
}

// GetWebhook returns the webhook and its Last-Modified header.
func (c *Client) GetWebhook(ctx context.Context, id string) (*Webhook, string, error) {
	var out Webhook
	resp, err := c.Get(ctx, webhookPath(id), &out)
	if err != nil {
		return nil, "", err
	}
	out.ID = id
	return &out, resp.LastModified(), nil
}

// PatchWebhook updates a webhook. lastModified is sent as If-Unmodified-Since
// when non-empty.
func (c *Client) PatchWebhook(ctx context.Context, id string, patch map[string]any, lastModified string) error {
	_, err := c.Patch(ctx, webhookPath(id), patch, lastModified, nil)
	return err
}

// DeleteWebhook deletes a webhook. A 404 is treated as success.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	_, err := c.Delete(ctx, webhookPath(id), nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
