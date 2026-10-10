package main

import "testing"

func TestParseConfig(t *testing.T) {
	cfg := parseConfig([]byte("enabled: true\nqueue_retries: 7\nqueue_max_wait: 90s\n"))
	if !cfg.Enabled || cfg.QueueRetries != 7 || cfg.QueueMaxWait.String() != "1m30s" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}
