package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/steamwo/qoder-proxy/internal/credential"
)

const (
	methodQuotaIdentifier = "quota.identifier"
	methodQuotaDescribe   = "quota.describe"
	methodQuotaFetch      = "quota.fetch"
	methodQuotaReset      = "quota.reset"

	qoderUsageURL = "https://openapi.qoder.sh/sash/api/v2/me/usage"
)

type quotaDescribeRequest struct {
	Plugin metadata `json:"plugin,omitempty"`
}

type quotaDescribeResponse struct {
	SupportedProviders []string `json:"supported_providers,omitempty"`
	DisplayName        string   `json:"display_name,omitempty"`
	SupportsReset      bool     `json:"supports_reset,omitempty"`
}

type quotaFetchRequest struct {
	AuthIndex      string            `json:"auth_index"`
	AuthID         string            `json:"auth_id"`
	Provider       string            `json:"provider"`
	StorageJSON    []byte            `json:"storage_json,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	Host           hostConfigSummary `json:"host,omitempty"`
	HostCallbackID string            `json:"host_callback_id,omitempty"`
}

type quotaSubscription struct {
	Plan     string `json:"plan,omitempty"`
	TierName string `json:"tierName,omitempty"`
	TierID   string `json:"tierId,omitempty"`
}

type quotaMetric struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit,omitempty"`
	Format   string  `json:"format,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

type quotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []quotaBucket `json:"buckets,omitempty"`
}

type quotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description,omitempty"`
}

type quotaFetchResponse struct {
	Subscription       *quotaSubscription `json:"subscription,omitempty"`
	Summary            []quotaMetric      `json:"summary,omitempty"`
	ServerTimeOffsetMs int64              `json:"serverTimeOffsetMs,omitempty"`
	Groups             []quotaGroup       `json:"groups,omitempty"`
}

type quotaResetRequest struct {
	AuthIndex      string            `json:"auth_index"`
	AuthID         string            `json:"auth_id"`
	Provider       string            `json:"provider"`
	StorageJSON    []byte            `json:"storage_json,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	Host           hostConfigSummary `json:"host,omitempty"`
	HostCallbackID string            `json:"host_callback_id,omitempty"`
}

type quotaResetResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type parsedQuota struct {
	Name      string
	Unit      string
	Remaining float64
	Used      float64
	Total     float64
	ResetTime string
}

func quotaIdentifier() ([]byte, error) {
	return okEnvelope(map[string]string{"identifier": pluginID})
}

func describeQuota(_ []byte) ([]byte, error) {
	return okEnvelope(quotaDescribeResponse{
		SupportedProviders: []string{pluginID},
		DisplayName:        "Qoder Credits",
		SupportsReset:      false,
	})
}

func fetchQuota(raw []byte) ([]byte, error) {
	var req quotaFetchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cred, err := credentialFromStorage(req.StorageJSON)
	if err != nil {
		return errorEnvelope("invalid_auth", err.Error(), http.StatusUnauthorized), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := fetchQoderUsage(ctx, newHostHTTPClient(req.HostCallbackID), cred)
	if err != nil {
		return errorEnvelope("quota_fetch_failed", err.Error(), http.StatusBadGateway), nil
	}
	return okEnvelope(resp)
}

func resetQuota(_ []byte) ([]byte, error) {
	return okEnvelope(quotaResetResponse{
		Success: false,
		Message: "Qoder does not support resetting account credits from CPA.",
	})
}

func fetchQoderUsage(ctx context.Context, client *http.Client, cred credential.Credential) (quotaFetchResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qoderUsageURL, nil)
	if err != nil {
		return quotaFetchResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cred.Token))
	req.Header.Set("Cosy-ClientType", "10")
	req.Header.Set("User-Agent", "Qoder")

	resp, err := client.Do(req)
	if err != nil {
		return quotaFetchResponse{}, fmt.Errorf("Qoder usage request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return quotaFetchResponse{}, fmt.Errorf("read Qoder usage response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if len(message) > 300 {
			message = message[:300]
		}
		return quotaFetchResponse{}, fmt.Errorf("Qoder usage returned HTTP %d: %s", resp.StatusCode, message)
	}
	return parseQoderUsage(body)
}

func parseQoderUsage(raw []byte) (quotaFetchResponse, error) {
	var root map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return quotaFetchResponse{}, fmt.Errorf("decode Qoder usage: %w", err)
	}
	usage := quotaObject(root, "qoderUsage", "qoder_usage")
	if usage == nil {
		mode := quotaString(root, "displayMode", "display_mode")
		if strings.EqualFold(mode, "enterprise") {
			return quotaFetchResponse{}, fmt.Errorf("Qoder enterprise account does not expose numeric credits through this endpoint")
		}
		return quotaFetchResponse{}, fmt.Errorf("Qoder usage response is missing qoderUsage")
	}

	defaultReset := quotaTime(usage, "expiresAt", "expires_at")
	packages := make([]parsedQuota, 0, 4)
	if q := parseQuotaObject("Plan", quotaObject(usage, "userQuota", "user_quota"), defaultReset); q != nil {
		packages = append(packages, *q)
	}
	if q := parseQuotaObject("Add-on", quotaObject(usage, "addOnQuota", "add_on_quota"), defaultReset); q != nil {
		packages = append(packages, *q)
	}
	for _, item := range quotaArray(usage, "dedicatedResourcePackages", "dedicated_resource_packages") {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := quotaString(entry, "name", "id")
		if name == "" {
			if labels := quotaArray(entry, "displayLabels", "display_labels"); len(labels) > 0 {
				if first, ok := labels[0].(map[string]any); ok {
					name = quotaString(first, "value")
				}
			}
		}
		if name == "" {
			name = "Dedicated package"
		}
		reset := quotaTime(entry, "expiresAt", "expires_at")
		if q := parseQuotaObject(name, entry, reset); q != nil {
			packages = append(packages, *q)
		}
	}
	if len(packages) == 0 {
		return quotaFetchResponse{}, fmt.Errorf("Qoder usage response contains no numeric credit buckets")
	}

	var totalRemaining, totalUsed, totalLimit float64
	unit := "credits"
	buckets := make([]quotaBucket, 0, len(packages))
	for _, pkg := range packages {
		totalRemaining += pkg.Remaining
		totalUsed += pkg.Used
		totalLimit += pkg.Total
		if pkg.Unit != "" {
			unit = pkg.Unit
		}
		fraction := 0.0
		if pkg.Total > 0 {
			fraction = clamp01(pkg.Remaining / pkg.Total)
		}
		buckets = append(buckets, quotaBucket{
			Window:            pkg.Name,
			RemainingFraction: fraction,
			ResetTime:         pkg.ResetTime,
			Description:       fmt.Sprintf("%s / %s %s remaining", formatQuotaNumber(pkg.Remaining), formatQuotaNumber(pkg.Total), pkg.Unit),
		})
	}

	var subscription *quotaSubscription
	plan := quotaString(usage, "userType", "user_type")
	tier := quotaString(usage, "usageType", "usage_type")
	if plan != "" || tier != "" {
		subscription = &quotaSubscription{Plan: plan, TierName: tier}
	}
	return quotaFetchResponse{
		Subscription: subscription,
		Summary: []quotaMetric{
			{Key: "remaining_credits", Label: "Remaining", Value: totalRemaining, Unit: unit, Format: "number"},
			{Key: "used_credits", Label: "Used", Value: totalUsed, Unit: unit, Format: "number"},
			{Key: "total_credits", Label: "Total", Value: totalLimit, Unit: unit, Format: "number"},
		},
		Groups: []quotaGroup{{DisplayName: "Credits", Buckets: buckets}},
	}, nil
}

func parseQuotaObject(name string, source map[string]any, reset string) *parsedQuota {
	if source == nil {
		return nil
	}
	total, hasTotal := quotaNumber(source, "total", "limitValue", "limit_value")
	used, hasUsed := quotaNumber(source, "used", "usedValue", "used_value")
	remaining, hasRemaining := quotaNumber(source, "remaining", "remainingValue", "remaining_value")
	if !hasTotal && !hasUsed && !hasRemaining {
		return nil
	}
	if total < 0 {
		total = 0
	}
	if used < 0 {
		used = 0
	}
	if !hasRemaining {
		remaining = math.Max(0, total-used)
	}
	if remaining < 0 {
		remaining = 0
	}
	if !hasTotal || total <= 0 {
		total = math.Max(0, used+remaining)
	}
	unit := quotaString(source, "unit")
	if unit == "" {
		unit = "credits"
	}
	return &parsedQuota{Name: name, Unit: unit, Remaining: remaining, Used: used, Total: total, ResetTime: reset}
}

func quotaObject(source map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := source[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func quotaArray(source map[string]any, keys ...string) []any {
	for _, key := range keys {
		if value, ok := source[key].([]any); ok {
			return value
		}
	}
	return nil
}

func quotaString(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func quotaNumber(source map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch value := source[key].(type) {
		case float64:
			return value, true
		case float32:
			return float64(value), true
		case int:
			return float64(value), true
		case int64:
			return float64(value), true
		case json.Number:
			if n, err := value.Float64(); err == nil {
				return n, true
			}
		case string:
			if n, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func quotaTime(source map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := source[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			text := strings.TrimSpace(typed)
			if text == "" {
				continue
			}
			if parsed, err := time.Parse(time.RFC3339, text); err == nil {
				return parsed.UTC().Format(time.RFC3339)
			}
			if n, err := strconv.ParseInt(text, 10, 64); err == nil {
				return quotaUnixTime(n)
			}
		case json.Number:
			if n, err := typed.Int64(); err == nil {
				return quotaUnixTime(n)
			}
		case float64:
			return quotaUnixTime(int64(typed))
		}
	}
	return ""
}

func quotaUnixTime(value int64) string {
	if value <= 0 {
		return ""
	}
	if value > 10_000_000_000 {
		return time.UnixMilli(value).UTC().Format(time.RFC3339)
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func formatQuotaNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
