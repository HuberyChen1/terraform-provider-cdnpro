package provider

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

// Phase 2 endpoints of the mock API: secrets, webhooks, log configs.

type mockSecret struct {
	name, description, secret string
	usedBy                    []string
	lastUpdate                time.Time
}

type mockWebhook struct {
	name, description, url string
	credentials            map[string]any // nil when none
	totalCalls             int64
	lastUpdate             time.Time
}

type mockLogConfig struct {
	description string
	hostnames   []string
	format      string
	storageDays int64
	spanMinutes int64
	lastUpdate  time.Time
}

var secretNameRe = regexp.MustCompile(`^[0-9a-zA-Z_.-]+$`)

func maskSecret(s string) string {
	n := len(s)
	switch {
	case n == 0:
		return ""
	case n < 20:
		keep := 2
		if n < 4 {
			keep = 0
		}
		return s[:keep] + "****" + s[n-keep:]
	case n <= 1000:
		keep := n * 10 / 100
		return s[:keep] + "****" + s[n-keep:]
	default:
		return s[:100] + "****" + s[n-100:]
	}
}

func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// ---- secrets ----------------------------------------------------------------

func validSecretValue(s string) bool {
	if len(s) < 8 || len(s) >= 3600 {
		return false
	}
	for _, c := range s {
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func (m *mockAPI) handleSecrets(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodPost {
			apiError(w, 405, "MethodNotAllowed", "")
			return
		}
		body, _ := readBody(r)
		name, _ := body["name"].(string)
		secret, _ := body["secret"].(string)
		desc, _ := body["description"].(string)
		if name == "" || len(name) > 30 || !secretNameRe.MatchString(name) {
			apiError(w, 400, "InvalidName", "invalid name")
			return
		}
		if !validSecretValue(secret) {
			apiError(w, 400, "InvalidSecretValue", "invalid secret")
			return
		}
		for _, s := range m.secrets {
			if s.name == name {
				apiError(w, 409, "DuplicateName", "name in use")
				return
			}
		}
		id := m.nextID("sec")
		m.secrets[id] = &mockSecret{name: name, description: desc, secret: secret, lastUpdate: time.Now()}
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	}
	s, ok := m.secrets[rest[0]]
	if !ok {
		apiError(w, 404, "InvalidSecretId", "secret not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Last-Modified", httpDate(s.lastUpdate))
		out := map[string]any{
			"id": rest[0], "name": s.name, "description": s.description, "secret": maskSecret(s.secret),
			"ownerCustomerId": 1, "creationTime": "2026-01-01T00:00:00Z", "lastUpdateTime": isoTime(s.lastUpdate),
		}
		if len(s.usedBy) > 0 {
			usage := []map[string]any{}
			for _, p := range s.usedBy {
				usage = append(usage, map[string]any{"propertyId": p})
			}
			out["usageInStaging"] = usage
		}
		writeJSON(w, 200, out)
	case http.MethodPatch:
		if r.Header.Get("Content-Type") != "application/json" {
			apiError(w, 415, "UnsupportedMediaType", "PATCH requires application/json")
			return
		}
		if !preconditionOK(w, r, s.lastUpdate) {
			return
		}
		body, _ := readBody(r)
		if v, present := body["name"]; present {
			name, _ := v.(string)
			if name == "" || len(name) > 30 || !secretNameRe.MatchString(name) {
				apiError(w, 400, "InvalidName", "invalid name")
				return
			}
			for id, other := range m.secrets {
				if id != rest[0] && other.name == name {
					apiError(w, 409, "DuplicateName", "name in use")
					return
				}
			}
			s.name = name
		}
		if v, present := body["description"]; present {
			s.description, _ = v.(string)
		}
		if v, present := body["secret"]; present {
			secret, _ := v.(string)
			if !validSecretValue(secret) {
				apiError(w, 400, "InvalidSecretValue", "invalid secret")
				return
			}
			s.secret = secret
		}
		s.lastUpdate = time.Now()
		w.WriteHeader(204)
	case http.MethodDelete:
		if len(s.usedBy) > 0 {
			apiError(w, 409, "SecretInUse", "secret is referenced by a deployed property")
			return
		}
		delete(m.secrets, rest[0])
		w.WriteHeader(204)
	default:
		apiError(w, 405, "MethodNotAllowed", "")
	}
}

// ---- webhooks ---------------------------------------------------------------

func (m *mockAPI) handleWebhooks(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodPost {
			apiError(w, 405, "MethodNotAllowed", "")
			return
		}
		body, _ := readBody(r)
		name, _ := body["name"].(string)
		url, _ := body["url"].(string)
		desc, _ := body["description"].(string)
		if name == "" || len(name) > 250 {
			apiError(w, 400, "InvalidName", "invalid name")
			return
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			apiError(w, 400, "InvalidURL", "invalid url")
			return
		}
		wh := &mockWebhook{name: name, description: desc, url: url, lastUpdate: time.Now()}
		if c, ok := body["credentials"].(map[string]any); ok {
			user, _ := c["user"].(string)
			key, _ := c["secretKey"].(string)
			if (user == "") != (key == "") || strings.ContainsAny(user+key, " ") {
				apiError(w, 400, "InvalidCredential", "user and secretKey must be set together")
				return
			}
			if user != "" {
				wh.credentials = map[string]any{"user": user, "secretKey": key}
			}
		}
		id := m.nextID("wh")
		m.webhooks[id] = wh
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	}
	wh, ok := m.webhooks[rest[0]]
	if !ok {
		apiError(w, 404, "InvalidWebhookId", "webhook not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Last-Modified", httpDate(wh.lastUpdate))
		out := map[string]any{
			"name": wh.name, "description": wh.description, "url": wh.url,
			"metaData": map[string]any{
				"creationTime": "2026-01-01T00:00:00Z", "lastUpdateTime": isoTime(wh.lastUpdate),
				"lastCallTime": "", "totalCalls": wh.totalCalls,
			},
		}
		if wh.credentials != nil {
			out["credentials"] = wh.credentials // plaintext, as the real API does
		}
		writeJSON(w, 200, out)
	case http.MethodPatch:
		if !preconditionOK(w, r, wh.lastUpdate) {
			return
		}
		body, _ := readBody(r)
		if v, present := body["name"]; present {
			name, _ := v.(string)
			if name == "" {
				apiError(w, 400, "InvalidName", "invalid name")
				return
			}
			wh.name = name
		}
		if v, present := body["url"]; present {
			url, _ := v.(string)
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				apiError(w, 400, "InvalidURL", "invalid url")
				return
			}
			wh.url = url
		}
		if v, present := body["description"]; present {
			wh.description, _ = v.(string)
		}
		if c, ok := body["credentials"].(map[string]any); ok {
			// Merge field by field; "credentials": null is ignored, like the real API.
			if wh.credentials == nil {
				user, _ := c["user"].(string)
				key, _ := c["secretKey"].(string)
				if user == "" || key == "" {
					apiError(w, 400, "InvalidCredential", "both fields required when adding credentials")
					return
				}
				wh.credentials = map[string]any{}
			}
			for _, k := range []string{"user", "secretKey"} {
				if val, ok := c[k].(string); ok {
					wh.credentials[k] = val
				}
			}
		}
		wh.lastUpdate = time.Now()
		w.WriteHeader(204)
	case http.MethodDelete:
		delete(m.webhooks, rest[0])
		w.WriteHeader(204)
	default:
		apiError(w, 405, "MethodNotAllowed", "")
	}
}

// ---- log configs ------------------------------------------------------------

var allowedSpans = map[int64]bool{5: true, 15: true, 30: true, 60: true, 120: true, 240: true, 480: true, 1440: true}

func (m *mockAPI) applyLogConfigPatch(lc *mockLogConfig, body map[string]any, isCreate bool) (string, string) {
	if v, present := body["description"]; present || isCreate {
		lc.description, _ = v.(string)
	}
	if v, present := body["hostnames"]; present || isCreate {
		list, _ := v.([]any)
		if len(list) == 0 {
			return "InvalidHostname", "hostnames must not be empty"
		}
		lc.hostnames = nil
		for _, h := range list {
			lc.hostnames = append(lc.hostnames, h.(string))
		}
	}
	if v, present := body["logDownloadFormat"]; present || isCreate {
		s, _ := v.(string)
		if s == "" {
			s = client.DefaultLogDownloadFormat
		}
		lc.format = s
	}
	if v, present := body["logDownloadStorageDays"]; present || isCreate {
		f, ok := v.(float64)
		if !ok {
			f = float64(client.DefaultLogDownloadStorageDays)
		}
		if f < 1 || f > 30 {
			return "InvalidStorageDays", "logDownloadStorageDays must be 1..30"
		}
		lc.storageDays = int64(f)
	}
	if v, present := body["logDownloadFileSpanMinutes"]; present || isCreate {
		f, ok := v.(float64)
		if !ok {
			f = float64(client.DefaultLogDownloadFileSpanMinutes)
		}
		if !allowedSpans[int64(f)] {
			return "InvalidFileSpanMinutes", "unsupported span"
		}
		lc.spanMinutes = int64(f)
	}
	return "", ""
}

func (m *mockAPI) handleLogConfigs(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodPost {
			apiError(w, 405, "MethodNotAllowed", "")
			return
		}
		body, _ := readBody(r)
		lc := &mockLogConfig{lastUpdate: time.Now()}
		if code, msg := m.applyLogConfigPatch(lc, body, true); code != "" {
			apiError(w, 400, code, msg)
			return
		}
		m.seq++
		id := strconv.Itoa(4000 + m.seq)
		m.logConfigs[id] = lc
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	}
	lc, ok := m.logConfigs[rest[0]]
	if !ok {
		apiError(w, 404, "InvalidLogConfigId", "log config not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Last-Modified", httpDate(lc.lastUpdate))
		idNum, _ := strconv.Atoi(rest[0])
		writeJSON(w, 200, map[string]any{
			"id": json.Number(strconv.Itoa(idNum)),
			"configs": map[string]any{
				"description": lc.description, "hostnames": lc.hostnames, "logDownloadFormat": lc.format,
				"logDownloadStorageDays": lc.storageDays, "logDownloadFileSpanMinutes": lc.spanMinutes,
				"creationTime": "2026-01-01T00:00:00.000+00:00", "lastUpdateTime": lc.lastUpdate.UTC().Format("2006-01-02T15:04:05.000+00:00"),
			},
			"creationTime": "2026-01-01T00:00:00Z", "lastUpdateTime": isoTime(lc.lastUpdate),
		})
	case http.MethodPatch:
		if !preconditionOK(w, r, lc.lastUpdate) {
			return
		}
		body, _ := readBody(r)
		if code, msg := m.applyLogConfigPatch(lc, body, false); code != "" {
			apiError(w, 400, code, msg)
			return
		}
		lc.lastUpdate = time.Now()
		w.WriteHeader(204)
	case http.MethodDelete:
		delete(m.logConfigs, rest[0])
		w.WriteHeader(204)
	default:
		apiError(w, 405, "MethodNotAllowed", "")
	}
}
