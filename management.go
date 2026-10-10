package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const qoderOfficialLogoURL = "https://avatars.githubusercontent.com/u/219277434?v=4"

var managementPaths = struct {
	sync.RWMutex
	base     string
	resource string
}{
	base:     "/v0/management",
	resource: "/v0/resource/plugins/qoder",
}

type managementAccount struct {
	AuthIndex string              `json:"auth_index"`
	Name      string              `json:"name"`
	Email     string              `json:"email,omitempty"`
	Label     string              `json:"label,omitempty"`
	Status    string              `json:"status,omitempty"`
	Disabled  bool                `json:"disabled,omitempty"`
	Quota     *quotaFetchResponse `json:"quota,omitempty"`
	Error     string              `json:"error,omitempty"`
}

type managementDashboard struct {
	Plugin      string              `json:"plugin"`
	RefreshedAt string              `json:"refreshed_at"`
	Accounts    []managementAccount `json:"accounts"`
}

func registerManagement(raw []byte) ([]byte, error) {
	var req managementRegistrationRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	setManagementPaths(req.BasePath, req.ResourceBasePath)
	return okEnvelope(managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/plugins/qoder/accounts", Description: "List Qoder accounts with live credit usage."},
			{Method: http.MethodPost, Path: "/plugins/qoder/refresh", Description: "Refresh Qoder credit usage for all accounts."},
		},
		Resources: []managementResource{{
			Path:        "/panel",
			Menu:        "Qoder",
			Description: "Qoder account credits and plan usage.",
		}},
	})
}

func setManagementPaths(basePath, resourceBasePath string) {
	managementPaths.Lock()
	defer managementPaths.Unlock()
	if value := strings.TrimRight(strings.TrimSpace(basePath), "/"); value != "" {
		managementPaths.base = value
	}
	if value := strings.TrimRight(strings.TrimSpace(resourceBasePath), "/"); value != "" {
		managementPaths.resource = value
	}
}

func currentManagementPaths() (string, string) {
	managementPaths.RLock()
	defer managementPaths.RUnlock()
	return managementPaths.base, managementPaths.resource
}

func handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	basePath, resourceBasePath := currentManagementPaths()
	path := strings.TrimRight(strings.TrimSpace(req.Path), "/")
	panelPath := strings.TrimRight(resourceBasePath+"/panel", "/")
	if req.Method == http.MethodGet && path == panelPath {
		dashboard := loadManagementDashboard(req.HostCallbackID)
		page, err := renderManagementPanel(dashboard)
		if err != nil {
			return okEnvelope(managementTextResponse(http.StatusInternalServerError, err.Error()))
		}
		return okEnvelope(managementHTMLResponse(http.StatusOK, page))
	}
	apiBase := strings.TrimRight(basePath, "/") + "/plugins/qoder"
	switch {
	case req.Method == http.MethodGet && path == apiBase+"/accounts":
		return okEnvelope(managementJSONResponse(http.StatusOK, loadManagementDashboard(req.HostCallbackID)))
	case req.Method == http.MethodPost && path == apiBase+"/refresh":
		return okEnvelope(managementJSONResponse(http.StatusOK, loadManagementDashboard(req.HostCallbackID)))
	default:
		return okEnvelope(managementJSONResponse(http.StatusNotFound, map[string]any{"error": "not found"}))
	}
}

func loadManagementDashboard(callbackID string) managementDashboard {
	dashboard := managementDashboard{
		Plugin:      pluginID,
		RefreshedAt: time.Now().UTC().Format(time.RFC3339),
		Accounts:    []managementAccount{},
	}
	raw, err := callHost(methodHostAuthList, map[string]any{})
	if err != nil {
		dashboard.Accounts = append(dashboard.Accounts, managementAccount{Error: "host.auth.list: " + err.Error()})
		return dashboard
	}
	var listed hostAuthListResponse
	if err := json.Unmarshal(raw, &listed); err != nil {
		dashboard.Accounts = append(dashboard.Accounts, managementAccount{Error: "decode host.auth.list: " + err.Error()})
		return dashboard
	}
	for _, entry := range listed.Files {
		if !isQoderHostAuthEntry(entry) {
			continue
		}
		account := managementAccount{
			AuthIndex: strings.TrimSpace(entry.AuthIndex),
			Name:      strings.TrimSpace(entry.Name),
			Email:     strings.TrimSpace(entry.Email),
			Label:     strings.TrimSpace(entry.Label),
			Status:    strings.TrimSpace(entry.Status),
			Disabled:  entry.Disabled,
		}
		if account.AuthIndex == "" {
			account.Error = "missing auth_index"
			dashboard.Accounts = append(dashboard.Accounts, account)
			continue
		}
		getRaw, err := callHost(methodHostAuthGet, hostAuthGetRequest{AuthIndex: account.AuthIndex})
		if err != nil {
			account.Error = "host.auth.get: " + err.Error()
			dashboard.Accounts = append(dashboard.Accounts, account)
			continue
		}
		var stored hostAuthGetResponse
		if err := json.Unmarshal(getRaw, &stored); err != nil {
			account.Error = "decode host.auth.get: " + err.Error()
			dashboard.Accounts = append(dashboard.Accounts, account)
			continue
		}
		cred, err := credentialFromStorage(stored.JSON)
		if err != nil {
			account.Error = err.Error()
			dashboard.Accounts = append(dashboard.Accounts, account)
			continue
		}
		if account.Email == "" {
			account.Email = strings.TrimSpace(cred.Email)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		quota, err := fetchQoderUsage(ctx, newHostHTTPClient(callbackID), cred)
		cancel()
		if err != nil {
			account.Error = err.Error()
		} else {
			account.Quota = &quota
		}
		dashboard.Accounts = append(dashboard.Accounts, account)
	}
	return dashboard
}

func isQoderHostAuthEntry(entry hostAuthFileEntry) bool {
	if strings.EqualFold(strings.TrimSpace(entry.Provider), pluginID) || strings.EqualFold(strings.TrimSpace(entry.Type), pluginID) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(entry.Name)), pluginID+"-")
}

func managementJSONResponse(status int, value any) managementResponse {
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"encode response"}`)
	}
	return managementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       body,
	}
}

func managementHTMLResponse(status int, body []byte) managementResponse {
	return managementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       body,
	}
}

func managementTextResponse(status int, body string) managementResponse {
	return managementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
		Body:       []byte(body),
	}
}

var qoderPanelTemplate = template.Must(template.New("qoder-panel").Funcs(template.FuncMap{
	"metric": func(metric quotaMetric) string {
		value := strconv.FormatFloat(metric.Value, 'f', -1, 64)
		if metric.Unit != "" {
			value += " " + metric.Unit
		}
		return value
	},
	"percent": func(fraction float64) string {
		return strconv.Itoa(int(clamp01(fraction)*100 + 0.5))
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Qoder Credits</title>
<style>
:root{color-scheme:light dark;font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}body{margin:0;background:#f5f6f8;color:#17191c}.wrap{max-width:980px;margin:0 auto;padding:28px}.head{display:flex;align-items:center;justify-content:space-between;gap:18px;margin-bottom:20px}.brand{display:flex;align-items:center;gap:12px}.brand img{width:40px;height:40px;border-radius:10px}.brand h1{font-size:24px;margin:0}.muted{color:#69707a;font-size:13px}.refresh{border:1px solid #cfd3d8;background:#fff;color:#17191c;padding:9px 14px;border-radius:9px;font-weight:600;cursor:pointer}.grid{display:grid;gap:14px}.card{background:#fff;border:1px solid #e1e4e8;border-radius:14px;padding:18px;box-shadow:0 1px 2px rgba(0,0,0,.04)}.row{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.email{font-size:16px;font-weight:700}.status{font-size:12px;color:#69707a}.metrics{display:flex;flex-wrap:wrap;gap:10px;margin-top:14px}.metric{background:#f5f6f8;border-radius:10px;padding:10px 12px;min-width:130px}.metric b{display:block;font-size:15px;margin-top:3px}.groups{margin-top:16px;display:grid;gap:10px}.bucket{border-top:1px solid #eceef0;padding-top:10px}.bucket-line{display:flex;justify-content:space-between;gap:12px}.bar{height:7px;background:#eceef0;border-radius:999px;overflow:hidden;margin-top:7px}.bar span{display:block;height:100%;background:#272a2f}.error{margin-top:12px;color:#b42318;background:#fff1f0;border-radius:9px;padding:10px}.empty{background:#fff;border:1px dashed #cfd3d8;border-radius:14px;padding:28px;text-align:center;color:#69707a}@media(prefers-color-scheme:dark){body{background:#121416;color:#f4f5f6}.card,.refresh{background:#1c1f23;color:#f4f5f6;border-color:#34383e}.muted,.status{color:#9ba2ab}.metric{background:#24282d}.bucket{border-color:#30343a}.bar{background:#30343a}.bar span{background:#f4f5f6}.empty{background:#1c1f23;border-color:#34383e}.error{background:#351b1b;color:#ffb4ab}}</style>
</head>
<body><main class="wrap">
<div class="head"><div class="brand"><img src="` + qoderOfficialLogoURL + `" alt="Qoder"><div><h1>Qoder Credits</h1><div class="muted">Updated {{.RefreshedAt}}</div></div></div><form method="get"><button class="refresh" type="submit">刷新额度 / Refresh</button></form></div>
<div class="grid">
{{if .Accounts}}
{{range .Accounts}}<section class="card"><div class="row"><div><div class="email">{{if .Email}}{{.Email}}{{else if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</div><div class="status">{{.Name}}{{if .Status}} · {{.Status}}{{end}}{{if .Disabled}} · disabled{{end}}</div></div></div>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
{{if .Quota}}
{{if .Quota.Subscription}}<div class="metrics"><div class="metric">Plan<b>{{if .Quota.Subscription.TierName}}{{.Quota.Subscription.TierName}}{{else}}{{.Quota.Subscription.Plan}}{{end}}</b></div>{{range .Quota.Summary}}<div class="metric">{{.Label}}<b>{{metric .}}</b></div>{{end}}</div>{{else}}<div class="metrics">{{range .Quota.Summary}}<div class="metric">{{.Label}}<b>{{metric .}}</b></div>{{end}}</div>{{end}}
<div class="groups">{{range .Quota.Groups}}{{range .Buckets}}<div class="bucket"><div class="bucket-line"><span>{{.Window}}</span><strong>{{percent .RemainingFraction}}%</strong></div><div class="bar"><span style="width:{{percent .RemainingFraction}}%"></span></div>{{if .Description}}<div class="muted">{{.Description}}</div>{{end}}{{if .ResetTime}}<div class="muted">Reset/expiry: {{.ResetTime}}</div>{{end}}</div>{{end}}{{end}}</div>
{{end}}</section>{{end}}
{{else}}<div class="empty">No Qoder credentials found.</div>{{end}}
</div></main></body></html>`))

func renderManagementPanel(dashboard managementDashboard) ([]byte, error) {
	var out bytes.Buffer
	if err := qoderPanelTemplate.Execute(&out, dashboard); err != nil {
		return nil, fmt.Errorf("render Qoder management panel: %w", err)
	}
	return out.Bytes(), nil
}
