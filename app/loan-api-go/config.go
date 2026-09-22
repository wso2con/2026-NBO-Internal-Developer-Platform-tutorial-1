// config.go — environment configuration (BUILD-SPEC.md §3.2, §7.1).
//
// Env vars only, read and range-checked ONCE at startup. A required variable that
// is missing names itself and exits non-zero — loudly at startup, never silently
// on the first request. Same rule, same messages as the Ballerina loan-api.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	port             int
	databaseURL      string
	natsURL          string
	creditScoringURL string
	scoringTimeout   time.Duration
	logLevel         string
}

const (
	defaultPort             = 8080
	defaultScoringTimeoutMS = 2000
	minPort                 = 1
	maxPort                 = 65535
)

var validLogLevels = []string{"debug", "info", "warn", "error"}

func loadConfig() (config, error) {
	cfg := config{
		port:           defaultPort,
		scoringTimeout: defaultScoringTimeoutMS * time.Millisecond,
	}

	// PORT is honoured here, unlike the Ballerina service: that one had to bind a
	// compile-time constant because the Ballerina buildpack generates an OpenAPI
	// definition at build time. A Dockerfile build has no such constraint, so §3.2
	// applies unmodified.
	if raw := strings.TrimSpace(os.Getenv("PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("PORT must be a number, got %q", raw)
		}
		if parsed < minPort || parsed > maxPort {
			return cfg, fmt.Errorf("PORT must be between %d and %d, got %d", minPort, maxPort, parsed)
		}
		cfg.port = parsed
	}

	var err error
	// All three are injected by the platform on OpenChoreo and by compose locally.
	if cfg.databaseURL, err = requireEnv("DATABASE_URL"); err != nil {
		return cfg, err
	}
	if cfg.natsURL, err = requireEnv("NATS_URL"); err != nil {
		return cfg, err
	}
	if cfg.creditScoringURL, err = requireEnv("CREDIT_SCORING_URL"); err != nil {
		return cfg, err
	}

	if raw := strings.TrimSpace(os.Getenv("SCORING_TIMEOUT_MS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return cfg, fmt.Errorf("SCORING_TIMEOUT_MS must be a positive number, got %q", raw)
		}
		cfg.scoringTimeout = time.Duration(parsed) * time.Millisecond
	}

	level, err := requireEnv("LOG_LEVEL")
	if err != nil {
		return cfg, err
	}
	cfg.logLevel = strings.ToLower(level)
	if !slices_contains(validLogLevels, cfg.logLevel) {
		return cfg, fmt.Errorf("LOG_LEVEL must be one of %s, got %q",
			strings.Join(validLogLevels, ", "), cfg.logLevel)
	}

	return cfg, nil
}

func requireEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required but was not set", name)
	}
	return value, nil
}

func slices_contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// newLogger matches §3.4's shape exactly, including renaming slog's "msg" to
// "message" so Go and Ballerina logs are greppable with one expression.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.MessageKey {
				a.Key = "message"
			}
			return a
		},
	})
	return slog.New(handler).With("module", "kifaru/loan_api_go")
}
