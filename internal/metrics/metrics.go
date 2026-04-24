package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"vault-kv-diff/internal/comparator"
)

type Metrics struct {
	duplicateKey  *prometheus.GaugeVec
	scanDuration  prometheus.Gauge
	scanTimestamp prometheus.Gauge
	scanErrors    prometheus.Counter
	pathsCompared *prometheus.GaugeVec

	mu         sync.Mutex
	activeKeys map[labelKey]struct{}
}

type labelKey struct {
	kv1, kv2, path, key string
}

func New(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		duplicateKey: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vault_kv_duplicate_key",
			Help: "1 if a secret key has the same value in both KV mounts",
		}, []string{"kv1", "kv2", "path", "key"}),

		scanDuration: f.NewGauge(prometheus.GaugeOpts{
			Name: "vault_kv_scan_duration_seconds",
			Help: "Duration of the last scan in seconds",
		}),

		scanTimestamp: f.NewGauge(prometheus.GaugeOpts{
			Name: "vault_kv_scan_last_timestamp_seconds",
			Help: "Unix timestamp of the last successful scan",
		}),

		scanErrors: f.NewCounter(prometheus.CounterOpts{
			Name: "vault_kv_scan_errors_total",
			Help: "Total number of scan errors",
		}),

		pathsCompared: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vault_kv_paths_compared_total",
			Help: "Number of paths compared in the last scan",
		}, []string{"kv1", "kv2"}),

		activeKeys: make(map[labelKey]struct{}),
	}
}

func (m *Metrics) RecordError() {
	m.scanErrors.Inc()
}

func (m *Metrics) RecordScan(result *comparator.Result, durationSecs, timestamp float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.scanDuration.Set(durationSecs)
	m.scanTimestamp.Set(timestamp)
	m.pathsCompared.WithLabelValues(result.KV1, result.KV2).Set(float64(result.PathsCompared))

	newKeys := make(map[labelKey]struct{}, len(result.Duplicates))
	for _, d := range result.Duplicates {
		newKeys[labelKey{kv1: d.KV1, kv2: d.KV2, path: d.Path, key: d.Key}] = struct{}{}
	}

	// Remove metrics that disappeared in the new scan
	for lbl := range m.activeKeys {
		if _, ok := newKeys[lbl]; !ok {
			m.duplicateKey.DeleteLabelValues(lbl.kv1, lbl.kv2, lbl.path, lbl.key)
		}
	}

	// Set metrics for found duplicates
	for lbl := range newKeys {
		m.duplicateKey.WithLabelValues(lbl.kv1, lbl.kv2, lbl.path, lbl.key).Set(1)
	}

	m.activeKeys = newKeys
}
