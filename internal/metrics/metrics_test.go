package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/purisev/vault-kv-diff/internal/comparator"
)

func newTestMetrics() (*Metrics, prometheus.Gatherer) {
	reg := prometheus.NewRegistry()
	return New(reg), reg
}

func TestRecordPair_DuplicateCount(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordPair(resultWith(2, 10))

	if got := testutil.ToFloat64(m.duplicateCount.WithLabelValues("alpha", "beta")); got != 2 {
		t.Errorf("vault_kv_duplicate_count: expected 2, got %v", got)
	}
}

func TestRecordPair_PathsCompared(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordPair(resultWith(0, 7))

	if got := testutil.ToFloat64(m.pathsCompared.WithLabelValues("alpha", "beta")); got != 7 {
		t.Errorf("vault_kv_paths_compared: expected 7, got %v", got)
	}
}

func TestRecordPair_DuplicateKeyGauge(t *testing.T) {
	m, _ := newTestMetrics()
	result := &comparator.Result{
		KV1: "alpha", KV2: "beta",
		Duplicates: []comparator.DuplicateKey{
			{Path: "app/db", Key: "HOST", KV1: "alpha", KV2: "beta"},
		},
	}
	m.RecordPair(result)

	if got := testutil.ToFloat64(m.duplicateKey.WithLabelValues("alpha", "beta", "app/db", "HOST")); got != 1 {
		t.Errorf("vault_kv_duplicate_key: expected 1, got %v", got)
	}
}

func TestRecordPair_StaleKeyRemovedAfterNextScan(t *testing.T) {
	m, reg := newTestMetrics()

	first := &comparator.Result{
		KV1: "alpha", KV2: "beta",
		Duplicates: []comparator.DuplicateKey{
			{Path: "app/db", Key: "HOST", KV1: "alpha", KV2: "beta"},
		},
	}
	m.RecordPair(first)

	second := &comparator.Result{KV1: "alpha", KV2: "beta", Duplicates: []comparator.DuplicateKey{}}
	m.RecordPair(second)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "vault_kv_duplicate_key" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "key" && lp.GetValue() == "HOST" {
					t.Error("stale duplicate_key series for HOST should have been deleted")
				}
			}
		}
	}
}

// Stale keys from one pair must not be GC'd when another pair is scanned.
func TestRecordPair_GCIsolatedBetweenPairs(t *testing.T) {
	m, _ := newTestMetrics()

	pairOne := &comparator.Result{
		KV1: "alpha", KV2: "beta",
		Duplicates: []comparator.DuplicateKey{
			{Path: "app/db", Key: "HOST", KV1: "alpha", KV2: "beta"},
		},
	}
	m.RecordPair(pairOne)

	// Scan a different pair — should not remove alpha/beta series.
	pairTwo := &comparator.Result{KV1: "gamma", KV2: "alpha", Duplicates: []comparator.DuplicateKey{}}
	m.RecordPair(pairTwo)

	if got := testutil.ToFloat64(m.duplicateKey.WithLabelValues("alpha", "beta", "app/db", "HOST")); got != 1 {
		t.Errorf("alpha/beta series should survive a scan of gamma/alpha pair, got %v", got)
	}
}

func TestRecordPair_DuplicateCountUpdatedAcrossScans(t *testing.T) {
	m, _ := newTestMetrics()

	m.RecordPair(resultWith(3, 0))
	if got := testutil.ToFloat64(m.duplicateCount.WithLabelValues("alpha", "beta")); got != 3 {
		t.Fatalf("expected 3 after first scan, got %v", got)
	}

	m.RecordPair(resultWith(1, 0))
	if got := testutil.ToFloat64(m.duplicateCount.WithLabelValues("alpha", "beta")); got != 1 {
		t.Errorf("expected 1 after second scan, got %v", got)
	}
}

func TestRecordCycle_Duration(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordCycle(3.14, 0)

	if got := testutil.ToFloat64(m.scanDuration); got != 3.14 {
		t.Errorf("vault_kv_scan_duration_seconds: expected 3.14, got %v", got)
	}
}

func TestRecordCycle_Timestamp(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordCycle(0, 999.0)

	if got := testutil.ToFloat64(m.scanTimestamp); got != 999.0 {
		t.Errorf("vault_kv_scan_last_timestamp_seconds: expected 999, got %v", got)
	}
}

func TestRecordError(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordError()
	m.RecordError()

	if got := testutil.ToFloat64(m.scanErrors); got != 2 {
		t.Errorf("vault_kv_scan_errors_total: expected 2, got %v", got)
	}
}

// resultWith builds a minimal Result with n duplicate keys and p paths compared.
func resultWith(n, p int) *comparator.Result {
	dups := make([]comparator.DuplicateKey, n)
	for i := range dups {
		dups[i] = comparator.DuplicateKey{KV1: "alpha", KV2: "beta", Path: "app/db", Key: "KEY"}
	}
	return &comparator.Result{KV1: "alpha", KV2: "beta", PathsCompared: p, Duplicates: dups}
}
