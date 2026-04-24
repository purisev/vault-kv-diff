package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/purisev/vault-kv-diff/internal/comparator"
)

type Metrics struct {
	duplicateKey   *prometheus.GaugeVec
	duplicateCount *prometheus.GaugeVec
	scanDuration   prometheus.Gauge
	scanTimestamp  prometheus.Gauge
	scanErrors     prometheus.Counter
	pathsCompared  *prometheus.GaugeVec

	mu         sync.Mutex
	activeKeys map[mountPair]map[labelKey]struct{}
}

// mountPair identifies a specific kv1/kv2 combination for per-pair GC bookkeeping.
type mountPair struct{ kv1, kv2 string }

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

		duplicateCount: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vault_kv_duplicate_count",
			Help: "Total number of duplicate key-value pairs found in the last scan",
		}, []string{"kv1", "kv2"}),

		scanDuration: f.NewGauge(prometheus.GaugeOpts{
			Name: "vault_kv_scan_duration_seconds",
			Help: "Duration of the last scan cycle in seconds",
		}),

		scanTimestamp: f.NewGauge(prometheus.GaugeOpts{
			Name: "vault_kv_scan_last_timestamp_seconds",
			Help: "Unix timestamp of the last successful scan cycle",
		}),

		scanErrors: f.NewCounter(prometheus.CounterOpts{
			Name: "vault_kv_scan_errors_total",
			Help: "Total number of scan errors",
		}),

		pathsCompared: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vault_kv_paths_compared",
			Help: "Number of paths compared in the last scan",
		}, []string{"kv1", "kv2"}),

		activeKeys: make(map[mountPair]map[labelKey]struct{}),
	}
}

func (m *Metrics) RecordError() {
	m.scanErrors.Inc()
}

// RecordCycle records overall scan-cycle metrics (duration, timestamp).
// Called once per scan cycle after all pairs complete successfully.
func (m *Metrics) RecordCycle(durationSecs, timestamp float64) {
	m.scanDuration.Set(durationSecs)
	m.scanTimestamp.Set(timestamp)
}

// RecordPair records per-pair metrics and garbage-collects stale duplicate_key series
// for that specific pair. Safe to call concurrently for different pairs.
func (m *Metrics) RecordPair(result *comparator.Result) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pair := mountPair{kv1: result.KV1, kv2: result.KV2}

	m.pathsCompared.WithLabelValues(result.KV1, result.KV2).Set(float64(result.PathsCompared))
	m.duplicateCount.WithLabelValues(result.KV1, result.KV2).Set(float64(len(result.Duplicates)))

	newKeys := make(map[labelKey]struct{}, len(result.Duplicates))
	for _, d := range result.Duplicates {
		newKeys[labelKey{kv1: d.KV1, kv2: d.KV2, path: d.Path, key: d.Key}] = struct{}{}
	}

	// Remove series that disappeared in the new scan for this pair only.
	for lbl := range m.activeKeys[pair] {
		if _, ok := newKeys[lbl]; !ok {
			m.duplicateKey.DeleteLabelValues(lbl.kv1, lbl.kv2, lbl.path, lbl.key)
		}
	}

	for lbl := range newKeys {
		m.duplicateKey.WithLabelValues(lbl.kv1, lbl.kv2, lbl.path, lbl.key).Set(1)
	}

	m.activeKeys[pair] = newKeys
}
