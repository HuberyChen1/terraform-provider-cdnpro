package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// LogConfigRequest is the body of POST /cdn/report/logConfigs and, as a merge
// patch, of PATCH /cdn/report/logConfigs/{id}. On PATCH a null
// logDownloadFormat / logDownloadStorageDays / logDownloadFileSpanMinutes
// resets the field to the API default, and a null description clears it.
type LogConfigRequest struct {
	Description                *string  `json:"description"`
	Hostnames                  []string `json:"hostnames"`
	LogDownloadFormat          *string  `json:"logDownloadFormat"`
	LogDownloadStorageDays     *int64   `json:"logDownloadStorageDays"`
	LogDownloadFileSpanMinutes *int64   `json:"logDownloadFileSpanMinutes"`
}

// LogConfig is the body of GET /cdn/report/logConfigs/{id}. The ID is a JSON
// number; the provider carries it as a string.
type LogConfig struct {
	ID             json.Number     `json:"id"`
	Configs        LogConfigConfig `json:"configs"`
	CreationTime   string          `json:"creationTime"`
	LastUpdateTime string          `json:"lastUpdateTime"`
}

// LogConfigConfig is LogConfig.Configs.
type LogConfigConfig struct {
	Description                string   `json:"description"`
	Hostnames                  []string `json:"hostnames"`
	LogDownloadFormat          string   `json:"logDownloadFormat"`
	LogDownloadStorageDays     int64    `json:"logDownloadStorageDays"`
	LogDownloadFileSpanMinutes int64    `json:"logDownloadFileSpanMinutes"`
}

// API defaults applied when a field is omitted or null.
const (
	DefaultLogDownloadFormat          = `%cltip %rmtuser [%apachet] "%method %url %protocol" %statuscode %rspsize "%referer" "%ua" %rsptime`
	DefaultLogDownloadStorageDays     = int64(14)
	DefaultLogDownloadFileSpanMinutes = int64(30)
)

const logConfigsPath = "/cdn/report/logConfigs"

func logConfigPath(id string) string { return logConfigsPath + "/" + id }

// CreateLogConfig creates a log config and returns its (numeric) ID as a string.
func (c *Client) CreateLogConfig(ctx context.Context, req LogConfigRequest) (string, error) {
	resp, err := c.Post(ctx, logConfigsPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if _, convErr := strconv.ParseInt(id, 10, 64); convErr != nil {
		return "", fmt.Errorf("POST %s: cannot read numeric ID from Location %q", logConfigsPath, resp.Location())
	}
	return id, nil
}

// GetLogConfig returns the log config and its Last-Modified header.
func (c *Client) GetLogConfig(ctx context.Context, id string) (*LogConfig, string, error) {
	var out LogConfig
	resp, err := c.Get(ctx, logConfigPath(id), &out)
	if err != nil {
		return nil, "", err
	}
	if out.ID == "" {
		out.ID = json.Number(id)
	}
	return &out, resp.LastModified(), nil
}

// PatchLogConfig applies req as a merge patch. lastModified is sent as
// If-Unmodified-Since when non-empty.
func (c *Client) PatchLogConfig(ctx context.Context, id string, req LogConfigRequest, lastModified string) error {
	_, err := c.Patch(ctx, logConfigPath(id), req, lastModified, nil)
	return err
}

// DeleteLogConfig deletes a log config. A 404 is treated as success.
func (c *Client) DeleteLogConfig(ctx context.Context, id string) error {
	_, err := c.Delete(ctx, logConfigPath(id), nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
