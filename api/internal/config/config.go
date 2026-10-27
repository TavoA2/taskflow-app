// Package config loads runtime configuration from environment variables only,
// so the same binary runs unchanged in docker compose, k3d and EKS.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

type Config struct {
	// HTTPAddr is the listen address, e.g. ":8080".
	HTTPAddr string
	LogLevel slog.Level
	// DrainDelay is how long the server keeps serving after SIGTERM, so load balancers
	// and Kubernetes endpoints stop routing to this instance before connections close.
	DrainDelay time.Duration
	// ShutdownTimeout bounds how long in-flight requests get to finish after draining.
	ShutdownTimeout time.Duration
}

// Load reads configuration from the environment, applying defaults for unset variables.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{HTTPAddr: valueOr(getenv("HTTP_ADDR"), ":8080")}

	if err := cfg.LogLevel.UnmarshalText([]byte(valueOr(getenv("LOG_LEVEL"), "info"))); err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	var err error
	if cfg.DrainDelay, err = duration(getenv, "DRAIN_DELAY", "5s"); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = duration(getenv, "SHUTDOWN_TIMEOUT", "15s"); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func duration(getenv func(string) string, key, def string) (time.Duration, error) {
	d, err := time.ParseDuration(valueOr(getenv(key), def))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s: must not be negative, got %s", key, d)
	}
	return d, nil
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
