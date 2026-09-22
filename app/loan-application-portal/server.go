// server.go — the static file server for the console (BUILD-SPEC.md §7.5).
//
// §7.5 allows nginx-unprivileged or a small Go static file server. Go, because
// the container must run non-root on a READ-ONLY root filesystem, and nginx
// wants to write pid files, temp paths and logs.
//
// /config.js is generated IN MEMORY from $API_BASE_URL on every request rather
// than written to disk at startup. Two reasons: nothing can be written under a
// read-only filesystem, and generating it per request means the value is always
// whatever the environment currently says — no stale file to reason about.
//
// The API base URL is therefore never baked into the JS bundle. Changing
// API_BASE_URL and restarting the container is enough; no rebuild.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	log := newLogger(cfg.logLevel)
	slog.SetDefault(log)

	mux := http.NewServeMux()

	// Runtime configuration, generated per request (§7.5).
	mux.HandleFunc("GET /config.js", func(w http.ResponseWriter, r *http.Request) {
		body, err := json.Marshal(map[string]string{
			"apiBaseUrl": cfg.apiBaseURL,
			"brandColor": cfg.brandColor,
		})
		if err != nil {
			http.Error(w, "could not render config", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		// Never cached: a promoted image must pick up the new URL immediately.
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "window.__KIFARU_CONFIG__ = %s;\n", body)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	mux.Handle("/", spaHandler(cfg.rootDir))

	srv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", cfg.port), // §2.4
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("loan-application-portal started",
		"port", cfg.port,
		"api_base_url", cfg.apiBaseURL,
		"root_dir", cfg.rootDir,
		"log_level", cfg.logLevel)

	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Info("shutting down, draining for up to 10s")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("shutdown failed", "error", err)
		}
		close(idle)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "error", err)
		os.Exit(1)
	}
	<-idle
	log.Info("stopped")
}

// spaHandler serves the built bundle, falling back to index.html so client-side
// navigation survives a page refresh. Hashed assets are cached hard; index.html
// never is, or a promoted image would keep serving the previous bundle.
func spaHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(r.URL.Path)
		path := filepath.Join(root, clean)

		if info, err := os.Stat(path); err != nil || info.IsDir() {
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
			return
		}
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		files.ServeHTTP(w, r)
	})
}

type config struct {
	port       int
	apiBaseURL string
	rootDir    string
	logLevel   string
	brandColor string
}

func loadConfig() (config, error) {
	cfg := config{port: 8080, rootDir: "/app/dist", logLevel: "info"}

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

	// Optional. Empty means same-origin, which is what a reverse proxy in front
	// of both would want; compose sets it explicitly.
	cfg.apiBaseURL = strings.TrimRight(os.Getenv("API_BASE_URL"), "/")
	// Optional. Lets one image serve two deployments in different colours:
	// the SME cell runs amber, retail keeps the default navy. Runtime, like
	// API_BASE_URL, so nothing is baked into the bundle.
	cfg.brandColor = strings.TrimSpace(os.Getenv("BRAND_COLOR"))

	if raw := os.Getenv("STATIC_ROOT"); raw != "" {
		cfg.rootDir = raw
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

// Same JSON log shape as every other service (§3.4).
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
	return slog.New(handler).With("module", "kifaru/loan_application_portal")
}

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
