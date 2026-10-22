package client

import (
	"errors"
	"fmt"
	"net/http"
)

// Error codes the provider handles specially. Every other code is passed through
// to the Terraform diagnostic verbatim.
const (
	CodePreconditionFailed       = "PreconditionFailed"
	CodeTooManyVersions          = "TooManyVersions"
	CodeInvalidCertVersion       = "InvalidCertVersion"
	CodeOutdatedSecret           = "OutdatedSecret"
	CodeDuplicateDeployment      = "DuplicateDeployment"
	CodeEdgeHostnameBeingDeploy  = "EdgeHostnameBeingDeployed"
	CodeEdgeHostnameChangeForbid = "EdgeHostnameChangeForbidden"
	CodeFrozenPropertyVersion    = "FrozenPropertyVersion"
)

// APIError is a non-2xx response from the CDN Pro API. Code and Message are the
// API's own {"code": ..., "message": ...} body, kept verbatim.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Method     string
	Path       string
	RequestID  string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s %s: HTTP %d %s: %s", e.Method, e.Path, e.StatusCode, e.Code, e.Message)
}

// AsAPIError unwraps err into an *APIError, or returns nil.
func AsAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}

// IsNotFound reports whether err is an API 404. Resources treat this on Read as
// "deleted out of band" and remove themselves from state.
func IsNotFound(err error) bool {
	e := AsAPIError(err)
	return e != nil && e.StatusCode == http.StatusNotFound
}

// IsPreconditionFailed reports whether err is the 412 raised when an
// If-Unmodified-Since header is older than the resource's Last-Modified.
func IsPreconditionFailed(err error) bool {
	e := AsAPIError(err)
	return e != nil && (e.StatusCode == http.StatusPreconditionFailed || e.Code == CodePreconditionFailed)
}

// HasCode reports whether err is an API error carrying the given code.
func HasCode(err error, code string) bool {
	e := AsAPIError(err)
	return e != nil && e.Code == code
}
