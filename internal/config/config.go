package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	VaultAddr    string
	VaultToken   string
	AuthMethod   string // "token" or "kubernetes"
	K8sRole      string
	K8sMountPath string
	K8sTokenPath string
	ScanInterval time.Duration
	ScanTimeout  time.Duration
	HTTPPort     string
	LogLevel     string
	ConfigFile   string
}

// CompiledExclusions holds pre-compiled regexps for fast matching.
type CompiledExclusions struct {
	Keys         map[string]struct{}
	KeyPatterns  []*regexp.Regexp
	PathPatterns []*regexp.Regexp
}

// CompiledPair is a mount pair with its compiled exclusions, ready for scanning.
type CompiledPair struct {
	KV1        string
	KV2        string
	Exclusions *CompiledExclusions
}

// rawExcludeConfig mirrors the YAML "exclude" block inside a pair.
type rawExcludeConfig struct {
	Keys         []string `yaml:"keys"`
	KeyPatterns  []string `yaml:"key_patterns"`
	PathPatterns []string `yaml:"path_patterns"`
}

type rawPairConfig struct {
	KV1     string           `yaml:"kv1"`
	KV2     string           `yaml:"kv2"`
	Exclude rawExcludeConfig `yaml:"exclude"`
}

type rawPairsFile struct {
	Pairs []rawPairConfig `yaml:"pairs"`
}

func Load() (*Config, error) {
	interval, err := time.ParseDuration(getenv("SCAN_INTERVAL", "5m"))
	if err != nil {
		return nil, fmt.Errorf("invalid SCAN_INTERVAL: %w", err)
	}

	timeout, err := time.ParseDuration(getenv("SCAN_TIMEOUT", "4m"))
	if err != nil {
		return nil, fmt.Errorf("invalid SCAN_TIMEOUT: %w", err)
	}

	cfg := &Config{
		VaultAddr:    getenv("VAULT_ADDR", ""),
		VaultToken:   getenv("VAULT_TOKEN", ""),
		AuthMethod:   getenv("VAULT_AUTH_METHOD", "token"),
		K8sRole:      getenv("VAULT_K8S_ROLE", ""),
		K8sMountPath: getenv("VAULT_K8S_MOUNT", "kubernetes"),
		K8sTokenPath: getenv("VAULT_K8S_TOKEN_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/token"),
		ScanInterval: interval,
		ScanTimeout:  timeout,
		HTTPPort:     getenv("HTTP_PORT", "9090"),
		LogLevel:     getenv("LOG_LEVEL", "info"),
		ConfigFile:   getenv("CONFIG_FILE", "config.yaml"),
	}

	if cfg.VaultAddr == "" {
		return nil, fmt.Errorf("VAULT_ADDR is required")
	}
	switch cfg.AuthMethod {
	case "token":
		if cfg.VaultToken == "" {
			return nil, fmt.Errorf("VAULT_TOKEN is required when VAULT_AUTH_METHOD=token")
		}
	case "kubernetes":
		if cfg.K8sRole == "" {
			return nil, fmt.Errorf("VAULT_K8S_ROLE is required when VAULT_AUTH_METHOD=kubernetes")
		}
	default:
		return nil, fmt.Errorf("unknown VAULT_AUTH_METHOD=%q (allowed: token, kubernetes)", cfg.AuthMethod)
	}

	return cfg, nil
}

// LoadPairs reads the config file and returns compiled mount pairs.
// Returns an error if the file is missing, malformed, or defines no pairs.
// On hot-reload error the caller should log and continue with the previous pairs.
func LoadPairs(file string) ([]CompiledPair, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", file, err)
	}
	var pf rawPairsFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", file, err)
	}
	if len(pf.Pairs) == 0 {
		return nil, fmt.Errorf("config %s defines no pairs", file)
	}

	compiled := make([]CompiledPair, 0, len(pf.Pairs))
	for i, p := range pf.Pairs {
		if p.KV1 == "" || p.KV2 == "" {
			return nil, fmt.Errorf("pair[%d]: kv1 and kv2 are required", i)
		}
		ex, err := compileExclusions(p.Exclude)
		if err != nil {
			return nil, fmt.Errorf("pair[%d] (%s/%s): %w", i, p.KV1, p.KV2, err)
		}
		compiled = append(compiled, CompiledPair{KV1: p.KV1, KV2: p.KV2, Exclusions: ex})
	}
	return compiled, nil
}

func compileExclusions(raw rawExcludeConfig) (*CompiledExclusions, error) {
	ex := &CompiledExclusions{
		Keys: make(map[string]struct{}, len(raw.Keys)),
	}
	for _, k := range raw.Keys {
		ex.Keys[k] = struct{}{}
	}
	for _, p := range raw.KeyPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid key_pattern %q: %w", p, err)
		}
		ex.KeyPatterns = append(ex.KeyPatterns, re)
	}
	for _, p := range raw.PathPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid path_pattern %q: %w", p, err)
		}
		ex.PathPatterns = append(ex.PathPatterns, re)
	}
	return ex, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
