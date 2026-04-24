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
	KV1Mount     string
	KV2Mount     string
	ScanInterval time.Duration
	ScanTimeout  time.Duration
	HTTPPort     string
	LogLevel     string
	ConfigFile   string
	Exclude      ExcludeConfig
}

type ExcludeConfig struct {
	Keys         []string `yaml:"keys"`
	KeyPatterns  []string `yaml:"key_patterns"`
	PathPatterns []string `yaml:"path_patterns"`
}

type fileConfig struct {
	Exclude ExcludeConfig `yaml:"exclude"`
}

// CompiledExclusions holds pre-compiled regexps for fast matching.
type CompiledExclusions struct {
	Keys         map[string]struct{}
	KeyPatterns  []*regexp.Regexp
	PathPatterns []*regexp.Regexp
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
		KV1Mount:     getenv("KV1_MOUNT", ""),
		KV2Mount:     getenv("KV2_MOUNT", ""),
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
	if cfg.KV1Mount == "" {
		return nil, fmt.Errorf("KV1_MOUNT is required")
	}
	if cfg.KV2Mount == "" {
		return nil, fmt.Errorf("KV2_MOUNT is required")
	}

	if err := loadFileConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) CompileExclusions() (*CompiledExclusions, error) {
	ex := &CompiledExclusions{
		Keys: make(map[string]struct{}, len(c.Exclude.Keys)),
	}
	for _, k := range c.Exclude.Keys {
		ex.Keys[k] = struct{}{}
	}
	for _, p := range c.Exclude.KeyPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid key_pattern %q: %w", p, err)
		}
		ex.KeyPatterns = append(ex.KeyPatterns, re)
	}
	for _, p := range c.Exclude.PathPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid path_pattern %q: %w", p, err)
		}
		ex.PathPatterns = append(ex.PathPatterns, re)
	}
	return ex, nil
}

// LoadExclusions reads the exclusions file and compiles its patterns.
// If the file does not exist, empty exclusions are returned.
// On error, the caller should log and continue with the previous exclusions.
func LoadExclusions(file string) (*CompiledExclusions, error) {
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return &CompiledExclusions{Keys: make(map[string]struct{})}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", file, err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", file, err)
	}
	return (&Config{Exclude: fc.Exclude}).CompileExclusions()
}

func loadFileConfig(cfg *Config) error {
	data, err := os.ReadFile(cfg.ConfigFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading config %s: %w", cfg.ConfigFile, err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return fmt.Errorf("parsing config %s: %w", cfg.ConfigFile, err)
	}
	cfg.Exclude = fc.Exclude
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
