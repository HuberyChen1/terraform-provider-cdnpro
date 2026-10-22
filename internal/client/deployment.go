package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Deployment targets.
const (
	TargetStaging    = "staging"
	TargetProduction = "production"
)

// Deployment action names.
const (
	ActionDeployProperty = "deploy_property"
	ActionRemoveProperty = "remove_property"
	ActionDeployCert     = "deploy_cert"
	ActionRemoveCert     = "remove_cert"
)

// IsPropertyAction reports whether action targets a property.
func IsPropertyAction(action string) bool {
	return action == ActionDeployProperty || action == ActionRemoveProperty
}

// IsDeployAction reports whether action deploys (as opposed to removes).
func IsDeployAction(action string) bool {
	return action == ActionDeployProperty || action == ActionDeployCert
}

// InverseAction returns the remove_* counterpart of a deploy_* action, or ""
// for remove_* actions (which have no safe inverse).
func InverseAction(action string) string {
	switch action {
	case ActionDeployProperty:
		return ActionRemoveProperty
	case ActionDeployCert:
		return ActionRemoveCert
	default:
		return ""
	}
}

// DeploymentAction is one entry of a deployment task's actions.
type DeploymentAction struct {
	Action        string `json:"action"`
	PropertyID    string `json:"propertyId,omitempty"`
	CertificateID string `json:"certificateId,omitempty"`
	// Version is sent as a JSON string; the API accepts a number or a numeric
	// string and stores whatever it received.
	Version *FlexibleInt `json:"version,omitempty"`
}

// FlexibleInt marshals as a string and unmarshals from a number or a string.
type FlexibleInt int64

func (f FlexibleInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(int64(f), 10))
}

func (f *FlexibleInt) UnmarshalJSON(b []byte) error {
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexibleInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("expected number or numeric string: %w", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return fmt.Errorf("expected numeric string, got %q", s)
	}
	*f = FlexibleInt(n)
	return nil
}

// DeploymentTaskRequest is the body of POST /cdn/deploymentTasks. Name is
// required by the API.
type DeploymentTaskRequest struct {
	Name    string             `json:"name"`
	Target  string             `json:"target"`
	Actions []DeploymentAction `json:"actions"`
	Webhook *string            `json:"webhook,omitempty"`
}

// DeploymentTask is the body of GET /cdn/deploymentTasks/{id}. The API does
// not echo the ID; it is filled in by the client.
type DeploymentTask struct {
	ID             string             `json:"-"`
	Name           string             `json:"name"`
	Target         string             `json:"target"`
	Actions        []DeploymentAction `json:"actions"`
	Status         string             `json:"status"`
	StatusDetails  string             `json:"statusDetails"`
	SubmissionTime string             `json:"submissionTime"`
	FinishTime     string             `json:"finishTime"`
	Webhook        string             `json:"webhook"`
}

// ValidationRequest is the body of POST /cdn/validations. Name is required.
type ValidationRequest struct {
	Name       string      `json:"name"`
	PropertyID string      `json:"propertyId"`
	Version    FlexibleInt `json:"version"`
}

// ValidationTask is the body of GET /cdn/validations/{id}.
type ValidationTask struct {
	ID             string `json:"-"`
	Name           string `json:"name"`
	PropertyID     string `json:"propertyId"`
	Version        int64  `json:"version"`
	Status         string `json:"status"`
	StatusDetails  string `json:"statusDetails"`
	SubmissionTime string `json:"submissionTime"`
	FinishTime     string `json:"finishTime"`
}

const (
	deploymentTasksPath = "/cdn/deploymentTasks"
	validationsPath     = "/cdn/validations"
)

// TaskFailedError is returned when an async task ends in "failed".
type TaskFailedError struct {
	Kind    TaskKind
	ID      string
	Details string
}

func (e *TaskFailedError) Error() string {
	msg := fmt.Sprintf("%s %s failed", e.Kind, e.ID)
	if e.Details != "" {
		msg += ": " + e.Details
	}
	return msg
}

// CreateDeploymentTask submits a deployment task and returns its ID.
func (c *Client) CreateDeploymentTask(ctx context.Context, req DeploymentTaskRequest) (string, error) {
	resp, err := c.Post(ctx, deploymentTasksPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", deploymentTasksPath)
	}
	return id, nil
}

// GetDeploymentTask fetches one deployment task.
func (c *Client) GetDeploymentTask(ctx context.Context, id string) (*DeploymentTask, error) {
	var out DeploymentTask
	if _, err := c.Get(ctx, deploymentTasksPath+"/"+id, &out); err != nil {
		return nil, err
	}
	out.ID = id
	return &out, nil
}

// WaitForDeploymentTask polls until the task is terminal. A failed task is
// returned as *TaskFailedError together with the final task.
func (c *Client) WaitForDeploymentTask(ctx context.Context, id string, cfg PollConfig) (*DeploymentTask, error) {
	var task *DeploymentTask
	err := Poll(ctx, cfg, func(ctx context.Context) (bool, error) {
		t, err := c.GetDeploymentTask(ctx, id)
		if err != nil {
			return false, err
		}
		task = t
		switch Classify(KindDeploymentTask, t.Status) {
		case Pending:
			return false, nil
		case Failed:
			return false, &TaskFailedError{Kind: KindDeploymentTask, ID: id, Details: t.StatusDetails}
		case Unknown:
			return false, fmt.Errorf("deployment task %s: unexpected status %q", id, t.Status)
		default:
			return true, nil
		}
	})
	return task, err
}

// CreateValidation submits a property version validation and returns its ID.
func (c *Client) CreateValidation(ctx context.Context, req ValidationRequest) (string, error) {
	resp, err := c.Post(ctx, validationsPath, req, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", validationsPath)
	}
	return id, nil
}

// GetValidation fetches one validation task.
func (c *Client) GetValidation(ctx context.Context, id string) (*ValidationTask, error) {
	var out ValidationTask
	if _, err := c.Get(ctx, validationsPath+"/"+id, &out); err != nil {
		return nil, err
	}
	out.ID = id
	return &out, nil
}

// WaitForValidation polls until the validation is terminal. Note the API spells
// the pending state "in progress" with a space.
func (c *Client) WaitForValidation(ctx context.Context, id string, cfg PollConfig) (*ValidationTask, error) {
	var task *ValidationTask
	err := Poll(ctx, cfg, func(ctx context.Context) (bool, error) {
		t, err := c.GetValidation(ctx, id)
		if err != nil {
			return false, err
		}
		task = t
		switch Classify(KindValidation, t.Status) {
		case Pending:
			return false, nil
		case Failed:
			return false, &TaskFailedError{Kind: KindValidation, ID: id, Details: t.StatusDetails}
		case Unknown:
			return false, fmt.Errorf("validation %s: unexpected status %q", id, t.Status)
		default:
			return true, nil
		}
	})
	return task, err
}

// ValidatePropertyVersion submits a validation for the given property version
// and waits for it to finish.
func (c *Client) ValidatePropertyVersion(ctx context.Context, name, propertyID string, version int64, cfg PollConfig) (*ValidationTask, error) {
	id, err := c.CreateValidation(ctx, ValidationRequest{Name: name, PropertyID: propertyID, Version: FlexibleInt(version)})
	if err != nil {
		return nil, err
	}
	return c.WaitForValidation(ctx, id, cfg)
}
