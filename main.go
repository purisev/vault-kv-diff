package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
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

	// Load initial pairs — required at startup.
	initialPairs, err := config.LoadPairs(cfg.ConfigFile)
	if err != nil {
		log.Error("failed to load pairs config", "err", err)
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

	// Pod is considered unhealthy if no scan has succeeded within this window.
	livenessThreshold := 3*cfg.ScanInterval + cfg.ScanTimeout

	var (
		mu              sync.RWMutex
		lastResults     []*comparator.Result
		lastSuccessTime time.Time
		currentPairs    = initialPairs
	)

	runScan := func() {
		// Reload pairs config before every scan; on error keep previous pairs.
		if newPairs, err := config.LoadPairs(cfg.ConfigFile); err != nil {
			log.Warn("failed to reload pairs config, using previous", "err", err)
		} else {
			mu.Lock()
			currentPairs = newPairs
			mu.Unlock()
		}

		mu.RLock()
		pairs := currentPairs
		mu.RUnlock()

		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ScanTimeout)
		defer cancel()

		// Refresh Vault token before scan (relevant for kubernetes auth).
		if err := client.Refresh(ctx); err != nil {
			log.Error("failed to refresh Vault token", "err", err)
			m.RecordError()
			return
		}

		results := make([]*comparator.Result, len(pairs))
		var (
			wg     sync.WaitGroup
			errMu  sync.Mutex
			anyErr bool
		)

		for i, pair := range pairs {
			wg.Add(1)
			go func(i int, pair config.CompiledPair) {
				defer wg.Done()
				cmp := comparator.New(client, pair.Exclusions, pair.KV1, pair.KV2, log)
				result, err := cmp.Compare(ctx)
				if err != nil {
					log.Error("scan error", "kv1", pair.KV1, "kv2", pair.KV2, "err", err)
					m.RecordError()
					errMu.Lock()
					anyErr = true
					errMu.Unlock()
					return
				}
				result.ScannedAt = time.Now().UTC()
				m.RecordPair(result)
				results[i] = result
			}(i, pair)
		}
		wg.Wait()

		if anyErr {
			return
		}

		m.RecordCycle(time.Since(start).Seconds(), float64(time.Now().Unix()))

		mu.Lock()
		lastResults = results
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
	// Returns 503 until all pairs complete their first successful scan.
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
		results := lastResults
		mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		if results == nil {
			_, _ = w.Write([]byte(`{"pairs":[]}`))
			return
		}

		pairs := make([]pairReport, 0, len(results))
		for _, r := range results {
			if r == nil {
				continue
			}
			paths := groupByPath(r.Duplicates)
			pairs = append(pairs, pairReport{
				KV1:           r.KV1,
				KV2:           r.KV2,
				Duplicates:    paths,
				PathsAffected: len(paths),
				PathsCompared: r.PathsCompared,
				ScannedAt:     r.ScannedAt,
			})
		}
		_ = json.NewEncoder(w).Encode(reportResponse{Pairs: pairs})
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

type reportPath struct {
	Path string   `json:"path"`
	Keys []string `json:"keys"`
}

type pairReport struct {
	KV1           string       `json:"kv1"`
	KV2           string       `json:"kv2"`
	Duplicates    []reportPath `json:"duplicates"`
	PathsAffected int          `json:"paths_affected"`
	PathsCompared int          `json:"paths_compared"`
	ScannedAt     time.Time    `json:"scanned_at"`
}

type reportResponse struct {
	Pairs []pairReport `json:"pairs"`
}

func groupByPath(dups []comparator.DuplicateKey) []reportPath {
	grouped := make(map[string]*reportPath, len(dups))
	for _, d := range dups {
		if _, ok := grouped[d.Path]; !ok {
			grouped[d.Path] = &reportPath{Path: d.Path}
		}
		grouped[d.Path].Keys = append(grouped[d.Path].Keys, d.Key)
	}
	paths := make([]reportPath, 0, len(grouped))
	for _, rp := range grouped {
		sort.Strings(rp.Keys)
		paths = append(paths, *rp)
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
	return paths
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
