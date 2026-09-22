// arrears-eod — the night shift (BUILD-SPEC.md §7.4).
//
// A batch job. It runs to completion and exits: 0 on success, non-zero on
// failure. It MUST NOT loop or sleep waiting for work — the platform schedules
// it as a CronJob, and a job that never exits is a job that never succeeds.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	started := time.Now()

	cfg, err := loadConfig()
	if err != nil {
		// Fail loudly at startup naming the variable (§3.2).
		return err
	}

	log := newLogger(cfg.logLevel)
	slog.SetDefault(log)

	asOf, err := ParseAsOfDate(cfg.asOfDate, time.Now().UTC())
	if err != nil {
		return err
	}

	// A batch job gets a bounded lifetime. If the database is wedged it should
	// fail the CronJob rather than hang until the next schedule collides with it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("DATABASE_URL is invalid: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("cannot reach the database: %w", err)
	}

	log.Info("arrears-eod starting",
		"as_of_date", asOf.Format("2006-01-02"),
		"dry_run", cfg.dryRun,
		"log_level", cfg.logLevel)

	rows, err := assess(ctx, pool, asOf)
	if err != nil {
		return err
	}

	SortWorstFirst(rows)

	// One JSON line per classification, so the portal can consume them (§7.4).
	// Every line carries application_id, as §2.5 requires of anything loan-related.
	for _, r := range rows {
		log.Info("loan classified",
			"application_id", r.ApplicationID,
			"applicant_name", r.ApplicantName,
			"outstanding_kes", r.OutstandingKES,
			"days_past_due", r.DaysPastDue,
			"bucket", r.Bucket,
			"bank_action", BankAction(r.Bucket),
			"as_of_date", asOf.Format("2006-01-02"))
	}

	if cfg.dryRun {
		log.Warn("DRY_RUN set — classifications were not written",
			"would_have_written", len(rows))
	} else {
		if err := upsert(ctx, pool, rows, asOf); err != nil {
			return err
		}
	}

	// The human-readable table goes to stdout after the JSON lines, so it is the
	// last thing on screen when the presenter runs this (§7.4).
	WriteReport(os.Stdout, rows, asOf, time.Since(started))

	log.Info("arrears-eod complete",
		"loans_assessed", len(rows),
		"elapsed_ms", time.Since(started).Milliseconds())
	return nil
}

// --- configuration (§7.4) ---------------------------------------------------

type config struct {
	databaseURL string
	asOfDate    string
	dryRun      bool
	logLevel    string
}

func loadConfig() (config, error) {
	cfg := config{logLevel: "info"}

	cfg.databaseURL = os.Getenv("DATABASE_URL")
	if cfg.databaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required but was not set")
	}

	// Optional. Empty means today — and §10.6 leaves it unset deliberately,
	// because the seed's due dates are relative to the day the seed ran.
	cfg.asOfDate = os.Getenv("AS_OF_DATE")

	switch raw := os.Getenv("DRY_RUN"); raw {
	case "", "false", "0":
		cfg.dryRun = false
	case "true", "1":
		cfg.dryRun = true
	default:
		return cfg, fmt.Errorf("DRY_RUN must be true or false, got %q", raw)
	}

	if raw := os.Getenv("LOG_LEVEL"); raw != "" {
		switch raw {
		case "debug", "info", "warn", "error":
			cfg.logLevel = raw
		default:
			return cfg, fmt.Errorf("LOG_LEVEL must be one of debug|info|warn|error, got %q", raw)
		}
	}

	return cfg, nil
}

// Same JSON log shape as the Ballerina services (§3.4).
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
	return slog.New(handler).With("module", "kifaru/arrears_eod")
}
