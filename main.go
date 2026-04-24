package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"vault-kv-diff/internal/comparator"
	"vault-kv-diff/internal/config"
	"vault-kv-diff/internal/metrics"
	vclient "vault-kv-diff/internal/vault"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration error", "err", err)
		os.Exit(1)
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

	var (
		mu         sync.RWMutex
		lastResult *comparator.Result
	)

	runScan := func() {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

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

		m.RecordScan(result, elapsed, float64(time.Now().Unix()))

		mu.Lock()
		lastResult = result
		mu.Unlock()
	}

	// Run first scan immediately on startup
	runScan()

	ticker := time.NewTicker(cfg.ScanInterval)
	defer ticker.Stop()

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
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

	go func() {
		log.Info("HTTP server started", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server failed", "err", err)
			os.Exit(1)
		}
	}()

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
