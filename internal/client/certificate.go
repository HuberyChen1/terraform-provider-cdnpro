package client

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// CertificateRequest is the body of POST /cdn/certificates and of PATCH
// /cdn/certificates/{id}. NewVersion.PrivateKey must be the plaintext PEM (or a
// reserved keyword); the client encrypts it against the request date.
type CertificateRequest struct {
	Name        *string      `json:"name,omitempty"`
	Description *string      `json:"description,omitempty"`
	AutoRenew   *string      `json:"autoRenew,omitempty"`
	ForceRenew  *bool        `json:"forceRenew,omitempty"`
	NewVersion  *CertVersion `json:"newVersion,omitempty"`
}

// CertVersion is CertificateRequest.NewVersion.
type CertVersion struct {
	Comments           *string             `json:"comments,omitempty"`
	PrivateKey         *string             `json:"privateKey,omitempty"`
	Certificate        *string             `json:"certificate,omitempty"`
	ChainCert          *string             `json:"chainCert,omitempty"`
	IdentificationInfo *IdentificationInfo `json:"identificationInfo,omitempty"`
}

// IdentificationInfo is the subject of a generated self-signed certificate.
type IdentificationInfo struct {
	Country                 *string  `json:"country,omitempty"`
	State                   *string  `json:"state,omitempty"`
	City                    *string  `json:"city,omitempty"`
	Company                 *string  `json:"company,omitempty"`
	Department              *string  `json:"department,omitempty"`
	CommonName              *string  `json:"commonName,omitempty"`
	Email                   *string  `json:"email,omitempty"`
	SubjectAlternativeNames []string `json:"subjectAlternativeNames,omitempty"`
}

// CertificateSummary is the body of GET /cdn/certificates/{id}.
type CertificateSummary struct {
	CertificateID       string               `json:"certificateId"`
	Name                string               `json:"name"`
	Description         string               `json:"description"`
	AutoRenew           string               `json:"autoRenew"`
	ForceRenew          bool                 `json:"forceRenew"`
	CAOnly              bool                 `json:"caOnly"`
	VersionInProduction *int64               `json:"versionInProduction"`
	VersionInStaging    *int64               `json:"versionInStaging"`
	UsageInProduction   []CertificateUsage   `json:"usageInProduction"`
	UsageInStaging      []CertificateUsage   `json:"usageInStaging"`
	Versions            []CertVersionSummary `json:"versions"`
}

// LatestVersion returns the highest version number listed in Versions, or 0.
func (s *CertificateSummary) LatestVersion() int64 {
	var latest int64
	for _, v := range s.Versions {
		if v.Version > latest {
			latest = v.Version
		}
	}
	return latest
}

// CertificateUsage is one entry of usageInProduction / usageInStaging.
type CertificateUsage struct {
	CustomerID int64    `json:"customerId"`
	PropertyID string   `json:"propertyId"`
	Hostnames  []string `json:"hostnames"`
	Origins    []string `json:"origins"`
}

// CertVersionSummary is one entry of CertificateSummary.Versions.
type CertVersionSummary struct {
	Version        int64  `json:"version"`
	Type           string `json:"type"`
	ExpirationTime string `json:"expirationTime"`
	CreationTime   string `json:"creationTime"`
	Fingerprint    string `json:"fingerprint"`
	Comments       string `json:"comments"`
}

// CertVersionDetail is the body of GET /cdn/certificates/{id}/versions/{n}.
// PEM material is never present.
type CertVersionDetail struct {
	CertificateID           string             `json:"certificateId"`
	Name                    string             `json:"name"`
	Version                 int64              `json:"version"`
	Type                    string             `json:"type"`
	ExpirationTime          string             `json:"expirationTime"`
	CreationTime            string             `json:"creationTime"`
	Subject                 string             `json:"subject"`
	Issuer                  string             `json:"issuer"`
	SignatureAlgo           string             `json:"signatureAlgo"`
	SerialNumber            string             `json:"serialNumber"`
	Fingerprint             string             `json:"fingerprint"`
	Comments                string             `json:"comments"`
	Algorithm               string             `json:"algorithm"`
	KeyLength               int64              `json:"keyLength"`
	SubjectAlternativeNames []string           `json:"subjectAlternativeNames"`
	ChainCertificates       []ChainCertSummary `json:"chainCertificates"`
	InProduction            bool               `json:"inProduction"`
	InStaging               bool               `json:"inStaging"`
}

// ChainCertSummary is one entry of CertVersionDetail.ChainCertificates.
type ChainCertSummary struct {
	Subject        string `json:"subject"`
	Issuer         string `json:"issuer"`
	ExpirationTime string `json:"expirationTime"`
	SignatureAlgo  string `json:"signatureAlgo"`
}

const certificatesPath = "/cdn/certificates"

func certificatePath(id string) string { return certificatesPath + "/" + id }

// encryptNewVersion returns a copy of req with NewVersion.PrivateKey encrypted
// for the given request date.
func (c *Client) encryptNewVersion(req CertificateRequest, date string) (CertificateRequest, error) {
	if req.NewVersion == nil || req.NewVersion.PrivateKey == nil {
		return req, nil
	}
	encrypted, err := EncryptPrivateKey(*req.NewVersion.PrivateKey, c.apiKey, date)
	if err != nil {
		return req, fmt.Errorf("encrypt private key: %w", err)
	}
	nv := *req.NewVersion
	nv.PrivateKey = &encrypted
	req.NewVersion = &nv
	return req, nil
}

// CreateCertificate creates a certificate (version 1) and returns the new ID.
func (c *Client) CreateCertificate(ctx context.Context, req CertificateRequest) (string, error) {
	date := c.Date()
	body, err := c.encryptNewVersion(req, date)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(ctx, Request{Method: http.MethodPost, Path: certificatesPath, Body: body, Date: date}, nil)
	if err != nil {
		return "", err
	}
	id := resp.LastID()
	if id == "" {
		return "", fmt.Errorf("POST %s: response has no Location header", certificatesPath)
	}
	return id, nil
}

// GetCertificate returns the certificate summary and its Last-Modified header.
func (c *Client) GetCertificate(ctx context.Context, id string) (*CertificateSummary, string, error) {
	var out CertificateSummary
	resp, err := c.Get(ctx, certificatePath(id), &out)
	if err != nil {
		return nil, "", err
	}
	return &out, resp.LastModified(), nil
}

// GetCertificateVersion returns one version's metadata. version may be a number
// or "latest".
func (c *Client) GetCertificateVersion(ctx context.Context, id, version string) (*CertVersionDetail, error) {
	var out CertVersionDetail
	if _, err := c.Get(ctx, certificatePath(id)+"/versions/"+version, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchCertificate updates metadata and, when req.NewVersion is set, mints a
// new version. It returns the new version number (0 when no version was
// created). lastModified is sent as If-Unmodified-Since when non-empty.
func (c *Client) PatchCertificate(ctx context.Context, id string, req CertificateRequest, lastModified string) (int64, error) {
	date := c.Date()
	body, err := c.encryptNewVersion(req, date)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(ctx, Request{
		Method:  http.MethodPatch,
		Path:    certificatePath(id),
		Body:    body,
		Headers: ifUnmodifiedSince(lastModified),
		Date:    date,
	}, nil)
	if err != nil {
		return 0, err
	}
	if req.NewVersion == nil {
		return 0, nil
	}
	n, convErr := strconv.ParseInt(resp.LastID(), 10, 64)
	if convErr != nil {
		return 0, fmt.Errorf("PATCH %s: cannot read new version number from Location %q", certificatePath(id), resp.Location())
	}
	return n, nil
}

// DeleteCertificate deletes a certificate. A 404 is treated as success.
func (c *Client) DeleteCertificate(ctx context.Context, id string) error {
	_, err := c.Delete(ctx, certificatePath(id), nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
