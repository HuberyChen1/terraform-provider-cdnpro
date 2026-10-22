package client

import (
	"context"
	"fmt"
)

// SecretRequest is the body of POST /cdn/secrets. For PATCH use a merge patch
// map (see SecretPatch) so that description can be cleared with null.
type SecretRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Secret      string  `json:"secret"`
}

// SecretPatch builds the merge patch for PATCH /cdn/secrets/{id}. A nil
// description clears it; an empty secret leaves the stored value untouched.
func SecretPatch(name string, description *string, secret string) map[string]any {
	patch := map[string]any{"name": name, "description": description}
	if secret != "" {
		patch["secret"] = secret
	}
	return patch
}

// Secret is the body of GET /cdn/secrets/{id}. Secret is masked.
type Secret struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	Secret            string        `json:"secret"`
	OwnerCustomerID   int64         `json:"ownerCustomerId"`
	CreationTime      string        `json:"creationTime"`
	LastUpdateTime    string        `json:"lastUpdateTime"`
	UsageInProduction []SecretUsage `json:"usageInProduction"`
	UsageInStaging    []SecretUsage `json:"usageInStaging"`
}

// SecretUsage is one entry of usageInProduction / usageInStaging.
type SecretUsage struct {
	PropertyID string `json:"propertyId"`
}

const secretsPath = "/cdn/secrets"

func secretPath(id string) string { return secretsPath + "/" + id }

// CreateSecret creates a secret and returns its ID from the Location header.
func (c *Client) CreateSecret(ctx context.Context, req SecretRequest) (string, error) {
	resp, err := c.Post(ctx, secretsPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", secretsPath)
	}
	return id, nil
}

// GetSecret returns the secret and its Last-Modified header.
func (c *Client) GetSecret(ctx context.Context, id string) (*Secret, string, error) {
	var out Secret
	resp, err := c.Get(ctx, secretPath(id), &out)
	if err != nil {
		return nil, "", err
	}
	if out.ID == "" {
		out.ID = id
	}
	return &out, resp.LastModified(), nil
}

// PatchSecret applies a merge patch. lastModified is sent as If-Unmodified-Since
// when non-empty.
func (c *Client) PatchSecret(ctx context.Context, id string, patch map[string]any, lastModified string) error {
	_, err := c.Patch(ctx, secretPath(id), patch, lastModified, nil)
	return err
}

// DeleteSecret deletes a secret. A 404 is treated as success.
func (c *Client) DeleteSecret(ctx context.Context, id string) error {
	_, err := c.Delete(ctx, secretPath(id), nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
