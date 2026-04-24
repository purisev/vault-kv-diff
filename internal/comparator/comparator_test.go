package comparator

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"testing"

	"github.com/purisev/vault-kv-diff/internal/config"
)

// mockVault implements vaultClient for tests.
type mockVault struct {
	paths   map[string][]string
	secrets map[string]map[string]map[string]interface{}
}

func (m *mockVault) ListAllSecrets(_ context.Context, mount string) ([]string, error) {
	return m.paths[mount], nil
}

func (m *mockVault) ReadSecretData(_ context.Context, mount, path string) (map[string]interface{}, error) {
	if data, ok := m.secrets[mount][path]; ok {
		return data, nil
	}
	return map[string]interface{}{}, nil
}

func emptyEx() *config.CompiledExclusions {
	return &config.CompiledExclusions{Keys: make(map[string]struct{})}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newCmp(mv *mockVault, ex *config.CompiledExclusions) *Comparator {
	return New(mv, ex, "kv1", "kv2", silentLogger())
}

// --- Compare ---

func TestCompare_FindsDuplicates(t *testing.T) {
	mv := &mockVault{
		paths: map[string][]string{
			"kv1": {"app/db"},
			"kv2": {"app/db"},
		},
		secrets: map[string]map[string]map[string]interface{}{
			"kv1": {"app/db": {"HOST": "db.example.com", "PORT": "5432"}},
			"kv2": {"app/db": {"HOST": "db.example.com", "PORT": "6432"}},
		},
	}
	result, err := newCmp(mv, emptyEx()).Compare(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate, got %d", len(result.Duplicates))
	}
	if result.Duplicates[0].Key != "HOST" {
		t.Errorf("expected duplicate key HOST, got %s", result.Duplicates[0].Key)
	}
}

func TestCompare_NoDuplicatesWhenValuesDiffer(t *testing.T) {
	mv := &mockVault{
		paths: map[string][]string{
			"kv1": {"app/db"},
			"kv2": {"app/db"},
		},
		secrets: map[string]map[string]map[string]interface{}{
			"kv1": {"app/db": {"HOST": "db-a.example.com"}},
			"kv2": {"app/db": {"HOST": "db-b.example.com"}},
		},
	}
	result, err := newCmp(mv, emptyEx()).Compare(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Duplicates) != 0 {
		t.Errorf("expected no duplicates, got %d", len(result.Duplicates))
	}
}

func TestCompare_OnlyCommonPaths(t *testing.T) {
	mv := &mockVault{
		paths: map[string][]string{
			"kv1": {"app/db", "app/redis"},
			"kv2": {"app/db"},
		},
		secrets: map[string]map[string]map[string]interface{}{
			"kv1": {
				"app/db":    {"HOST": "same"},
				"app/redis": {"URL": "same"},
			},
			"kv2": {"app/db": {"HOST": "same"}},
		},
	}
	result, err := newCmp(mv, emptyEx()).Compare(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PathsCompared != 1 {
		t.Errorf("expected 1 path compared, got %d", result.PathsCompared)
	}
}

func TestCompare_SkipsExcludedKey(t *testing.T) {
	mv := &mockVault{
		paths: map[string][]string{
			"kv1": {"app/cfg"},
			"kv2": {"app/cfg"},
		},
		secrets: map[string]map[string]map[string]interface{}{
			"kv1": {"app/cfg": {"env": "value", "HOST": "same"}},
			"kv2": {"app/cfg": {"env": "value", "HOST": "same"}},
		},
	}
	ex := &config.CompiledExclusions{
		Keys: map[string]struct{}{"env": {}},
	}
	result, err := newCmp(mv, ex).Compare(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range result.Duplicates {
		if d.Key == "env" {
			t.Error("excluded key 'env' should not appear in duplicates")
		}
	}
}

func TestCompare_SkipsExcludedPath(t *testing.T) {
	mv := &mockVault{
		paths: map[string][]string{
			"kv1": {"common/shared", "app/db"},
			"kv2": {"common/shared", "app/db"},
		},
		secrets: map[string]map[string]map[string]interface{}{
			"kv1": {
				"common/shared": {"KEY": "value"},
				"app/db":        {"HOST": "value-a"},
			},
			"kv2": {
				"common/shared": {"KEY": "value"},
				"app/db":        {"HOST": "value-b"},
			},
		},
	}
	ex := &config.CompiledExclusions{
		Keys:         make(map[string]struct{}),
		PathPatterns: []*regexp.Regexp{regexp.MustCompile(`^common/.*`)},
	}
	result, err := newCmp(mv, ex).Compare(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Duplicates) != 0 {
		t.Errorf("expected no duplicates, got %d", len(result.Duplicates))
	}
}

func TestCompare_EmptyMounts(t *testing.T) {
	mv := &mockVault{
		paths:   map[string][]string{"kv1": {}, "kv2": {}},
		secrets: map[string]map[string]map[string]interface{}{},
	}
	result, err := newCmp(mv, emptyEx()).Compare(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Duplicates) != 0 || result.PathsCompared != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

// --- isKeyExcluded ---

func TestIsKeyExcluded_ExactMatch(t *testing.T) {
	c := &Comparator{ex: &config.CompiledExclusions{
		Keys: map[string]struct{}{"env": {}},
	}}
	if !c.isKeyExcluded("env") {
		t.Error("expected 'env' to be excluded")
	}
	if c.isKeyExcluded("environment") {
		t.Error("expected 'environment' not to be excluded")
	}
}

func TestIsKeyExcluded_PatternMatch(t *testing.T) {
	c := &Comparator{ex: &config.CompiledExclusions{
		Keys:        make(map[string]struct{}),
		KeyPatterns: []*regexp.Regexp{regexp.MustCompile(`^APP_.*`)},
	}}
	if !c.isKeyExcluded("APP_ENV") {
		t.Error("expected APP_ENV to be excluded")
	}
	if c.isKeyExcluded("DB_HOST") {
		t.Error("expected DB_HOST not to be excluded")
	}
}

func TestIsKeyExcluded_NoMatch(t *testing.T) {
	c := &Comparator{ex: emptyEx()}
	if c.isKeyExcluded("ANYTHING") {
		t.Error("expected no exclusions with empty config")
	}
}

// --- isPathExcluded ---

func TestIsPathExcluded_Match(t *testing.T) {
	c := &Comparator{ex: &config.CompiledExclusions{
		Keys:         make(map[string]struct{}),
		PathPatterns: []*regexp.Regexp{regexp.MustCompile(`^shared/.*`)},
	}}
	if !c.isPathExcluded("shared/config") {
		t.Error("expected shared/config to be excluded")
	}
}

func TestIsPathExcluded_NoMatch(t *testing.T) {
	c := &Comparator{ex: &config.CompiledExclusions{
		Keys:         make(map[string]struct{}),
		PathPatterns: []*regexp.Regexp{regexp.MustCompile(`^shared/.*`)},
	}}
	if c.isPathExcluded("app/config") {
		t.Error("expected app/config not to be excluded")
	}
}
