package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagementRegistrationUsesHostPaths(t *testing.T) {
	raw, err := registerManagement([]byte(`{"base_path":"/v8/management","resource_base_path":"/v0/resource/plugins/qoder"}`))
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("registration failed: %#v", env.Error)
	}
	var registration managementRegistrationResponse
	if err := json.Unmarshal(env.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Resources) != 1 || registration.Resources[0].Path != "/panel" || registration.Resources[0].Menu != "Qoder" {
		t.Fatalf("unexpected resources: %#v", registration.Resources)
	}
	if len(registration.Routes) != 2 {
		t.Fatalf("unexpected routes: %#v", registration.Routes)
	}
	base, resource := currentManagementPaths()
	if base != "/v8/management" || resource != "/v0/resource/plugins/qoder" {
		t.Fatalf("paths = %q, %q", base, resource)
	}
}

func TestQoderAuthEntryDetection(t *testing.T) {
	cases := []struct {
		name  string
		entry hostAuthFileEntry
		want  bool
	}{
		{name: "provider", entry: hostAuthFileEntry{Provider: "qoder"}, want: true},
		{name: "type", entry: hostAuthFileEntry{Type: "QODER"}, want: true},
		{name: "legacy filename", entry: hostAuthFileEntry{Name: "qoder-user@example.com.json"}, want: true},
		{name: "other", entry: hostAuthFileEntry{Provider: "codex", Name: "codex.json"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isQoderHostAuthEntry(tc.entry); got != tc.want {
				t.Fatalf("isQoderHostAuthEntry() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestManagementPanelUsesQoderBrandAndQuota(t *testing.T) {
	quota := quotaFetchResponse{
		Subscription: &quotaSubscription{Plan: "pro", TierName: "Pro"},
		Summary: []quotaMetric{{Key: "remaining_credits", Label: "Remaining", Value: 830, Unit: "credits"}},
		Groups: []quotaGroup{{DisplayName: "Credits", Buckets: []quotaBucket{{Window: "Plan", RemainingFraction: 0.83, Description: "830 / 1000 credits remaining"}}}},
	}
	page, err := renderManagementPanel(managementDashboard{
		Plugin:      pluginID,
		RefreshedAt: "2026-10-10T00:00:00Z",
		Accounts: []managementAccount{{
			Name: "qoder-user@example.com.json", Email: "user@example.com", Status: "active", Quota: &quota,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	for _, want := range []string{"Qoder Credits", qoderOfficialLogoURL, "刷新额度 / Refresh", "user@example.com", "830 credits", "83%"} {
		if !strings.Contains(body, want) {
			t.Fatalf("panel missing %q", want)
		}
	}
}
