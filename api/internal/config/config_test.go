package config

import (
	"log/slog"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(envFrom(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{HTTPAddr: ":8080", LogLevel: slog.LevelInfo, DrainDelay: 5 * time.Second, ShutdownTimeout: 15 * time.Second}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"HTTP_ADDR":        ":9090",
		"LOG_LEVEL":        "debug",
		"DRAIN_DELAY":      "0s",
		"SHUTDOWN_TIMEOUT": "2s",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{HTTPAddr: ":9090", LogLevel: slog.LevelDebug, DrainDelay: 0, ShutdownTimeout: 2 * time.Second}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"bad log level":     {"LOG_LEVEL": "verbose"},
		"bad duration":      {"DRAIN_DELAY": "five"},
		"negative duration": {"SHUTDOWN_TIMEOUT": "-1s"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := load(envFrom(env)); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}
