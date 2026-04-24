# vault-kv-diff

A service for detecting secrets with identical values across two KV v2 mounts in HashiCorp Vault — for example, `stage` and `prod`.

It scans both mounts on a schedule, finds paths and keys where values match, and exposes them as Prometheus metrics. Secret values are never transmitted or logged.

**Why:** to ensure that stage and prod are not sharing the same database URLs, access tokens, or API keys.

---

## How it works

1. Recursively traverses both KV mounts and collects all secret paths.
2. Finds the intersection — paths that exist in both mounts.
3. For each common path, reads the secret from both mounts and compares values key by key.
4. If a value matches — emits a Prometheus metric with the path and key name (never the value itself).
5. Repeats on schedule (`SCAN_INTERVAL`).

---

## Metrics

| Metric | Type | Description |
|---|---|---|
| `vault_kv_duplicate_key` | Gauge | 1 for each key with the same value in both mounts. Labels: `kv1`, `kv2`, `path`, `key` |
| `vault_kv_scan_duration_seconds` | Gauge | Duration of the last scan |
| `vault_kv_scan_last_timestamp_seconds` | Gauge | Unix timestamp of the last successful scan |
| `vault_kv_scan_errors_total` | Counter | Total number of scan errors |
| `vault_kv_paths_compared_total` | Gauge | Number of paths compared in the last scan. Labels: `kv1`, `kv2` |

The `vault_kv_duplicate_key` metric is automatically removed when a duplicate is resolved on the next scan.

### Alert example

```yaml
- alert: VaultKvDuplicateSecrets
  expr: vault_kv_duplicate_key > 0
  for: 0m
  labels:
    severity: warning
  annotations:
    summary: "Same secret in stage and prod"
    description: "Path {{ $labels.path }}, key {{ $labels.key }} has the same value in mounts {{ $labels.kv1 }} and {{ $labels.kv2 }}"
```

---

## HTTP endpoints

| Path | Description |
|---|---|
| `/metrics` | Prometheus metrics |
| `/healthz` | Liveness probe — `503` if the last successful scan is older than `3×SCAN_INTERVAL + SCAN_TIMEOUT`; `200` during initial startup |
| `/readyz` | Readiness probe — `503` until the first scan completes successfully |
| `/report` | JSON report of current duplicates (without values) |

Example `/report` response:

```json
{
  "duplicates": [
    {"path": "app/database", "key": "DB_HOST", "kv1": "stage", "kv2": "prod"},
    {"path": "app/redis",    "key": "REDIS_URL", "kv1": "stage", "kv2": "prod"}
  ],
  "paths_compared": 42,
  "kv1": "stage",
  "kv2": "prod"
}
```

---

## Configuration

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `VAULT_ADDR` | — | Vault address, required |
| `VAULT_AUTH_METHOD` | `token` | Authentication method: `token` or `kubernetes` |
| `VAULT_TOKEN` | — | Vault token, required when `VAULT_AUTH_METHOD=token` |
| `VAULT_K8S_ROLE` | — | Vault role name, required when `VAULT_AUTH_METHOD=kubernetes` |
| `VAULT_K8S_MOUNT` | `kubernetes` | Kubernetes auth backend mount path in Vault |
| `VAULT_K8S_TOKEN_PATH` | `/var/run/secrets/kubernetes.io/serviceaccount/token` | Path to the SA token inside the pod |
| `KV1_MOUNT` | — | First KV mount (e.g. `stage`), required |
| `KV2_MOUNT` | — | Second KV mount (e.g. `prod`), required |
| `SCAN_INTERVAL` | `5m` | Scan interval as a Go duration string (`30s`, `5m`, `1h`) |
| `SCAN_TIMEOUT` | `4m` | Per-scan context timeout; should be less than `SCAN_INTERVAL` |
| `HTTP_PORT` | `9090` | HTTP server port |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `CONFIG_FILE` | `config.yaml` | Path to the exclusions file |

### Exclusions file (`config.yaml`)

Lets you skip keys and paths where matching values are acceptable or expected.

```yaml
exclude:
  # Exact key names — always skip during comparison
  keys:
    - environment
    - env
    - namespace
    - app_name

  # Regex patterns matching key names
  key_patterns:
    - "^env_.*"
    - ".*_env$"
    - "^APP_ENV$"

  # Regex patterns matching secret paths (relative to mount, no leading slash)
  path_patterns:
    - "^common/.*"
    - "^shared/.*"
```

Patterns use Go [`regexp`](https://pkg.go.dev/regexp/syntax) syntax. Changes to the file take effect only after restarting the service.

---

## Running

### Docker Compose

```bash
cp .env.example .env
# edit .env
docker compose up -d
```

### Kubernetes

Minimal manifest with Kubernetes auth:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vault-kv-diff
spec:
  replicas: 1
  selector:
    matchLabels:
      app: vault-kv-diff
  template:
    metadata:
      labels:
        app: vault-kv-diff
    spec:
      serviceAccountName: vault-kv-diff
      containers:
        - name: vault-kv-diff
          image: vault-kv-diff:latest
          ports:
            - containerPort: 9090
          env:
            - name: VAULT_ADDR
              value: "https://vault.example.com:8200"
            - name: VAULT_AUTH_METHOD
              value: "kubernetes"
            - name: VAULT_K8S_ROLE
              value: "vault-kv-diff"
            - name: KV1_MOUNT
              value: "stage"
            - name: KV2_MOUNT
              value: "prod"
            - name: SCAN_INTERVAL
              value: "5m"
            - name: SCAN_TIMEOUT
              value: "4m"
          startupProbe:
            httpGet:
              path: /readyz
              port: 9090
            periodSeconds: 10
            failureThreshold: 60  # up to 10 minutes for the first scan
          readinessProbe:
            httpGet:
              path: /readyz
              port: 9090
            periodSeconds: 15
            failureThreshold: 2
          livenessProbe:
            httpGet:
              path: /healthz
              port: 9090
            periodSeconds: 30
            failureThreshold: 3
          volumeMounts:
            - name: config
              mountPath: /app/config.yaml
              subPath: config.yaml
      volumes:
        - name: config
          configMap:
            name: vault-kv-diff-config
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: vault-kv-diff
```

### Configuring Vault for Kubernetes auth

```bash
# Enable Kubernetes auth (if not already enabled)
vault auth enable kubernetes

# Configure the backend
vault write auth/kubernetes/config \
  kubernetes_host="https://kubernetes.default.svc"

# Create the role
vault write auth/kubernetes/role/vault-kv-diff \
  bound_service_account_names=vault-kv-diff \
  bound_service_account_namespaces=monitoring \
  policies=vault-kv-diff \
  ttl=1h
```

Minimal policy:

```hcl
path "stage/*" {
  capabilities = ["read", "list"]
}

path "prod/*" {
  capabilities = ["read", "list"]
}
```

---

## Building

```bash
# Locally
go build -o vault-kv-diff .

# Docker
docker build -t vault-kv-diff .
```
