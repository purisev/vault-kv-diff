# vault-kv-diff

A service for detecting secrets with identical values across pairs of KV v2 mounts in HashiCorp Vault.

It scans configured mount pairs on a schedule, finds paths and keys where values match, and exposes them as Prometheus metrics. Secret values are never transmitted or logged.

**Why:** to ensure that two environments are not sharing the same database URLs, access tokens, or API keys.

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
| `vault_kv_duplicate_count` | Gauge | Total number of duplicate key-value pairs in the last scan. Labels: `kv1`, `kv2`. Use this for alerting. |
| `vault_kv_duplicate_key` | Gauge | 1 for each key with the same value in both mounts. Labels: `kv1`, `kv2`, `path`, `key`. Use for drill-down. |
| `vault_kv_scan_duration_seconds` | Gauge | Duration of the last scan |
| `vault_kv_scan_last_timestamp_seconds` | Gauge | Unix timestamp of the last successful scan |
| `vault_kv_scan_errors_total` | Counter | Total number of scan errors |
| `vault_kv_paths_compared` | Gauge | Number of paths compared in the last scan. Labels: `kv1`, `kv2` |

The `vault_kv_duplicate_key` metric is automatically removed when a duplicate is resolved on the next scan.

### Alert example

```yaml
- alert: VaultKvDuplicateSecrets
  expr: vault_kv_duplicate_count > 0
  for: 0m
  labels:
    severity: warning
  annotations:
    summary: "Identical secrets found between {{ $labels.kv1 }} and {{ $labels.kv2 }}"
    description: "{{ $value }} key(s) have the same value in both mounts. Check /report for details."
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
  "pairs": [
    {
      "kv1": "mount-a",
      "kv2": "mount-b",
      "duplicates": [
        {"path": "app/database", "keys": ["DB_HOST", "DB_PASS"]},
        {"path": "app/redis",    "keys": ["REDIS_URL"]}
      ],
      "paths_affected": 2,
      "paths_compared": 42,
      "scanned_at": "2026-04-24T10:00:00Z"
    }
  ]
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
| `SCAN_INTERVAL` | `5m` | Scan interval as a Go duration string (`30s`, `5m`, `1h`) |
| `SCAN_TIMEOUT` | `4m` | Per-scan context timeout; should be less than `SCAN_INTERVAL` |
| `HTTP_PORT` | `9090` | HTTP server port |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `CONFIG_FILE` | `config.yaml` | Path to the config file |

### Config file (`config.yaml`)

Defines mount pairs to compare and per-pair exclusion rules. Reloaded automatically before every scan — no restart needed.

```yaml
pairs:
  - kv1: mount-a
    kv2: mount-b
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

  - kv1: mount-c
    kv2: mount-a
    # no exclusions — compare all keys
```

Multiple pairs are scanned in parallel. Patterns use Go [`regexp`](https://pkg.go.dev/regexp/syntax) syntax.

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

Minimal policy (repeat for each configured mount):

```hcl
path "<mount>/*" {
  capabilities = ["read", "list"]
}
```

> **Security note:** run the service under a dedicated service account in its own namespace (e.g. `monitoring`), never under the Vault service account or in the `vault` namespace. The Vault SA typically carries broad or root-level permissions — inheriting them would violate the principle of least privilege and turn a compromised vault-kv-diff into a full Vault breach. The role above restricts access to `read` and `list` only, which is all the service needs.

---

## Building

```bash
# Locally
go build -o vault-kv-diff .

# Docker
docker build -t vault-kv-diff .
```
