package main

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParseQoderUsage(t *testing.T) {
	raw := []byte(`{
		"displayMode":"qoder",
		"qoderUsage":{
			"userType":"personal_professional_trial",
			"usageType":"credits",
			"expiresAt":1791673619906,
			"userQuota":{"total":300,"used":50,"remaining":250,"percentage":0.1667,"unit":"credits"},
			"addOnQuota":{"total":100,"used":10,"remaining":90,"percentage":0.1,"unit":"credits"}
		}
	}`)
	got, err := parseQoderUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subscription == nil || got.Subscription.Plan != "personal_professional_trial" || got.Subscription.TierName != "credits" {
		t.Fatalf("subscription = %#v", got.Subscription)
	}
	metrics := map[string]float64{}
	for _, item := range got.Summary {
		metrics[item.Key] = item.Value
	}
	if metrics["remaining_credits"] != 340 || metrics["used_credits"] != 60 || metrics["total_credits"] != 400 {
		t.Fatalf("summary = %#v", got.Summary)
	}
	if len(got.Groups) != 1 || len(got.Groups[0].Buckets) != 2 {
		t.Fatalf("groups = %#v", got.Groups)
	}
	if diff := math.Abs(got.Groups[0].Buckets[0].RemainingFraction - (250.0 / 300.0)); diff > 1e-9 {
		t.Fatalf("plan remaining fraction = %f", got.Groups[0].Buckets[0].RemainingFraction)
	}
	if got.Groups[0].Buckets[1].RemainingFraction != 0.9 {
		t.Fatalf("add-on remaining fraction = %f", got.Groups[0].Buckets[1].RemainingFraction)
	}
	if got.Groups[0].Buckets[0].ResetTime == "" {
		t.Fatal("plan reset time is empty")
	}
}

func TestParseQoderUsageHandlesStringAndSnakeCaseFields(t *testing.T) {
	raw := []byte(`{
		"qoder_usage":{
			"user_type":"pro",
			"usage_type":"credits",
			"user_quota":{"total":"20","used":"7","remaining":"13","unit":"credits"},
			"dedicated_resource_packages":[{"name":"Bonus","total":"5","used":"2"}]
		}
	}`)
	got, err := parseQoderUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 || len(got.Groups[0].Buckets) != 2 {
		t.Fatalf("groups = %#v", got.Groups)
	}
	metrics := map[string]float64{}
	for _, item := range got.Summary {
		metrics[item.Key] = item.Value
	}
	if metrics["remaining_credits"] != 16 || metrics["used_credits"] != 9 || metrics["total_credits"] != 25 {
		t.Fatalf("summary = %#v", got.Summary)
	}
}

func TestQuotaDescribeRPCShape(t *testing.T) {
	raw, err := describeQuota(nil)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("describe quota failed: %#v", env.Error)
	}
	var desc quotaDescribeResponse
	if err := json.Unmarshal(env.Result, &desc); err != nil {
		t.Fatal(err)
	}
	if len(desc.SupportedProviders) != 1 || desc.SupportedProviders[0] != pluginID || desc.SupportsReset {
		t.Fatalf("quota description = %#v", desc)
	}
}
