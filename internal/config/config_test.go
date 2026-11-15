package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Zuneve/template/internal/config"
)

const (
	keyCheckInterval = "POSITION_CHECK_INTERVAL"
	keyStaleAfter    = "POSITION_STALE_AFTER"
	keyWorkers       = "POSITION_WORKERS"
	keyQueueSize     = "POSITION_QUEUE_SIZE"
	keyPushURL       = "PUSH_HTTP_URL"
	keyPushTimeout   = "PUSH_REQUEST_TIMEOUT"
)

func validEnv() map[string]string {
	return map[string]string{
		"HTTP_ADDR":                  ":8080",
		"LOG_LEVEL":                  "info",
		"SHUTDOWN_TIMEOUT":           "10s",
		"DATABASE_URL":               "postgres://localhost:5432/db",
		"DATABASE_MAX_CONNS":         "10",
		"DATABASE_MIN_CONNS":         "2",
		"DATABASE_MAX_CONN_LIFETIME": "30m",
		"DATABASE_CONNECT_TIMEOUT":   "5s",
		"DATABASE_QUERY_TIMEOUT":     "3s",
		keyCheckInterval:             "30s",
		keyStaleAfter:                "60s",
		keyWorkers:                   "4",
		keyQueueSize:                 "100",
		keyPushURL:                   "http://localhost:8090",
		keyPushTimeout:               "1s",
	}
}

func load(t *testing.T, override map[string]string) (config.Config, error) {
	t.Helper()

	env := validEnv()
	for k, v := range override {
		env[k] = v
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	return config.Load()
}

func TestLoad_Valid(t *testing.T) {
	cfg, err := load(t, nil)

	require.NoError(t, err)
	require.Equal(t, config.PositionsConfig{
		CheckInterval: 30 * time.Second,
		StaleAfter:    time.Minute,
		Workers:       4,
		QueueSize:     100,
	}, cfg.Positions)
	require.Equal(t, config.PushConfig{URL: "http://localhost:8090", RequestTimeout: time.Second}, cfg.Push)
}

func TestLoad_InvalidLab3Settings(t *testing.T) {
	tests := []struct {
		name     string
		override map[string]string
		wantErr  string
	}{
		{"missing check interval", map[string]string{keyCheckInterval: ""}, "POSITION_CHECK_INTERVAL is required"},
		{"zero check interval", map[string]string{keyCheckInterval: "0s"}, "POSITION_CHECK_INTERVAL must be positive"},
		{"negative stale after", map[string]string{keyStaleAfter: "-1m"}, "POSITION_STALE_AFTER must be positive"},
		{"bad duration", map[string]string{keyStaleAfter: "soon"}, "POSITION_STALE_AFTER: invalid duration"},
		{"zero workers", map[string]string{keyWorkers: "0"}, "POSITION_WORKERS must be positive"},
		{"workers not a number", map[string]string{keyWorkers: "many"}, "POSITION_WORKERS: invalid integer"},
		{"zero queue", map[string]string{keyQueueSize: "0"}, "POSITION_QUEUE_SIZE must be positive"},
		{"missing push url", map[string]string{keyPushURL: ""}, "PUSH_HTTP_URL is required"},
		{"push url without scheme", map[string]string{keyPushURL: "localhost:8090"}, "PUSH_HTTP_URL must be an absolute http(s) URL"},
		{"push url with other scheme", map[string]string{keyPushURL: "ftp://localhost"}, "PUSH_HTTP_URL must be an absolute http(s) URL"},
		{"zero push timeout", map[string]string{keyPushTimeout: "0s"}, "PUSH_REQUEST_TIMEOUT must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(t, tt.override)

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLoad_ReportsAllProblemsAtOnce(t *testing.T) {
	_, err := load(t, map[string]string{keyWorkers: "0", keyQueueSize: "0"})

	require.ErrorContains(t, err, "POSITION_WORKERS must be positive")
	require.ErrorContains(t, err, "POSITION_QUEUE_SIZE must be positive")
}
