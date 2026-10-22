package client

import (
	"context"
	"fmt"
)

// PurgeRequest is the body of POST /cdn/purges.
type PurgeRequest struct {
	Name          *string     `json:"name,omitempty"`
	Target        string      `json:"target"`
	Action        *string     `json:"action,omitempty"`
	FileURLs      []string    `json:"fileUrls,omitempty"`
	FileHeaders   []NameValue `json:"fileHeaders,omitempty"`
	DirURLs       []string    `json:"dirUrls,omitempty"`
	RegexPatterns []string    `json:"regexPatterns,omitempty"`
	Webhook       *string     `json:"webhook,omitempty"`
}

// TaskError is one entry of a purge/prefetch task's errors array.
type TaskError struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

// PurgeTask is the body of GET /cdn/purges/{id}. Status fields are top-level.
// The API does not echo the ID; the client fills it in.
type PurgeTask struct {
	ID             string      `json:"-"`
	Name           string      `json:"name"`
	Target         string      `json:"target"`
	Action         string      `json:"action"`
	Status         string      `json:"status"`
	SuccessRate    *float64    `json:"successRate"`
	SubmissionTime string      `json:"submissionTime"`
	FinishTime     string      `json:"finishTime"`
	Errors         []TaskError `json:"errors"`
}

// PrefetchFile is one entry of PrefetchRequest.FileList.
type PrefetchFile struct {
	URL     string      `json:"url"`
	Headers []NameValue `json:"headers,omitempty"`
}

// PrefetchRequest is the body of POST /cdn/prefetches. There is no target:
// prefetches always hit production.
type PrefetchRequest struct {
	Name          *string        `json:"name,omitempty"`
	FileList      []PrefetchFile `json:"fileList"`
	Regions       []string       `json:"regions,omitempty"`
	StartTime     *string        `json:"startTime,omitempty"`
	Webhook       *string        `json:"webhook,omitempty"`
	PrefetchLevel *string        `json:"prefetchLevel,omitempty"`
}

// PrefetchTask is the body of GET /cdn/prefetches/{id}. Unlike purges, the
// status fields are nested under metaData.
type PrefetchTask struct {
	Name          string           `json:"name"`
	StartTime     string           `json:"startTime"`
	PrefetchLevel string           `json:"prefetchLevel"`
	MetaData      PrefetchMetaData `json:"metaData"`
}

// PrefetchMetaData is PrefetchTask.MetaData.
type PrefetchMetaData struct {
	ID             string      `json:"id"`
	Status         string      `json:"status"`
	SuccessRate    *float64    `json:"successRate"`
	SubmissionTime string      `json:"submissionTime"`
	FinishTime     string      `json:"finishTime"`
	Errors         []TaskError `json:"errors"`
}

const (
	purgesPath     = "/cdn/purges"
	prefetchesPath = "/cdn/prefetches"
)

// CreatePurge submits a purge and returns its ID.
func (c *Client) CreatePurge(ctx context.Context, req PurgeRequest) (string, error) {
	resp, err := c.Post(ctx, purgesPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", purgesPath)
	}
	return id, nil
}

// GetPurge fetches one purge task.
func (c *Client) GetPurge(ctx context.Context, id string) (*PurgeTask, error) {
	var out PurgeTask
	if _, err := c.Get(ctx, purgesPath+"/"+id, &out); err != nil {
		return nil, err
	}
	out.ID = id
	return &out, nil
}

// WaitForPurge polls until the purge is terminal and returns the final task.
// A "failed" status is returned as *TaskFailedError; a finished task with a
// successRate below 100 is returned without error so the caller can decide.
func (c *Client) WaitForPurge(ctx context.Context, id string, cfg PollConfig) (*PurgeTask, error) {
	var task *PurgeTask
	err := Poll(ctx, cfg, func(ctx context.Context) (bool, error) {
		t, err := c.GetPurge(ctx, id)
		if err != nil {
			return false, err
		}
		task = t
		switch Classify(KindPurge, t.Status) {
		case Pending:
			return false, nil
		case Failed:
			return false, &TaskFailedError{Kind: KindPurge, ID: id, Details: formatTaskErrors(t.Errors)}
		case Unknown:
			return false, fmt.Errorf("purge %s: unexpected status %q", id, t.Status)
		default:
			return true, nil
		}
	})
	return task, err
}

// CreatePrefetch submits a prefetch and returns its ID.
func (c *Client) CreatePrefetch(ctx context.Context, req PrefetchRequest) (string, error) {
	resp, err := c.Post(ctx, prefetchesPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", prefetchesPath)
	}
	return id, nil
}

// GetPrefetch fetches one prefetch task.
func (c *Client) GetPrefetch(ctx context.Context, id string) (*PrefetchTask, error) {
	var out PrefetchTask
	if _, err := c.Get(ctx, prefetchesPath+"/"+id, &out); err != nil {
		return nil, err
	}
	if out.MetaData.ID == "" {
		out.MetaData.ID = id
	}
	return &out, nil
}

// WaitForPrefetch polls until the prefetch is terminal, reading the status from
// metaData rather than the top level.
func (c *Client) WaitForPrefetch(ctx context.Context, id string, cfg PollConfig) (*PrefetchTask, error) {
	var task *PrefetchTask
	err := Poll(ctx, cfg, func(ctx context.Context) (bool, error) {
		t, err := c.GetPrefetch(ctx, id)
		if err != nil {
			return false, err
		}
		task = t
		switch Classify(KindPrefetch, t.MetaData.Status) {
		case Pending:
			return false, nil
		case Failed:
			return false, &TaskFailedError{Kind: KindPrefetch, ID: id, Details: formatTaskErrors(t.MetaData.Errors)}
		case Unknown:
			return false, fmt.Errorf("prefetch %s: unexpected status %q", id, t.MetaData.Status)
		default:
			return true, nil
		}
	})
	return task, err
}

func formatTaskErrors(errs []TaskError) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out += "; "
		}
		out += e.ErrorCode
		if e.ErrorMessage != "" {
			out += ": " + e.ErrorMessage
		}
	}
	return out
}
