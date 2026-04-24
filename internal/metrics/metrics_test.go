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

func TestRecordScan_DuplicateCount(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordScan(resultWith(2, 10), 1.5, 1000.0)

	if got := testutil.ToFloat64(m.duplicateCount.WithLabelValues("stage", "prod")); got != 2 {
		t.Errorf("vault_kv_duplicate_count: expected 2, got %v", got)
	}
}

func TestRecordScan_PathsCompared(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordScan(resultWith(0, 7), 0, 0)

	if got := testutil.ToFloat64(m.pathsCompared.WithLabelValues("stage", "prod")); got != 7 {
		t.Errorf("vault_kv_paths_compared: expected 7, got %v", got)
	}
}

func TestRecordScan_Duration(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordScan(resultWith(0, 0), 3.14, 0)

	if got := testutil.ToFloat64(m.scanDuration); got != 3.14 {
		t.Errorf("vault_kv_scan_duration_seconds: expected 3.14, got %v", got)
	}
}

func TestRecordScan_Timestamp(t *testing.T) {
	m, _ := newTestMetrics()
	m.RecordScan(resultWith(0, 0), 0, 999.0)

	if got := testutil.ToFloat64(m.scanTimestamp); got != 999.0 {
		t.Errorf("vault_kv_scan_last_timestamp_seconds: expected 999, got %v", got)
	}
}

func TestRecordScan_DuplicateKeyGauge(t *testing.T) {
	m, _ := newTestMetrics()
	result := &comparator.Result{
		KV1: "stage", KV2: "prod",
		Duplicates: []comparator.DuplicateKey{
			{Path: "app/db", Key: "HOST", KV1: "stage", KV2: "prod"},
		},
	}
	m.RecordScan(result, 0, 0)

	if got := testutil.ToFloat64(m.duplicateKey.WithLabelValues("stage", "prod", "app/db", "HOST")); got != 1 {
		t.Errorf("vault_kv_duplicate_key: expected 1, got %v", got)
	}
}

func TestRecordScan_StaleKeyRemovedAfterNextScan(t *testing.T) {
	m, reg := newTestMetrics()

	first := &comparator.Result{
		KV1: "stage", KV2: "prod",
		Duplicates: []comparator.DuplicateKey{
			{Path: "app/db", Key: "HOST", KV1: "stage", KV2: "prod"},
		},
	}
	m.RecordScan(first, 0, 0)

	// second scan: duplicate resolved
	second := &comparator.Result{KV1: "stage", KV2: "prod", Duplicates: []comparator.DuplicateKey{}}
	m.RecordScan(second, 0, 0)

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

func TestRecordScan_DuplicateCountUpdatedAcrossScans(t *testing.T) {
	m, _ := newTestMetrics()

	m.RecordScan(resultWith(3, 0), 0, 0)
	if got := testutil.ToFloat64(m.duplicateCount.WithLabelValues("stage", "prod")); got != 3 {
		t.Fatalf("expected 3 after first scan, got %v", got)
	}

	m.RecordScan(resultWith(1, 0), 0, 0)
	if got := testutil.ToFloat64(m.duplicateCount.WithLabelValues("stage", "prod")); got != 1 {
		t.Errorf("expected 1 after second scan, got %v", got)
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
		dups[i] = comparator.DuplicateKey{KV1: "stage", KV2: "prod", Path: "app/db", Key: "KEY"}
	}
	return &comparator.Result{KV1: "stage", KV2: "prod", PathsCompared: p, Duplicates: dups}
}
