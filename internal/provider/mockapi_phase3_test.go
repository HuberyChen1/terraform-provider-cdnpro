package provider

import (
	"net/http"
	"strings"
	"time"
)

// Phase 3 endpoints of the mock API: purges, prefetches and the read-only
// lookups (hostnames, shields, isps, clientRegions).

type mockShield struct {
	id, name, region string
	ipv4, ipv6       []string
	used             bool
}

var (
	mockShields = []mockShield{
		{id: "shield-us-1", name: "US East", region: "us", ipv4: []string{"203.0.113.0/24"}, ipv6: []string{"2001:0db8:0000:0000:0000:0000:0000:0000/32"}, used: true},
		{id: "shield-eu-1", name: "EU West", region: "ie", ipv4: []string{"198.51.100.0/24"}, ipv6: nil, used: false},
	}
	mockISPs = []map[string]string{
		{"code": "att", "name": "ATT", "region": "na.us"},
		{"code": "comcast", "name": "Comcast", "region": "na.us"},
		{"code": "cmcc", "name": "China Mobile", "region": "as.cn"},
	}
	mockRegions = []map[string]string{
		{"code": "na.us", "name": "United States"},
		{"code": "as.cn", "name": "China"},
		{"code": "eu", "name": "Europe"},
	}
)

// partialPurgeMarker in any purge URL makes the mock report a 50% success rate.
const partialPurgeMarker = "partial"

func (m *mockAPI) handlePurges(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		body, _ := readBody(r)
		m.purgePosts = append(m.purgePosts, body)
		target, _ := body["target"].(string)
		if target != "staging" && target != "production" {
			apiError(w, 400, "InvalidPurgeTarget", "bad target")
			return
		}
		files, _ := body["fileUrls"].([]any)
		dirs, _ := body["dirUrls"].([]any)
		regex, _ := body["regexPatterns"].([]any)
		if len(files)+len(dirs)+len(regex) == 0 {
			apiError(w, 400, "EmptyPurgeUrl", "no urls")
			return
		}
		if len(regex) > 2 {
			apiError(w, 400, "InvalidPurgeUrl", "too many regex patterns")
			return
		}
		rate := 100.0
		for _, u := range append(files, dirs...) {
			if s, _ := u.(string); strings.Contains(s, "undeployed") {
				apiError(w, 400, "InvalidPurgeUrl", "The host has not been deployed to "+target)
				return
			} else if strings.Contains(s, partialPurgeMarker) {
				rate = 50
			}
		}
		id := m.nextID("purge")
		m.purges[id] = &mockTask{kind: "purge", body: body, status: "waiting", ticks: 2, created: time.Now(), rate: rate}
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	}
	if len(rest) == 1 && r.Method == http.MethodGet {
		t, ok := m.purges[rest[0]]
		if !ok {
			apiError(w, 404, "InvalidPurgeTaskId", "not found")
			return
		}
		tickSimpleTask(t, "finished")
		out := map[string]any{
			"name": t.body["name"], "target": t.body["target"], "action": "invalidate", "status": t.status,
			"submissionTime": "2026-01-01T00:00:00Z", "customerId": 1,
			"fileUrls": t.body["fileUrls"], "dirUrls": t.body["dirUrls"], "regexPatterns": t.body["regexPatterns"],
		}
		if t.status == "finished" {
			out["finishTime"] = "2026-01-01T00:05:00Z"
			out["successRate"] = t.rate
			if t.rate < 100 {
				out["errors"] = []map[string]any{{"errorCode": "PartialFailure", "errorMessage": "some edges did not respond"}}
			}
		}
		writeJSON(w, 200, out)
		return
	}
	apiError(w, 404, "NotFound", "unknown purge path")
}

func (m *mockAPI) handlePrefetches(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 && r.Method == http.MethodPost {
		body, _ := readBody(r)
		m.prefetchPosts = append(m.prefetchPosts, body)
		files, _ := body["fileList"].([]any)
		if len(files) == 0 {
			apiError(w, 400, "InvalidPrefetchUrl", "fileList is empty")
			return
		}
		for _, f := range files {
			fm, _ := f.(map[string]any)
			if u, _ := fm["url"].(string); strings.Contains(u, "undeployed") {
				apiError(w, 400, "NoSuchDomain", "host not deployed to production")
				return
			}
		}
		if st, ok := body["startTime"].(string); ok {
			t, err := time.Parse("2006-01-02T15:04:05Z", st)
			if err != nil || !t.After(time.Now()) || t.After(time.Now().Add(24*time.Hour)) {
				apiError(w, 400, "InvalidTime", "The start time has to be within 24 hours in the future.")
				return
			}
		}
		id := m.nextID("pf")
		m.prefetches[id] = &mockTask{kind: "prefetch", body: body, status: "waiting", ticks: 2, created: time.Now(), rate: 100}
		w.Header().Set("Location", location(r, id))
		w.WriteHeader(201)
		return
	}
	if len(rest) == 1 && r.Method == http.MethodGet {
		t, ok := m.prefetches[rest[0]]
		if !ok {
			apiError(w, 404, "InvalidPrefetchId", "not found")
			return
		}
		tickSimpleTask(t, "finished")
		md := map[string]any{"id": rest[0], "status": t.status, "submissionTime": "2026-01-01T00:00:00Z", "customerId": 1}
		if t.status == "finished" {
			md["finishTime"] = "2026-01-01T00:05:00Z"
			md["successRate"] = t.rate
		}
		writeJSON(w, 200, map[string]any{
			"name": t.body["name"], "fileList": t.body["fileList"], "regions": t.body["regions"],
			"prefetchLevel": "Parent", "metaData": md,
		})
		return
	}
	apiError(w, 404, "NotFound", "unknown prefetch path")
}

// tickSimpleTask advances waiting -> inprogress -> terminal on successive GETs.
func tickSimpleTask(t *mockTask, terminal string) {
	if t.ticks <= 0 {
		return
	}
	t.ticks--
	if t.ticks > 0 {
		t.status = "inprogress"
	} else {
		t.status = terminal
	}
}

func (m *mockAPI) handleHostnames(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || r.Method != http.MethodGet {
		apiError(w, 404, "NotFound", "unknown hostname path")
		return
	}
	hostname := strings.ToLower(rest[0])
	out := map[string]any{"hostname": hostname, "propertyInProduction": nil, "propertyInStaging": nil}
	for id, p := range m.properties {
		for _, pv := range p.versions {
			hosts, _ := pv.config["hostnames"].([]any)
			for _, h := range hosts {
				if hs, _ := h.(string); strings.EqualFold(hs, hostname) {
					if p.inProduction != nil && *p.inProduction == pv.version {
						out["propertyInProduction"] = map[string]any{"propertyId": id, "version": pv.version}
					}
					if p.inStaging != nil && *p.inStaging == pv.version {
						out["propertyInStaging"] = map[string]any{"propertyId": id, "version": pv.version}
					}
				}
			}
		}
	}
	writeJSON(w, 200, out)
}

func (m *mockAPI) handleShields(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodGet {
		apiError(w, 404, "NotFound", "unknown shields path")
		return
	}
	q := r.URL.Query()
	search := q.Get("search")
	var used *bool
	if v := q.Get("usedInProperties"); v != "" {
		b := v == "true"
		used = &b
	}
	out := []map[string]any{}
	for _, s := range mockShields {
		if search != "" && !strings.Contains(s.id+s.name+s.region, search) {
			continue
		}
		if used != nil && s.used != *used {
			continue
		}
		item := map[string]any{
			"id": s.id, "name": s.name, "region": s.region, "ipV4": s.ipv4,
			"lastUpdated": "2026-01-01T00:35:01.000+00:00", "effectiveDate": "2026-01-02T00:00:00.000+00:00",
			"usedInProperties": s.used,
		}
		if s.ipv6 != nil {
			item["ipV6"] = s.ipv6
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"shields": out})
}

func filterCodeNames(items []map[string]string, search, filterKey, filterVal string) []map[string]string {
	out := []map[string]string{}
	for _, it := range items {
		if search != "" && !strings.Contains(it["code"]+it["name"], search) {
			continue
		}
		if filterVal != "" && it[filterKey] != filterVal {
			continue
		}
		out = append(out, map[string]string{"code": it["code"], "name": it["name"]})
	}
	return out
}

func (m *mockAPI) handleISPs(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodGet {
		apiError(w, 404, "NotFound", "unknown isps path")
		return
	}
	q := r.URL.Query()
	writeJSON(w, 200, filterCodeNames(mockISPs, q.Get("search"), "region", q.Get("regionCode")))
}

func (m *mockAPI) handleClientRegions(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodGet {
		apiError(w, 404, "NotFound", "unknown clientRegions path")
		return
	}
	q := r.URL.Query()
	items := mockRegions
	if isp := q.Get("ispCode"); isp != "" {
		// Regions where the ISP operates.
		items = nil
		for _, i := range mockISPs {
			if i["code"] != isp {
				continue
			}
			for _, reg := range mockRegions {
				if reg["code"] == i["region"] {
					items = append(items, reg)
				}
			}
		}
	}
	writeJSON(w, 200, filterCodeNames(items, q.Get("search"), "", ""))
}
