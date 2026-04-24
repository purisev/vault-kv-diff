package config

import (
	"os"
	"path/filepath"
	"testing"
)

// --- LoadExclusions ---

func TestLoadExclusions_FileNotExist(t *testing.T) {
	ex, err := LoadExclusions("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ex.Keys) != 0 || len(ex.KeyPatterns) != 0 || len(ex.PathPatterns) != 0 {
		t.Fatal("expected empty exclusions for missing file")
	}
}

func TestLoadExclusions_ValidFile(t *testing.T) {
	content := `
exclude:
  keys:
    - env
    - namespace
  key_patterns:
    - "^APP_.*"
  path_patterns:
    - "^common/.*"
`
	ex, err := LoadExclusions(writeTempConfig(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := ex.Keys["env"]; !ok {
		t.Error("expected 'env' in keys")
	}
	if _, ok := ex.Keys["namespace"]; !ok {
		t.Error("expected 'namespace' in keys")
	}
	if len(ex.KeyPatterns) != 1 {
		t.Errorf("expected 1 key pattern, got %d", len(ex.KeyPatterns))
	}
	if len(ex.PathPatterns) != 1 {
		t.Errorf("expected 1 path pattern, got %d", len(ex.PathPatterns))
	}
}

func TestLoadExclusions_EmptyFile(t *testing.T) {
	ex, err := LoadExclusions(writeTempConfig(t, ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ex.Keys) != 0 || len(ex.KeyPatterns) != 0 || len(ex.PathPatterns) != 0 {
		t.Fatal("expected empty exclusions for empty file")
	}
}

func TestLoadExclusions_InvalidYAML(t *testing.T) {
	_, err := LoadExclusions(writeTempConfig(t, "{{invalid"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadExclusions_InvalidKeyPattern(t *testing.T) {
	content := `
exclude:
  key_patterns:
    - "["
`
	_, err := LoadExclusions(writeTempConfig(t, content))
	if err == nil {
		t.Fatal("expected error for invalid regex in key_patterns")
	}
}

func TestLoadExclusions_InvalidPathPattern(t *testing.T) {
	content := `
exclude:
  path_patterns:
    - "["
`
	_, err := LoadExclusions(writeTempConfig(t, content))
	if err == nil {
		t.Fatal("expected error for invalid regex in path_patterns")
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

func TestLoad_MissingKV1Mount(t *testing.T) {
	setRequiredEnv(t, "token")
	t.Setenv("KV1_MOUNT", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing KV1_MOUNT")
	}
}

func TestLoad_MissingKV2Mount(t *testing.T) {
	setRequiredEnv(t, "token")
	t.Setenv("KV2_MOUNT", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing KV2_MOUNT")
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

// --- helpers ---

func setRequiredEnv(t *testing.T, authMethod string) {
	t.Helper()
	t.Setenv("VAULT_ADDR", "https://vault.example.com")
	t.Setenv("VAULT_AUTH_METHOD", authMethod)
	t.Setenv("VAULT_TOKEN", "test-token")
	t.Setenv("VAULT_K8S_ROLE", "test-role")
	t.Setenv("KV1_MOUNT", "stage")
	t.Setenv("KV2_MOUNT", "prod")
	t.Setenv("SCAN_INTERVAL", "5m")
	t.Setenv("CONFIG_FILE", "/nonexistent/config.yaml")
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return f
}
