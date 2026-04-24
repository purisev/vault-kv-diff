package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- LoadPairs ---

func TestLoadPairs_FileNotExist(t *testing.T) {
	_, err := LoadPairs("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadPairs_InvalidYAML(t *testing.T) {
	_, err := LoadPairs(writeTempConfig(t, "{{invalid"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadPairs_NoPairs(t *testing.T) {
	_, err := LoadPairs(writeTempConfig(t, "pairs: []"))
	if err == nil {
		t.Fatal("expected error when no pairs defined")
	}
}

func TestLoadPairs_MissingKV1(t *testing.T) {
	content := `
pairs:
  - kv2: beta
`
	_, err := LoadPairs(writeTempConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing kv1")
	}
}

func TestLoadPairs_MissingKV2(t *testing.T) {
	content := `
pairs:
  - kv1: alpha
`
	_, err := LoadPairs(writeTempConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing kv2")
	}
}

func TestLoadPairs_ValidFile(t *testing.T) {
	content := `
pairs:
  - kv1: alpha
    kv2: beta
    exclude:
      keys:
        - env
        - namespace
      key_patterns:
        - "^APP_.*"
      path_patterns:
        - "^common/.*"
  - kv1: gamma
    kv2: alpha
`
	pairs, err := LoadPairs(writeTempConfig(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}

	p := pairs[0]
	if p.KV1 != "alpha" || p.KV2 != "beta" {
		t.Errorf("pair[0]: expected alpha/beta, got %s/%s", p.KV1, p.KV2)
	}
	if _, ok := p.Exclusions.Keys["env"]; !ok {
		t.Error("expected 'env' in pair[0] excluded keys")
	}
	if len(p.Exclusions.KeyPatterns) != 1 {
		t.Errorf("expected 1 key pattern in pair[0], got %d", len(p.Exclusions.KeyPatterns))
	}
	if len(p.Exclusions.PathPatterns) != 1 {
		t.Errorf("expected 1 path pattern in pair[0], got %d", len(p.Exclusions.PathPatterns))
	}

	p2 := pairs[1]
	if p2.KV1 != "gamma" || p2.KV2 != "alpha" {
		t.Errorf("pair[1]: expected gamma/alpha, got %s/%s", p2.KV1, p2.KV2)
	}
	if len(p2.Exclusions.Keys) != 0 || len(p2.Exclusions.KeyPatterns) != 0 {
		t.Error("pair[1] should have no exclusions")
	}
}

func TestLoadPairs_InvalidKeyPattern(t *testing.T) {
	content := `
pairs:
  - kv1: alpha
    kv2: beta
    exclude:
      key_patterns:
        - "["
`
	_, err := LoadPairs(writeTempConfig(t, content))
	if err == nil {
		t.Fatal("expected error for invalid key_pattern")
	}
}

func TestLoadPairs_InvalidPathPattern(t *testing.T) {
	content := `
pairs:
  - kv1: alpha
    kv2: beta
    exclude:
      path_patterns:
        - "["
`
	_, err := LoadPairs(writeTempConfig(t, content))
	if err == nil {
		t.Fatal("expected error for invalid path_pattern")
	}
}

// --- Load validation ---

func TestLoad_MissingVaultAddr(t *testing.T) {
	setRequiredEnv(t, "token")
	t.Setenv("VAULT_ADDR", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing VAULT_ADDR")
	}
}

func TestLoad_TokenAuth_MissingToken(t *testing.T) {
	setRequiredEnv(t, "token")
	t.Setenv("VAULT_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing VAULT_TOKEN")
	}
}

func TestLoad_K8sAuth_MissingRole(t *testing.T) {
	setRequiredEnv(t, "kubernetes")
	t.Setenv("VAULT_K8S_ROLE", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing VAULT_K8S_ROLE")
	}
}

func TestLoad_UnknownAuthMethod(t *testing.T) {
	setRequiredEnv(t, "ldap")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for unknown auth method")
	}
}

func TestLoad_InvalidScanInterval(t *testing.T) {
	setRequiredEnv(t, "token")
	t.Setenv("SCAN_INTERVAL", "notaduration")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid SCAN_INTERVAL")
	}
}

func TestLoad_InvalidScanTimeout(t *testing.T) {
	setRequiredEnv(t, "token")
	t.Setenv("SCAN_TIMEOUT", "notaduration")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid SCAN_TIMEOUT")
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	setRequiredEnv(t, "token")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ScanInterval != 5*time.Minute {
		t.Errorf("expected default ScanInterval 5m, got %v", cfg.ScanInterval)
	}
	if cfg.ScanTimeout != 4*time.Minute {
		t.Errorf("expected default ScanTimeout 4m, got %v", cfg.ScanTimeout)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel info, got %s", cfg.LogLevel)
	}
}

// --- helpers ---

func setRequiredEnv(t *testing.T, authMethod string) {
	t.Helper()
	t.Setenv("VAULT_ADDR", "https://vault.example.com")
	t.Setenv("VAULT_AUTH_METHOD", authMethod)
	t.Setenv("VAULT_TOKEN", "test-token")
	t.Setenv("VAULT_K8S_ROLE", "test-role")
	t.Setenv("SCAN_INTERVAL", "5m")
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return f
}
