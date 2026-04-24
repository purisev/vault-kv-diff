package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/purisev/vault-kv-diff/internal/comparator"
	"github.com/purisev/vault-kv-diff/internal/config"
	"github.com/purisev/vault-kv-diff/internal/metrics"
	vclient "github.com/purisev/vault-kv-diff/internal/vault"
)

func main() {
	// Bootstrap logger before config is loaded.
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration error", "err", err)
		os.Exit(1)
	}

	log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))

	if cfg.ScanTimeout >= cfg.ScanInterval {
		log.Warn("SCAN_TIMEOUT >= SCAN_INTERVAL: a slow scan may delay the next tick",
			"scan_timeout", cfg.ScanTimeout, "scan_interval", cfg.ScanInterval)
	}

	ex, err := cfg.CompileExclusions()
	if err != nil {
		log.Error("failed to compile exclusions", "err", err)
		os.Exit(1)
	}

	client, err := vclient.New(cfg.VaultAddr, vclient.AuthConfig{
		Method:       vclient.AuthMethod(cfg.AuthMethod),
		Token:        cfg.VaultToken,
		K8sRole:      cfg.K8sRole,
		K8sMountPath: cfg.K8sMountPath,
		K8sTokenPath: cfg.K8sTokenPath,
	})
	if err != nil {
		log.Error("failed to initialize Vault client", "err", err)
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()
	m := metrics.New(reg)
	cmp := comparator.New(client, ex, cfg.KV1Mount, cfg.KV2Mount, log)

	// Pod is considered unhealthy if no scan has succeeded within this window.
	livenessThreshold := 3*cfg.ScanInterval + cfg.ScanTimeout

	var (
		mu              sync.RWMutex
		lastResult      *comparator.Result
		lastSuccessTime time.Time
	)

	runScan := func() {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ScanTimeout)
		defer cancel()

		if ex, err := config.LoadExclusions(cfg.ConfigFile); err != nil {
			log.Warn("failed to reload exclusions, using previous", "err", err)
		} else {
			cmp.SetExclusions(ex)
		}

		// Refresh Vault token before scan (relevant for kubernetes auth)
		if err := client.Refresh(ctx); err != nil {
			log.Error("failed to refresh Vault token", "err", err)
			m.RecordError()
			return
		}

		result, err := cmp.Compare(ctx)
		elapsed := time.Since(start).Seconds()
		if err != nil {
			log.Error("scan error", "err", err)
			m.RecordError()
			return
		}

		result.ScannedAt = time.Now().UTC()
		m.RecordScan(result, elapsed, float64(time.Now().Unix()))

		mu.Lock()
		lastResult = result
		lastSuccessTime = time.Now()
		mu.Unlock()
	}

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// /healthz — liveness probe.
	// Returns 503 only after at least one scan succeeded and the last success is stale.
	// During initial startup (before first scan) always returns 200.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		mu.RLock()
		t := lastSuccessTime
		mu.RUnlock()

		if !t.IsZero() && time.Since(t) > livenessThreshold {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("scan stale"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// /readyz — readiness probe.
	// Returns 503 until the first successful scan completes.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		mu.RLock()
		ready := !lastSuccessTime.IsZero()
		mu.RUnlock()

		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("waiting for first scan"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/report", func(w http.ResponseWriter, _ *http.Request) {
		mu.RLock()
		r := lastResult
		mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		if r == nil {
			_, _ = w.Write([]byte(`{"duplicates":[],"paths_compared":0}`))
			return
		}
		_ = json.NewEncoder(w).Encode(r)
	})

	srv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start HTTP server before the first scan so probes are available immediately.
	go func() {
		log.Info("HTTP server started", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server failed", "err", err)
			os.Exit(1)
		}
	}()

	// Run first scan immediately on startup.
	runScan()

	ticker := time.NewTicker(cfg.ScanInterval)
	defer ticker.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			runScan()
		case <-quit:
			log.Info("shutting down")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
			return
		}
	}
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
