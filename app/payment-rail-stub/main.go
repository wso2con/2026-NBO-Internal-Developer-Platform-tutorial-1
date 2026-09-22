// payment-rail-stub — a fake mobile money provider (BUILD-SPEC.md §7.6).
//
// Not a demo component. It stands in for an external payment API so the
// disbursement-worker has something to call, and §7.6 says to keep it trivial.
// Standard library only: net/http with ServeMux, no framework (§3.3), and UUIDs
// from crypto/rand rather than a dependency.
//
// Deliberately no Prometheus metrics here. §3.3 mandates them for Go services, but
// §7.6 overrides for this one: it is not a demo component and is not scraped.
// disbursement-worker and arrears-eod get the full treatment.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type paymentRequest struct {
	ApplicationID string  `json:"application_id"`
	AmountKES     float64 `json:"amount_kes"`
	WalletMSISDN  string  `json:"wallet_msisdn"`
}

type paymentResponse struct {
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"`
	Duplicate   bool   `json:"duplicate,omitempty"`
}

// Idempotency store. In-memory on purpose: this is a stub, and a restart losing
// its history is fine. The real idempotency guarantee the demo cares about lives
// in the worker's Valkey claim (§7.3), not here.
type ledger struct {
	mu   sync.Mutex
	refs map[string]string // idempotency key -> provider_ref
}

func (l *ledger) getOrCreate(key, ref string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.refs[key]; ok {
		return existing, true
	}
	l.refs[key] = ref
	return ref, false
}

type config struct {
	port     int
	failRate float64
	latency  time.Duration
	logLevel string
}

func main() {
	// The distroless runtime image has no shell and no wget, so the Dockerfile's
	// HEALTHCHECK re-executes this binary with -healthcheck to probe itself.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}

	cfg, err := loadConfig()
	if err != nil {
		// Fail loudly at startup naming the variable, never silently at first
		// request (§3.2 — the same contract the Ballerina services follow).
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.logLevel)
	slog.SetDefault(logger)

	store := &ledger{refs: make(map[string]string)}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/payments", handlePayment(store, cfg))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.port), // §2.4 — bind 0.0.0.0
		Handler: mux,
	}

	slog.Info("payment-rail-stub started",
		"port", cfg.port,
		"fail_rate", cfg.failRate,
		"latency_ms", cfg.latency.Milliseconds(),
		"log_level", cfg.logLevel)

	// Graceful shutdown, 10-second drain (§2.8).
	idle := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, syscall.SIGTERM, syscall.SIGINT)
		<-sigint
		slog.Info("shutting down, draining for up to 10s")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("shutdown failed", "error", err)
		}
		close(idle)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
	<-idle
	slog.Info("stopped")
}

func handlePayment(store *ledger, cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"message": "Idempotency-Key header is required"})
			return
		}

		var req paymentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"message": "malformed JSON body"})
			return
		}

		// Simulated provider latency, so the worker's async behaviour is visible
		// rather than instantaneous (§7.6).
		if cfg.latency > 0 {
			time.Sleep(cfg.latency)
		}

		// Simulated failures, for exercising the worker's retry path (§7.3).
		if cfg.failRate > 0 && randomFloat() < cfg.failRate {
			slog.Warn("payment failed (simulated)",
				"application_id", req.ApplicationID, "idempotency_key", key)
			writeJSON(w, http.StatusInternalServerError,
				map[string]string{"message": "provider error (simulated)"})
			return
		}

		ref, duplicate := store.getOrCreate(key, "PR-"+newUUID())

		// Same key twice returns the SAME provider_ref with duplicate:true and a
		// 200 rather than a 201 (§7.6).
		status := http.StatusCreated
		if duplicate {
			status = http.StatusOK
			slog.Info("duplicate payment request",
				"application_id", req.ApplicationID, "provider_ref", ref)
		} else {
			slog.Info("payment sent",
				"application_id", req.ApplicationID,
				"provider_ref", ref,
				"amount_kes", req.AmountKES)
		}

		writeJSON(w, status, paymentResponse{
			ProviderRef: ref,
			Status:      "SENT",
			Duplicate:   duplicate,
		})
	}
}

func loadConfig() (config, error) {
	cfg := config{port: 8080, failRate: 0, latency: 150 * time.Millisecond, logLevel: "info"}

	// §2.4: default 8080 when PORT is unset. Compose maps host 8082 to it (§9.2).
	if raw := os.Getenv("PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("PORT must be an integer, got %q", raw)
		}
		if p < 1 || p > 65535 {
			return cfg, fmt.Errorf("PORT must be between 1 and 65535, got %d", p)
		}
		cfg.port = p
	}

	if raw := os.Getenv("FAIL_RATE"); raw != "" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return cfg, fmt.Errorf("FAIL_RATE must be a number, got %q", raw)
		}
		if f < 0 || f > 1 {
			return cfg, fmt.Errorf("FAIL_RATE must be between 0 and 1, got %v", f)
		}
		cfg.failRate = f
	}

	if raw := os.Getenv("LATENCY_MS"); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("LATENCY_MS must be an integer, got %q", raw)
		}
		if ms < 0 {
			return cfg, fmt.Errorf("LATENCY_MS must not be negative, got %d", ms)
		}
		cfg.latency = time.Duration(ms) * time.Millisecond
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

// Produces the SAME JSON log shape as the Ballerina services (§3.4): a trace or a
// log pipeline that breaks at the language boundary is worse than none. Ballerina
// emits {"time","level","module","message",...}, so slog's "msg" is renamed and a
// "module" field added.
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
	return slog.New(handler).With("module", "kifaru/payment_rail_stub")
}

// Probes the local /healthz endpoint. Returns a process exit code: 0 healthy,
// 1 otherwise. Used only by the Dockerfile HEALTHCHECK.
func runHealthcheck() int {
	port := 8080
	if raw := os.Getenv("PORT"); raw != "" {
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randomFloat() float64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return 1 // fail closed: on RNG error, do not simulate a failure
	}
	return float64(n.Int64()) / 1_000_000
}
