// config.go — environment configuration (BUILD-SPEC.md §3.2, §7.3).
//
// Env vars only, read and range-checked ONCE at startup. A required variable that
// is missing names itself and exits non-zero — loudly at startup, never silently
// at the first message.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type config struct {
	natsURL         string
	valkeyURL       string
	databaseURL     string
	paymentRailURL  string
	paymentRailKey  string
	idempotencyTTL  time.Duration
	adminPort       int
	logLevel        string
	workerInstance  string
	maxPaymentTries int
}

// §7.3 defaults.
const (
	defaultIdempotencyTTLHours = 168 // 7 days
	defaultAdminPort           = 9090
	// "Give up after 5 attempts" (§7.3).
	defaultMaxPaymentTries = 5
	// "nak the message with a 30-second backoff" (§7.3).
	nakBackoff = 30 * time.Second
)

func loadConfig() (config, error) {
	cfg := config{
		idempotencyTTL:  defaultIdempotencyTTLHours * time.Hour,
		adminPort:       defaultAdminPort,
		logLevel:        "info",
		maxPaymentTries: defaultMaxPaymentTries,
	}

	var err error
	// All injected by the platform in the OpenChoreo phase; by compose locally.
	if cfg.natsURL, err = requireEnv("NATS_URL"); err != nil {
		return cfg, err
	}
	if cfg.valkeyURL, err = requireEnv("VALKEY_URL"); err != nil {
		return cfg, err
	}
	if cfg.databaseURL, err = requireEnv("DATABASE_URL"); err != nil {
		return cfg, err
	}
	if cfg.paymentRailURL, err = requireEnv("PAYMENT_RAIL_URL"); err != nil {
		return cfg, err
	}
	// §7.3 lists this as injected from a secret. It is never logged.
	if cfg.paymentRailKey, err = requireEnv("PAYMENT_RAIL_API_KEY"); err != nil {
		return cfg, err
	}

	if raw := os.Getenv("IDEMPOTENCY_TTL_HOURS"); raw != "" {
		hours, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("IDEMPOTENCY_TTL_HOURS must be an integer, got %q", raw)
		}
		if hours < 1 {
			return cfg, fmt.Errorf("IDEMPOTENCY_TTL_HOURS must be greater than 0, got %d", hours)
		}
		cfg.idempotencyTTL = time.Duration(hours) * time.Hour
	}

	if raw := os.Getenv("ADMIN_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("ADMIN_PORT must be an integer, got %q", raw)
		}
		if p < 1 || p > 65535 {
			return cfg, fmt.Errorf("ADMIN_PORT must be between 1 and 65535, got %d", p)
		}
		cfg.adminPort = p
	}

	if raw := os.Getenv("LOG_LEVEL"); raw != "" {
		switch raw {
		case "debug", "info", "warn", "error":
			cfg.logLevel = raw
		default:
			return cfg, fmt.Errorf("LOG_LEVEL must be one of debug|info|warn|error, got %q", raw)
		}
	}

	// Identifies which worker holds a claim, for the `held_by` field in the
	// duplicate-suppression log line (§7.3). Defaults to the hostname, which in
	// Kubernetes is the pod name.
	cfg.workerInstance = os.Getenv("WORKER_INSTANCE")
	if cfg.workerInstance == "" {
		if host, err := os.Hostname(); err == nil {
			cfg.workerInstance = host
		} else {
			cfg.workerInstance = "worker"
		}
	}

	return cfg, nil
}

func requireEnv(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("%s is required but was not set", name)
	}
	return v, nil
}

// Produces the SAME JSON log shape as the Ballerina services (§3.4): Ballerina
// emits {"time","level","module","message",...}, so slog's "msg" is renamed and a
// "module" field added. A log pipeline that breaks at the language boundary is
// worse than none.
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
	return slog.New(handler).With("module", "kifaru/disbursement_worker")
}
