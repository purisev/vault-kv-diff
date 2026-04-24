package vault

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	vault "github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
)

type AuthMethod string

const (
	AuthMethodToken      AuthMethod = "token"
	AuthMethodKubernetes AuthMethod = "kubernetes"
)

type AuthConfig struct {
	Method AuthMethod

	// token auth
	Token string

	// kubernetes auth
	K8sRole      string
	K8sMountPath string // auth backend mount path, default "kubernetes"
	K8sTokenPath string // path to SA token inside the pod
}

type Client struct {
	vc   *vault.Client
	auth AuthConfig
}

func New(addr string, auth AuthConfig) (*Client, error) {
	vc, err := vault.New(
		vault.WithAddress(addr),
		vault.WithRequestTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("client initialization: %w", err)
	}

	c := &Client{vc: vc, auth: auth}
	if err := c.authenticate(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

// Refresh re-authenticates with Vault.
// For kubernetes auth it is called before each scan — the SA token is always fresh.
// For token auth it is a no-op.
func (c *Client) Refresh(ctx context.Context) error {
	if c.auth.Method != AuthMethodKubernetes {
		return nil
	}
	return c.kubernetesLogin(ctx)
}

func (c *Client) authenticate(ctx context.Context) error {
	switch c.auth.Method {
	case AuthMethodToken:
		return c.vc.SetToken(c.auth.Token)
	case AuthMethodKubernetes:
		return c.kubernetesLogin(ctx)
	default:
		return fmt.Errorf("unknown authentication method: %s", c.auth.Method)
	}
}

func (c *Client) kubernetesLogin(ctx context.Context) error {
	jwt, err := os.ReadFile(c.auth.K8sTokenPath)
	if err != nil {
		return fmt.Errorf("reading SA token %s: %w", c.auth.K8sTokenPath, err)
	}

	resp, err := c.vc.Auth.KubernetesLogin(ctx,
		schema.KubernetesLoginRequest{
			Jwt:  string(jwt),
			Role: c.auth.K8sRole,
		},
		vault.WithMountPath(c.auth.K8sMountPath),
	)
	if err != nil {
		return fmt.Errorf("kubernetes login: %w", err)
	}
	if resp.Auth == nil {
		return fmt.Errorf("kubernetes login: empty auth in Vault response")
	}
	return c.vc.SetToken(resp.Auth.ClientToken)
}

// ListAllSecrets recursively traverses a KV v2 mount and returns all secret paths.
func (c *Client) ListAllSecrets(ctx context.Context, mount string) ([]string, error) {
	return c.listRecursive(ctx, mount, "")
}

func (c *Client) listRecursive(ctx context.Context, mount, path string) ([]string, error) {
	resp, err := c.vc.Secrets.KvV2List(ctx, path, vault.WithMountPath(mount))
	if err != nil {
		// Empty mount or non-existent path — not an error
		if vault.IsErrorStatus(err, http.StatusNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("list %s/%s: %w", mount, path, err)
	}

	var result []string
	for _, key := range resp.Data.Keys {
		full := path + key
		if strings.HasSuffix(key, "/") {
			sub, err := c.listRecursive(ctx, mount, full)
			if err != nil {
				return nil, err
			}
			result = append(result, sub...)
		} else {
			result = append(result, full)
		}
	}
	return result, nil
}

// ReadSecretData reads a KV v2 secret and returns its data (key → value).
func (c *Client) ReadSecretData(ctx context.Context, mount, path string) (map[string]interface{}, error) {
	resp, err := c.vc.Secrets.KvV2Read(ctx, path, vault.WithMountPath(mount))
	if err != nil {
		return nil, fmt.Errorf("read %s/%s: %w", mount, path, err)
	}
	if resp.Data.Data == nil {
		return map[string]interface{}{}, nil
	}
	return resp.Data.Data, nil
}
