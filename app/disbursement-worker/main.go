// disbursement-worker — the money mover (BUILD-SPEC.md §7.3).
//
// A JetStream consumer, not a service. There is NO HTTP server on a main port:
// the component declares no service endpoint. Probes and metrics live on a
// separate ADMIN_PORT (default 9090), because the platform still needs them.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// The distroless runtime image has no shell, so the Dockerfile's HEALTHCHECK
	// re-executes this binary with -healthcheck to probe its own admin port.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}

	cfg, err := loadConfig()
	if err != nil {
		// Fail loudly at startup naming the variable (§3.2).
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	log := newLogger(cfg.logLevel)
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("worker stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run(cfg config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// --- dependencies --------------------------------------------------------
	claims, err := newClaimStore(cfg.valkeyURL, cfg.idempotencyTTL)
	if err != nil {
		return err
	}
	defer func() { _ = claims.close() }()

	db, err := newStore(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer db.close()

	nc, err := nats.Connect(cfg.natsURL,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second))
	if err != nil {
		return fmt.Errorf("could not connect to NATS: %w", err)
	}
	defer nc.Drain() //nolint:errcheck

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("could not open JetStream: %w", err)
	}

	registry := prometheus.NewRegistry()
	w := &worker{
		cfg:      cfg,
		claims:   claims,
		payments: newPaymentClient(cfg.paymentRailURL, cfg.paymentRailKey),
		db:       db,
		events:   newPublisher(nc),
		metrics:  newMetrics(registry),
		log:      log,
	}

	// --- admin server (§7.3) -------------------------------------------------
	var ready atomic.Bool
	adminSrv := startAdminServer(cfg, registry, claims, db, &ready, log)

	// --- the LOANS stream (§8) -----------------------------------------------
	//
	// Same stream nats-init creates in docker-compose (loan.>, file storage,
	// 24h limits retention). CreateOrUpdateStream is idempotent, so the worker
	// can own this on any platform: locally it is a no-op after nats-init; on
	// OpenChoreo, where the nats Resource starts empty, the first worker to
	// come up creates it. Without the stream the consumer below cannot exist.
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "LOANS",
		Subjects:  []string{"loan.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    24 * time.Hour,
	}); err != nil {
		return fmt.Errorf("could not create the LOANS stream: %w", err)
	}

	// --- the durable consumer (§8) -------------------------------------------
	//
	// Durable, so a restart resumes where it left off rather than losing
	// messages — which is the point of using a broker at all.
	//
	// DeliverNew applies ONLY when the consumer is first created. Without it a
	// brand-new worker would replay the entire 24h retention window, disbursing
	// loans from old test runs. After creation the durable cursor governs, so
	// restarts still lose nothing.
	consumer, err := js.CreateOrUpdateConsumer(ctx, "LOANS", jetstream.ConsumerConfig{
		Durable:       "disbursement",
		FilterSubject: "loan.approved",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckWait:       60 * time.Second,
		MaxDeliver:    cfg.maxPaymentTries,
	})
	if err != nil {
		return fmt.Errorf("could not create the 'disbursement' consumer: %w", err)
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		// Each message gets its own timeout so one stuck payment cannot wedge
		// the consumer.
		msgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		w.handle(msgCtx, msg)
	})
	if err != nil {
		return fmt.Errorf("could not start consuming: %w", err)
	}
	defer consumeCtx.Stop()

	ready.Store(true)
	log.Info("disbursement-worker started",
		"nats_url", cfg.natsURL,
		"valkey_url", cfg.valkeyURL,
		"payment_rail_url", cfg.paymentRailURL,
		"admin_port", cfg.adminPort,
		"idempotency_ttl_hours", int(cfg.idempotencyTTL.Hours()),
		"worker_instance", cfg.workerInstance,
		"log_level", cfg.logLevel)
	// PAYMENT_RAIL_API_KEY is deliberately absent from that line — secrets are
	// redacted from the startup log (§3.2).

	// --- shutdown (§2.8) -----------------------------------------------------
	<-ctx.Done()
	log.Info("shutting down, draining for up to 10s")
	ready.Store(false)

	consumeCtx.Drain()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("admin server shutdown failed", "error", err)
	}

	log.Info("stopped")
	return nil
}

// Probes the local admin port. Returns a process exit code: 0 healthy, 1 not.
// Used only by the Dockerfile HEALTHCHECK.
func runHealthcheck() int {
	port := defaultAdminPort
	if raw := os.Getenv("ADMIN_PORT"); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil {
			port = p
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// The platform needs probes and metrics even though this component exposes no
// service endpoint, so they live on their own port (§7.3).
func startAdminServer(cfg config, registry *prometheus.Registry, claims *claimStore,
	db *store, ready *atomic.Bool, log *slog.Logger) *http.Server {

	mux := http.NewServeMux()

	// Liveness: the process is up. No dependency checks (§7.1's rule).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Readiness: consuming, and the dependencies that must work are reachable.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		w.Header().Set("Content-Type", "application/json")
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not ready","reason":"not consuming"}`))
			return
		}
		if err := claims.ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not ready","dependency":"valkey"}`))
			return
		}
		if err := db.ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not ready","dependency":"database"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	srv := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.adminPort),
		Handler: mux,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("admin server failed", "error", err)
		}
	}()
	return srv
}
