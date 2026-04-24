package comparator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/purisev/vault-kv-diff/internal/config"
)

type vaultClient interface {
	ListAllSecrets(ctx context.Context, mount string) ([]string, error)
	ReadSecretData(ctx context.Context, mount, path string) (map[string]interface{}, error)
}

type DuplicateKey struct {
	Path string `json:"path"`
	Key  string `json:"key"`
	KV1  string `json:"kv1"`
	KV2  string `json:"kv2"`
}

type Result struct {
	Duplicates    []DuplicateKey `json:"duplicates"`
	PathsCompared int            `json:"paths_compared"`
	KV1           string         `json:"kv1"`
	KV2           string         `json:"kv2"`
}

type Comparator struct {
	client vaultClient
	ex     *config.CompiledExclusions
	kv1    string
	kv2    string
	log    *slog.Logger
}

func New(client vaultClient, ex *config.CompiledExclusions, kv1, kv2 string, log *slog.Logger) *Comparator {
	return &Comparator{client: client, ex: ex, kv1: kv1, kv2: kv2, log: log}
}

func (c *Comparator) SetExclusions(ex *config.CompiledExclusions) {
	c.ex = ex
}

func (c *Comparator) Compare(ctx context.Context) (*Result, error) {
	c.log.Info("scan started", "kv1", c.kv1, "kv2", c.kv2)

	paths1, err := c.client.ListAllSecrets(ctx, c.kv1)
	if err != nil {
		return nil, fmt.Errorf("traversal %s: %w", c.kv1, err)
	}
	paths2, err := c.client.ListAllSecrets(ctx, c.kv2)
	if err != nil {
		return nil, fmt.Errorf("traversal %s: %w", c.kv2, err)
	}

	set2 := make(map[string]struct{}, len(paths2))
	for _, p := range paths2 {
		set2[p] = struct{}{}
	}

	var common []string
	for _, p := range paths1 {
		if _, ok := set2[p]; ok {
			common = append(common, p)
		}
	}

	c.log.Info("common paths found", "count", len(common))

	result := &Result{
		KV1:           c.kv1,
		KV2:           c.kv2,
		PathsCompared: len(common),
		Duplicates:    []DuplicateKey{},
	}

	for _, path := range common {
		if c.isPathExcluded(path) {
			c.log.Debug("path excluded", "path", path)
			continue
		}

		dups, err := c.compareSecret(ctx, path)
		if err != nil {
			c.log.Warn("failed to compare secret", "path", path, "err", err)
			continue
		}
		result.Duplicates = append(result.Duplicates, dups...)
	}

	c.log.Info("scan completed", "duplicates", len(result.Duplicates))
	return result, nil
}

func (c *Comparator) compareSecret(ctx context.Context, path string) ([]DuplicateKey, error) {
	data1, err := c.client.ReadSecretData(ctx, c.kv1, path)
	if err != nil {
		return nil, err
	}
	data2, err := c.client.ReadSecretData(ctx, c.kv2, path)
	if err != nil {
		return nil, err
	}

	var dups []DuplicateKey
	for key, val1 := range data1 {
		if c.isKeyExcluded(key) {
			continue
		}
		val2, exists := data2[key]
		if !exists {
			continue
		}
		// Compare string representations — KV v2 values are always strings
		if fmt.Sprintf("%v", val1) == fmt.Sprintf("%v", val2) {
			dups = append(dups, DuplicateKey{
				Path: path,
				Key:  key,
				KV1:  c.kv1,
				KV2:  c.kv2,
			})
		}
	}
	return dups, nil
}

func (c *Comparator) isPathExcluded(path string) bool {
	for _, re := range c.ex.PathPatterns {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

func (c *Comparator) isKeyExcluded(key string) bool {
	if _, ok := c.ex.Keys[key]; ok {
		return true
	}
	for _, re := range c.ex.KeyPatterns {
		if re.MatchString(key) {
			return true
		}
	}
	return false
}
