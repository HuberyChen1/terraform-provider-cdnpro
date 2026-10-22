package provider

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

// mockAPI is an in-memory stand-in for the CDN Pro API that implements the
// endpoints and quirks the provider relies on: HMAC Basic auth, Location
// headers, Last-Modified / If-Unmodified-Since, immutable property versions
// with frozen/validation status, async edge hostname history, and deployment /
// validation tasks that advance one state per GET.
type mockAPI struct {
	mu sync.Mutex

	user, key string
	server    *httptest.Server

	seq          int
	properties   map[string]*mockProperty
	certificates map[string]*mockCertificate
	edgeHosts    map[string]*mockEdgeHostname
	deployments  map[string]*mockTask
	validations  map[string]*mockTask
	secrets      map[string]*mockSecret
	webhooks     map[string]*mockWebhook
	logConfigs   map[string]*mockLogConfig
	purges       map[string]*mockTask
	prefetches   map[string]*mockTask

	// Counters the tests assert on.
	deploymentPosts int
	validationPosts int
	decryptedKeys   []string
	purgePosts      []map[string]any
	prefetchPosts   []map[string]any
	requests        []string
}

type mockProperty struct {
	name, description string
	versions          []*mockPropertyVersion
	inProduction      *int64
	inStaging         *int64
	lastUpdate        time.Time
}

type mockPropertyVersion struct {
	version              int64
	config               map[string]any
	frozen               bool
	lastValidationStatus string
}

type mockCertificate struct {
	name, description string
	autoRenew         string
	forceRenew        bool
	versions          []*mockCertVersion
	inProduction      *int64
	inStaging         *int64
	lastUpdate        time.Time
}

type mockCertVersion struct {
	version  int64
	comments string
	keyHash  string
	certHash string
}

type mockEdgeHostname struct {
	configs    map[string]any
	pending    map[string]any // config applied when the in-flight update succeeds
	history    []map[string]any
	ticks      int // GETs until the newest history entry becomes terminal
	deleting   bool
	lastUpdate time.Time
}

type mockTask struct {
	kind    string
	body    map[string]any
	status  string
	ticks   int
	applied bool
	created time.Time
	rate    float64 // successRate reported by purge/prefetch tasks
}

func newMockAPI() *mockAPI {
	m := &mockAPI{
		user:         "acc-user",
		key:          "acc-api-key-0123456789",
		properties:   map[string]*mockProperty{},
		certificates: map[string]*mockCertificate{},
		edgeHosts:    map[string]*mockEdgeHostname{},
		deployments:  map[string]*mockTask{},
		validations:  map[string]*mockTask{},
		secrets:      map[string]*mockSecret{},
		webhooks:     map[string]*mockWebhook{},
		logConfigs:   map[string]*mockLogConfig{},
		purges:       map[string]*mockTask{},
		prefetches:   map[string]*mockTask{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mockAPI) Close() { m.server.Close() }

func (m *mockAPI) nextID(prefix string) string {
	m.seq++
	return fmt.Sprintf("%s%d", prefix, m.seq)
}

// ---- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func apiError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "message": msg})
}

func httpDate(t time.Time) string { return t.UTC().Truncate(time.Second).Format(http.TimeFormat) }

func readBody(r *http.Request) (map[string]any, error) {
	var body map[string]any
	if r.Body == nil {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

func location(r *http.Request, suffix string) string {
	return "http://" + r.Host + strings.TrimRight(r.URL.Path, "/") + "/" + suffix
}

// preconditionOK implements If-Unmodified-Since with second granularity.
func preconditionOK(w http.ResponseWriter, r *http.Request, lastUpdate time.Time) bool {
	hdr := r.Header.Get(client.HeaderIfUnmodifiedSince)
	if hdr == "" {
		return true
	}
	t, err := http.ParseTime(hdr)
	if err != nil {
		return true
	}
	if lastUpdate.Unix() > t.Unix() {
		apiError(w, http.StatusPreconditionFailed, "PreconditionFailed", "The resource has been modified since "+hdr+".")
		return false
	}
	return true
}

func (m *mockAPI) authorized(w http.ResponseWriter, r *http.Request) bool {
	date := r.Header.Get("Date")
	auth := r.Header.Get("Authorization")
	if date == "" || !strings.HasPrefix(auth, "Basic ") {
		apiError(w, http.StatusUnauthorized, "Unauthorized", "missing Date or Authorization")
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
	if err != nil {
		apiError(w, http.StatusUnauthorized, "Unauthorized", "bad base64")
		return false
	}
	user, pw, _ := strings.Cut(string(raw), ":")
	if user != m.user || pw != client.SignPassword(m.key, date) {
		apiError(w, http.StatusUnauthorized, "Unauthorized", "bad signature")
		return false
	}
	return true
}

// ---- router -----------------------------------------------------------------

func (m *mockAPI) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, r.Method+" "+r.URL.Path)
	if !m.authorized(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "cdn" {
		apiError(w, 404, "NotFound", "unknown path")
		return
	}
	rest := parts[2:]
	switch parts[1] {
	case "properties":
		m.handleProperties(w, r, rest)
	case "certificates":
		m.handleCertificates(w, r, rest)
	case "edgeHostnames":
		m.handleEdgeHostnames(w, r, rest)
	case "deploymentTasks":
		m.handleDeployments(w, r, rest)
	case "validations":
		m.handleValidations(w, r, rest)
	case "secrets":
		m.handleSecrets(w, r, rest)
	case "webhooks":
		m.handleWebhooks(w, r, rest)
	case "purges":
		m.handlePurges(w, r, rest)
	case "prefetches":
		m.handlePrefetches(w, r, rest)
	case "hostnames":
		m.handleHostnames(w, r, rest)
	case "shields":
		m.handleShields(w, r, rest)
	case "isps":
		m.handleISPs(w, r, rest)
	case "clientRegions":
		m.handleClientRegions(w, r, rest)
	case "report":
		if len(rest) > 0 && rest[0] == "logConfigs" {
			m.handleLogConfigs(w, r, rest[1:])
			return
		}
		apiError(w, 404, "NotFound", "unknown path")
	default:
		apiError(w, 404, "NotFound", "unknown path")
	}
}

// ---- properties -------------------------------------------------------------

func applyPropertyDefaults(cfg map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		if v != nil {
			out[k] = v
		}
	}
	defaults := map[string]any{
		"syntaxVersion": float64(1), "tlsSessionTimeout": float64(1800), "cacheKeyUri": "preRewrite",
		"tlsMinVersion": "1", "tlsMaxVersion": "1.3", "followClientIpVersion": "off",
		"hasBeian": false, "enableIpv6Origin": false, "schemeInCacheKey": false, "allowProtocolDowngrade": false,
		"redirectHttpToHttps": false, "disableHttp2": false, "enableHttp3": false, "enableOcspStapling": false,
		"tls0Rtt": false, "disableCertAutomation": false,
	}
	for k, v := range defaults {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	if origins, ok := out["origins"].([]any); ok {
		for _, o := range origins {
			om, _ := o.(map[string]any)
			for k, v := range map[string]any{
				"supportedProtocol": "both", "directConnection": "auto", "peerSelectionAlgorithm": "round_robin",
				"keepAliveTimeout": float64(60), "maxConnections": float64(0), "verifyOrigin": false,
				"peerFailureTimeout": map[string]any{"failureThreshold": float64(6), "timeout": float64(1)},
			} {
				if _, ok := om[k]; !ok {
					om[k] = v
				}
			}
		}
	}
	return out
}

func (p *mockProperty) latest() *mockPropertyVersion { return p.versions[len(p.versions)-1] }

func (p *mockProperty) find(v string) *mockPropertyVersion {
	if v == "latest" {
		return p.latest()
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil
	}
	for _, pv := range p.versions {
		if pv.version == n {
			return pv
		}
	}
	return nil
}

func (m *mockAPI) handleProperties(w http.ResponseWriter, r *http.Request, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			apiError(w, 400, "InvalidInput", err.Error())
			return
		}
		name, _ := body["name"].(string)
		for _, p := range m.properties {
			if p.name == name {
				apiError(w, 409, "DuplicatePropertyName", "name in use")
				return
			}
		}
		cfg, _ := body["version"].(map[string]any)
		if cfg == nil {
			apiError(w, 400, "InvalidInput", "version is required")
			return
		}
		id := m.nextID("prop")
		desc, _ := body["description"].(string)
		m.properties[id] = &mockProperty{
			name: name, description: desc,
			versions:   []*mockPropertyVersion{{version: 1, config: applyPropertyDefaults(cfg), lastValidationStatus: "NotValidated"}},
			lastUpdate: time.Now(),
		}
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	case len(rest) == 0:
		apiError(w, 405, "MethodNotAllowed", "")
		return
	}

	p, ok := m.properties[rest[0]]
	if !ok {
		apiError(w, 404, "InvalidPropertyId", "property not found")
		return
	}
	switch {
	case len(rest) == 1 && r.Method == http.MethodGet:
		w.Header().Set("Last-Modified", httpDate(p.lastUpdate))
		latest := p.latest().version
		writeJSON(w, 200, map[string]any{
			"name": p.name, "description": p.description,
			"versions": len(p.versions), "latestVersion": latest,
			"versionInProduction": p.inProduction, "versionInStaging": p.inStaging,
			"creationTime": "2026-01-01T00:00:00Z", "lastUpdateTime": p.lastUpdate.UTC().Format("2006-01-02T15:04:05Z"),
		})
	case len(rest) == 1 && r.Method == http.MethodPatch:
		if !preconditionOK(w, r, p.lastUpdate) {
			return
		}
		body, _ := readBody(r)
		if v, ok := body["name"].(string); ok {
			p.name = v
		}
		if v, ok := body["description"].(string); ok {
			p.description = v
		}
		p.lastUpdate = time.Now()
		w.WriteHeader(200)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if p.inProduction != nil || p.inStaging != nil {
			apiError(w, 409, "PropertyInUse", "property is deployed")
			return
		}
		delete(m.properties, rest[0])
		w.WriteHeader(204)
	case len(rest) == 2 && rest[1] == "versions" && r.Method == http.MethodPost:
		body, _ := readBody(r)
		n := p.latest().version + 1
		p.versions = append(p.versions, &mockPropertyVersion{version: n, config: applyPropertyDefaults(body), lastValidationStatus: "NotValidated"})
		p.lastUpdate = time.Now()
		w.Header().Set("Location", location(r, strconv.FormatInt(n, 10)))
		w.WriteHeader(201)
	case len(rest) == 3 && rest[1] == "versions" && r.Method == http.MethodGet:
		pv := p.find(rest[2])
		if pv == nil {
			apiError(w, 404, "InvalidVersion", "version not found")
			return
		}
		w.Header().Set("Last-Modified", httpDate(p.lastUpdate))
		writeJSON(w, 200, map[string]any{
			"version": pv.version, "configs": pv.config,
			"status": map[string]any{"frozen": pv.frozen, "lastValidationStatus": pv.lastValidationStatus},
		})
	case len(rest) == 3 && rest[1] == "versions" && r.Method == http.MethodPatch:
		pv := p.find(rest[2])
		if pv == nil {
			apiError(w, 404, "InvalidVersion", "version not found")
			return
		}
		if !preconditionOK(w, r, p.lastUpdate) {
			return
		}
		body, _ := readBody(r)
		if pv.frozen {
			for k := range body {
				if k != "description" {
					apiError(w, 400, "FrozenPropertyVersion", "version is frozen")
					return
				}
			}
		}
		merged := map[string]any{}
		for k, v := range pv.config {
			merged[k] = v
		}
		for k, v := range body {
			if v == nil {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		pv.config = applyPropertyDefaults(merged)
		pv.lastValidationStatus = "NotValidated"
		p.lastUpdate = time.Now()
		w.WriteHeader(204)
	default:
		apiError(w, 404, "NotFound", "unknown property path")
	}
}

// ---- certificates -----------------------------------------------------------

func (c *mockCertificate) latest() *mockCertVersion { return c.versions[len(c.versions)-1] }

func (m *mockAPI) certVersionFromBody(w http.ResponseWriter, r *http.Request, nv map[string]any, n int64) (*mockCertVersion, bool) {
	pk, _ := nv["privateKey"].(string)
	cert, _ := nv["certificate"].(string)
	comments, _ := nv["comments"].(string)
	if pk == "" || cert == "" {
		apiError(w, 400, "InvalidData", "privateKey and certificate are required")
		return nil, false
	}
	if !client.IsPrivateKeyKeyword(pk) {
		plain, err := client.DecryptPrivateKey(pk, m.key, r.Header.Get("Date"))
		if err != nil || !strings.Contains(plain, "PRIVATE KEY") {
			apiError(w, 400, "InvalidKey", "cannot decrypt private key")
			return nil, false
		}
		m.decryptedKeys = append(m.decryptedKeys, plain)
		pk = plain
	} else if cert != "GENERATE" {
		apiError(w, 400, "InvalidCert", "keyword private key requires GENERATE")
		return nil, false
	} else if _, ok := nv["identificationInfo"].(map[string]any); !ok {
		apiError(w, 400, "InvalidIdentificationInfo", "identificationInfo required")
		return nil, false
	}
	return &mockCertVersion{version: n, comments: comments, keyHash: sha256Hex(pk), certHash: sha256Hex(cert)}, true
}

func (m *mockAPI) handleCertificates(w http.ResponseWriter, r *http.Request, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			apiError(w, 400, "InvalidInput", err.Error())
			return
		}
		nv, _ := body["newVersion"].(map[string]any)
		if nv == nil {
			apiError(w, 400, "InvalidInput", "newVersion required")
			return
		}
		v, ok := m.certVersionFromBody(w, r, nv, 1)
		if !ok {
			return
		}
		id := m.nextID("cert")
		name, _ := body["name"].(string)
		desc, _ := body["description"].(string)
		autoRenew, _ := body["autoRenew"].(string)
		if autoRenew == "" {
			autoRenew = "Off"
		}
		if strings.EqualFold(autoRenew, "off") {
			autoRenew = "Off"
		}
		force, _ := body["forceRenew"].(bool)
		m.certificates[id] = &mockCertificate{name: name, description: desc, autoRenew: autoRenew, forceRenew: force,
			versions: []*mockCertVersion{v}, lastUpdate: time.Now()}
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	case len(rest) == 0:
		apiError(w, 405, "MethodNotAllowed", "")
		return
	}
	c, ok := m.certificates[rest[0]]
	if !ok {
		apiError(w, 404, "InvalidCertId", "certificate not found")
		return
	}
	switch {
	case len(rest) == 1 && r.Method == http.MethodGet:
		w.Header().Set("Last-Modified", httpDate(c.lastUpdate))
		versions := []map[string]any{}
		for _, v := range c.versions {
			versions = append(versions, map[string]any{"version": v.version, "type": "uploaded", "comments": v.comments,
				"expirationTime": "2027-01-01T00:00:00Z", "creationTime": "2026-01-01T00:00:00Z", "fingerprint": v.certHash[:16]})
		}
		writeJSON(w, 200, map[string]any{
			"certificateId": rest[0], "name": c.name, "description": c.description, "autoRenew": c.autoRenew,
			"forceRenew": c.forceRenew, "caOnly": false, "versionInProduction": c.inProduction, "versionInStaging": c.inStaging,
			"usageInProduction": []any{}, "usageInStaging": []any{}, "versions": versions,
		})
	case len(rest) == 1 && r.Method == http.MethodPatch:
		if !preconditionOK(w, r, c.lastUpdate) {
			return
		}
		body, _ := readBody(r)
		if v, ok := body["name"].(string); ok {
			c.name = v
		}
		if v, ok := body["description"].(string); ok {
			c.description = v
		}
		if v, ok := body["autoRenew"].(string); ok {
			if strings.EqualFold(v, "off") {
				v = "Off"
			}
			c.autoRenew = v
		}
		if v, ok := body["forceRenew"].(bool); ok {
			c.forceRenew = v
		}
		c.lastUpdate = time.Now()
		if nv, ok := body["newVersion"].(map[string]any); ok {
			n := c.latest().version + 1
			v, ok := m.certVersionFromBody(w, r, nv, n)
			if !ok {
				return
			}
			c.versions = append(c.versions, v)
			w.Header().Set("Location", location(r, "versions/"+strconv.FormatInt(n, 10)))
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(204)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if c.inProduction != nil || c.inStaging != nil {
			apiError(w, 409, "CertificateInUse", "certificate is deployed")
			return
		}
		delete(m.certificates, rest[0])
		w.WriteHeader(204)
	case len(rest) == 3 && rest[1] == "versions" && r.Method == http.MethodGet:
		var v *mockCertVersion
		if rest[2] == "latest" {
			v = c.latest()
		} else {
			n, _ := strconv.ParseInt(rest[2], 10, 64)
			for _, cv := range c.versions {
				if cv.version == n {
					v = cv
				}
			}
		}
		if v == nil {
			apiError(w, 404, "InvalidCertVersion", "version not found")
			return
		}
		w.Header().Set("Last-Modified", httpDate(c.lastUpdate))
		writeJSON(w, 200, map[string]any{
			"certificateId": rest[0], "name": c.name, "version": v.version, "type": "uploaded",
			"expirationTime": "2027-01-01T00:00:00Z", "creationTime": "2026-01-01T00:00:00Z",
			"subject": "CN=example.com", "issuer": "CN=Test CA", "signatureAlgo": "SHA256withRSA",
			"serialNumber": "0a1b", "fingerprint": v.certHash[:16], "comments": v.comments,
			"algorithm": "RSA", "keyLength": 2048, "subjectAlternativeNames": []string{"example.com"},
			"chainCertificates": []map[string]any{{"subject": "CN=Test CA", "issuer": "CN=Root", "expirationTime": "2030-01-01T00:00:00Z", "signatureAlgo": "SHA256withRSA"}},
			"inProduction":      c.inProduction != nil && *c.inProduction == v.version,
			"inStaging":         c.inStaging != nil && *c.inStaging == v.version,
		})
	default:
		apiError(w, 404, "NotFound", "unknown certificate path")
	}
}

// ---- edge hostnames ---------------------------------------------------------

func (e *mockEdgeHostname) tick() {
	if len(e.history) == 0 || e.ticks <= 0 {
		return
	}
	e.ticks--
	if e.ticks > 0 {
		e.history[0]["status"] = "inprogress"
		return
	}
	e.history[0]["status"] = "succeeded"
	e.history[0]["finishTime"] = "2026-01-01T00:00:10Z"
	if e.pending != nil {
		e.configs = e.pending
		e.pending = nil
	}
}

func (e *mockEdgeHostname) inFlight() bool {
	return len(e.history) > 0 && (e.history[0]["status"] == "waiting" || e.history[0]["status"] == "inprogress")
}

func (e *mockEdgeHostname) push(op string, cfg map[string]any) {
	ev := map[string]any{"operation": op, "status": "waiting", "statusDetails": "", "submissionTime": "2026-01-01T00:00:00Z",
		"finishTime": "", "time": "2026-01-01T00:00:00Z", "apiName": "acc-user", "apiAccountId": "1", "apiRequestId": "req", "configuration": cfg}
	e.history = append([]map[string]any{ev}, e.history...)
	e.ticks = 2
}

func normalizeEdgeConfig(body map[string]any) map[string]any {
	cfg := map[string]any{}
	for _, k := range []string{"edgeHostname", "description", "estimatedBandwidth", "clientZones", "deliverByHttps"} {
		if v, ok := body[k]; ok && v != nil {
			cfg[k] = v
		}
	}
	if _, ok := cfg["description"]; !ok {
		cfg["description"] = ""
	}
	cfg["hasBeian"] = false
	if zones, ok := cfg["clientZones"].([]any); ok {
		for _, z := range zones {
			zm, _ := z.(map[string]any)
			if _, ok := zm["weight"]; !ok {
				zm["weight"] = float64(100)
			}
			if a, ok := zm["action"].(map[string]any); ok && a["type"] == "deliver" {
				if _, ok := a["enableIPv6"]; !ok {
					a["enableIPv6"] = true
				}
				// The real API returns canonical, sorted server group names.
				if by, ok := a["by"].([]any); ok {
					names := make([]string, 0, len(by))
					for _, b := range by {
						names = append(names, b.(string))
					}
					for i := 1; i < len(names); i++ {
						for j := i; j > 0 && names[j] < names[j-1]; j-- {
							names[j], names[j-1] = names[j-1], names[j]
						}
					}
					out := make([]any, len(names))
					for i, n := range names {
						out[i] = n
					}
					a["by"] = out
				}
			}
		}
	}
	if d, ok := cfg["deliverByHttps"].(map[string]any); ok && strings.EqualFold(fmt.Sprint(d["state"]), "off") {
		cfg["deliverByHttps"] = map[string]any{"state": "off"}
	} else if ok {
		if _, has := d["ttl"]; !has {
			d["ttl"] = float64(1800)
		}
		if _, has := d["port"]; !has {
			d["port"] = float64(443)
		}
	}
	return cfg
}

func (m *mockAPI) handleEdgeHostnames(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method != http.MethodPost {
			apiError(w, 405, "MethodNotAllowed", "")
			return
		}
		body, _ := readBody(r)
		name, _ := body["edgeHostname"].(string)
		name = strings.ToLower(name)
		if name == "" {
			name = m.nextID("auto") + ".edge.example"
		}
		if _, exists := m.edgeHosts[name]; exists {
			apiError(w, 409, "EdgeHostnameAlreadyExists", "exists")
			return
		}
		body["edgeHostname"] = name
		cfg := normalizeEdgeConfig(body)
		e := &mockEdgeHostname{configs: cfg, lastUpdate: time.Now()}
		e.push("creation", cfg)
		m.edgeHosts[name] = e
		w.Header().Set("Location", location(r, name))
		w.WriteHeader(201)
		return
	}
	name := strings.ToLower(rest[0])
	e, ok := m.edgeHosts[name]
	if !ok {
		apiError(w, 404, "InvalidEdgeHostname", "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		e.tick()
		if e.deleting && !e.inFlight() {
			delete(m.edgeHosts, name)
			apiError(w, 404, "InvalidEdgeHostname", "not found")
			return
		}
		w.Header().Set("Last-Modified", httpDate(e.lastUpdate))
		writeJSON(w, 200, map[string]any{
			"ownerCustomerId": 1, "configs": e.configs, "creationTime": "2026-01-01T00:00:00Z",
			"lastUpdateTime": e.lastUpdate.UTC().Format("2006-01-02T15:04:05Z"), "history": e.history,
		})
	case http.MethodPut:
		if !preconditionOK(w, r, e.lastUpdate) {
			return
		}
		if e.inFlight() {
			apiError(w, 400, "EdgeHostnameBeingDeployed", "operation in progress")
			return
		}
		body, _ := readBody(r)
		if bn, _ := body["edgeHostname"].(string); !strings.EqualFold(bn, name) {
			apiError(w, 409, "EdgeHostnameChangeForbidden", "edge hostname cannot change")
			return
		}
		body["edgeHostname"] = name
		cfg := normalizeEdgeConfig(body)
		e.pending = cfg
		e.push("update", cfg)
		e.lastUpdate = time.Now()
		w.WriteHeader(204)
	case http.MethodDelete:
		if e.inFlight() {
			apiError(w, 400, "EdgeHostnameBeingDeployed", "operation in progress")
			return
		}
		if strings.Contains(name, "busy") && !strings.EqualFold(r.Header.Get(client.HeaderCheckUsage), "no") {
			apiError(w, 400, "removingActiveEdgeHostname", "served traffic recently")
			return
		}
		e.deleting = true
		e.push("delete", e.configs)
		w.WriteHeader(204)
	default:
		apiError(w, 405, "MethodNotAllowed", "")
	}
}

// ---- deployment tasks and validations ----------------------------------------

func (m *mockAPI) handleDeployments(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		body, _ := readBody(r)
		m.deploymentPosts++
		name, _ := body["name"].(string)
		target, _ := body["target"].(string)
		if strings.TrimSpace(name) == "" {
			apiError(w, 400, "InvalidInput", "name is required")
			return
		}
		if target != "staging" && target != "production" {
			apiError(w, 400, "InvalidDeploymentTarget", "bad target")
			return
		}
		actions, _ := body["actions"].([]any)
		for _, a := range actions {
			am, _ := a.(map[string]any)
			action, _ := am["action"].(string)
			switch action {
			case "deploy_property", "remove_property":
				pid, _ := am["propertyId"].(string)
				p, ok := m.properties[pid]
				if !ok {
					apiError(w, 400, "InvalidPropertyId", "unknown property "+pid)
					return
				}
				if action == "deploy_property" {
					v := parseVersion(am["version"])
					pv := p.find(strconv.FormatInt(v, 10))
					if pv == nil {
						apiError(w, 400, "InvalidPropertyVersion", "unknown version")
						return
					}
					if pv.lastValidationStatus != "Validated" {
						apiError(w, 400, "InvalidPropertyStatus", "version not validated")
						return
					}
					pv.frozen = true
				}
			case "deploy_cert", "remove_cert":
				cid, _ := am["certificateId"].(string)
				if _, ok := m.certificates[cid]; !ok {
					apiError(w, 400, "InvalidCertificateId", "unknown certificate "+cid)
					return
				}
			default:
				apiError(w, 400, "InvalidAction", "bad action")
				return
			}
		}
		id := m.nextID("dep")
		m.deployments[id] = &mockTask{kind: "deployment", body: body, status: "waiting", ticks: 2, created: time.Now()}
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	}
	if len(rest) == 1 && r.Method == http.MethodGet {
		t, ok := m.deployments[rest[0]]
		if !ok {
			apiError(w, 404, "InvalidDeploymentTaskId", "not found")
			return
		}
		m.tickDeployment(t)
		writeJSON(w, 200, map[string]any{
			"name": t.body["name"], "target": t.body["target"], "actions": t.body["actions"], "webhook": t.body["webhook"],
			"status": t.status, "statusDetails": "", "submissionTime": "2026-01-01T00:00:00Z",
			"finishTime": map[bool]string{true: "2026-01-01T00:01:00Z", false: ""}[t.status == "succeeded"],
		})
		return
	}
	apiError(w, 404, "NotFound", "unknown deployment path")
}

func parseVersion(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func (m *mockAPI) tickDeployment(t *mockTask) {
	if t.ticks <= 0 {
		return
	}
	t.ticks--
	if t.ticks > 0 {
		t.status = "inprogress"
		return
	}
	t.status = "succeeded"
	if t.applied {
		return
	}
	t.applied = true
	target, _ := t.body["target"].(string)
	actions, _ := t.body["actions"].([]any)
	for _, a := range actions {
		am, _ := a.(map[string]any)
		action, _ := am["action"].(string)
		var slot **int64
		switch action {
		case "deploy_property", "remove_property":
			p := m.properties[am["propertyId"].(string)]
			if p == nil {
				continue
			}
			if target == "production" {
				slot = &p.inProduction
			} else {
				slot = &p.inStaging
			}
		default:
			c := m.certificates[am["certificateId"].(string)]
			if c == nil {
				continue
			}
			if target == "production" {
				slot = &c.inProduction
			} else {
				slot = &c.inStaging
			}
		}
		if strings.HasPrefix(action, "deploy_") {
			v := parseVersion(am["version"])
			*slot = &v
		} else {
			*slot = nil
		}
	}
}

func (m *mockAPI) handleValidations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		body, _ := readBody(r)
		m.validationPosts++
		if name, _ := body["name"].(string); strings.TrimSpace(name) == "" {
			apiError(w, 400, "InvalidInput", "name is required")
			return
		}
		pid, _ := body["propertyId"].(string)
		p, ok := m.properties[pid]
		if !ok {
			apiError(w, 400, "InvalidPropertyId", "unknown property")
			return
		}
		pv := p.find(strconv.FormatInt(parseVersion(body["version"]), 10))
		if pv == nil {
			apiError(w, 400, "InvalidPropertyVersion", "unknown version")
			return
		}
		pv.lastValidationStatus = "InProgress"
		id := m.nextID("val")
		m.validations[id] = &mockTask{kind: "validation", body: body, status: "waiting", ticks: 2, created: time.Now()}
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	}
	if len(rest) == 1 && r.Method == http.MethodGet {
		t, ok := m.validations[rest[0]]
		if !ok {
			apiError(w, 404, "InvalidValidationTaskId", "not found")
			return
		}
		if t.ticks > 0 {
			t.ticks--
			if t.ticks > 0 {
				t.status = "in progress"
			} else {
				t.status = "succeeded"
				p := m.properties[t.body["propertyId"].(string)]
				if pv := p.find(strconv.FormatInt(parseVersion(t.body["version"]), 10)); pv != nil {
					pv.lastValidationStatus = "Validated"
				}
			}
		}
		writeJSON(w, 200, map[string]any{
			"name": t.body["name"], "propertyId": t.body["propertyId"], "version": parseVersion(t.body["version"]),
			"status": t.status, "statusDetails": "", "submissionTime": "2026-01-01T00:00:00Z",
		})
		return
	}
	apiError(w, 404, "NotFound", "unknown validation path")
}

// ---- test accessors ----------------------------------------------------------

func (m *mockAPI) propertyVersionCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.properties[id]; ok {
		return len(p.versions)
	}
	return -1
}

func (m *mockAPI) propertyIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.properties))
	for id := range m.properties {
		ids = append(ids, id)
	}
	return ids
}

func (m *mockAPI) counts() (deployments, validations int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deploymentPosts, m.validationPosts
}

func (m *mockAPI) seedDeployedProperty(name, target string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID("prop")
	v := int64(1)
	p := &mockProperty{name: name, lastUpdate: time.Now(), versions: []*mockPropertyVersion{{
		version: 1, frozen: true, lastValidationStatus: "Validated",
		config: applyPropertyDefaults(map[string]any{
			"hostnames": []any{"adopt.example.com"}, "edgeLogic": "location / { origin_pass origin; }",
			"origins": []any{map[string]any{"name": "origin", "servers": []any{"origin.example.com"}}},
		}),
	}}}
	if target == "production" {
		p.inProduction = &v
	} else {
		p.inStaging = &v
	}
	m.properties[id] = p
	return id
}
