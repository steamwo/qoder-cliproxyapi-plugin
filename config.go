package main

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type pluginConfig struct {
	Enabled      bool
	QueueRetries int
	QueueMaxWait time.Duration
}

var currentConfig atomic.Value

func defaultPluginConfig() pluginConfig {
	return pluginConfig{Enabled: true, QueueRetries: 20, QueueMaxWait: 10 * time.Minute}
}

func loadedConfig() pluginConfig {
	if v := currentConfig.Load(); v != nil {
		if cfg, ok := v.(pluginConfig); ok {
			return cfg
		}
	}
	return defaultPluginConfig()
}

// parseConfig intentionally supports only the small scalar surface owned by this
// plugin, avoiding a YAML runtime dependency in the shared library.
func parseConfig(raw []byte) pluginConfig {
	cfg := defaultPluginConfig()
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch key {
		case "enabled":
			if v, err := strconv.ParseBool(value); err == nil {
				cfg.Enabled = v
			}
		case "queue_retries":
			if v, err := strconv.Atoi(value); err == nil && v >= 0 {
				cfg.QueueRetries = v
			}
		case "queue_max_wait":
			if v, err := time.ParseDuration(value); err == nil && v > 0 {
				cfg.QueueMaxWait = v
			}
		}
	}
	return cfg
}
