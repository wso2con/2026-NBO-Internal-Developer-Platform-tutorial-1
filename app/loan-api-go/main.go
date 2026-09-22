// loan-api-go — the public API, in Go (BUILD-SPEC.md §7.1).
//
// A second implementation of loan-api, wire-identical to the Ballerina one. It
// exists because the Ballerina buildpack takes ~10 minutes on a cold build, which
// is too long to run live on stage; this builds from a Dockerfile in well under a
// minute. Both are kept: the Ballerina service is the one to show when the point
// is the platform building from source, this one when the point is the
// application.
//
// Only one of the two should be deployed into an environment at a time — they
// share a database and would both consume the same application ids.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	// distroless has no shell, wget or curl, so the container probes itself:
	// `loan-api-go -healthcheck` is the Dockerfile HEALTHCHECK. Same trick as
	// disbursement-worker.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		// Configuration errors must be visible before any listener binds (§3.2),
		// and on stderr in plain text: the logger may not exist yet.
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	log := newLogger(cfg.logLevel)

	a := &api{
		cfg:    cfg,
		log:    log,
		db:     newStore(cfg.databaseURL),
		events: newPublisher(cfg.natsURL),
		score:  newScorer(cfg.creditScoringURL, cfg.scoringTimeout),
	}
	defer a.db.close()
	defer a.events.close()

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.port),
		Handler:           a.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Resolved config on one line so the presenter can prove which database and
	// which scoring endpoint this pod actually came up against. No password is
	// logged (§3.2 — secrets redacted): DATABASE_URL contains one, so it is not
	// echoed, only the host it points at.
	log.Info("loan-api-go started",
		"port", cfg.port,
		"credit_scoring_url", cfg.creditScoringURL,
		"scoring_timeout_ms", cfg.scoringTimeout.Milliseconds(),
		"log_level", cfg.logLevel)

	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errc:
		return err
	case <-stop:
		// 10-second drain on SIGTERM (§2.8), matching the Ballerina listener.
		log.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

// healthcheck hits /healthz on the configured port and reports 0 or 1. Liveness
// only: it deliberately does NOT call /readyz, so a database blip cannot make
// Docker or Kubernetes kill a process that is running perfectly well.
func healthcheck() int {
	port := defaultPort
	if raw := os.Getenv("PORT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			port = parsed
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
