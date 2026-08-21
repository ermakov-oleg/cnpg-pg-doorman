# cnpg-pg-doorman

A [CloudNativePG](https://cloudnative-pg.io/) plugin that injects [pg_doorman](https://github.com/ozontech/pg_doorman) connection pooler as a sidecar into PostgreSQL pods.

![Maturity: alpha](https://img.shields.io/badge/maturity-alpha-orange)
![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)

## Overview

CloudNativePG provides connection pooling through its `Pooler` CRD, which deploys PgBouncer as a separate Deployment, but doesn't offer per-pod pooling as a sidecar. **cnpg-pg-doorman** uses the CNPG plugin API to inject a [pg_doorman](https://github.com/ozontech/pg_doorman) sidecar — a high-performance connection pooler written in Rust — into every PostgreSQL pod.

The plugin consists of two components:

- **Plugin Server** — a gRPC server the CNPG controller talks to. It injects the pg_doorman sidecar via lifecycle hooks, blocks cluster creation until the corresponding `PgDoorman` CR exists, and (leader-elected) renders the CR and its referenced Secrets into a per-cluster `<cluster>-doorman-config` Secret.
- **Wrapper sidecar** — supervises the pg_doorman process inside each PostgreSQL pod: applies configuration from the mounted Secret (SIGHUP hot-reload; non-reloadable fields trigger a graceful in-place process restart), restarts it on crash, and drops pooler sessions when the instance is demoted. It has no Kubernetes client of its own, so PostgreSQL pods need zero RBAC.

```
┌──────────────────┐   gRPC   ┌──────────────────────────┐
│ CNPG Controller  │◄────────►│ pg-doorman Plugin Server │
│ Manager          │          │ (Deployment)             │
└────────┬─────────┘          └────────────┬─────────────┘
         │ manages                         │ renders PgDoorman CR
         ▼                                 ▼ and referenced Secrets
┌────────────────────────────────┐    ┌──────────────────────┐
│ PostgreSQL Pod                 │    │ <cluster>-doorman-   │
│                                │◄───┤ config Secret        │
│ Client ─► :6432 ────► :5432    │    └──────────────────────┘
│        (pg_doorman) (postgres) │
└────────────────────────────────┘
```

## Features

- **CRD-based configuration** — pooler settings are managed through the `PgDoorman` custom resource
- **Hot-reload** — configuration changes are applied without pod restarts
- **Dynamic authentication** — reuses PostgreSQL password verification via `auth_query`
- **Static user credentials** — passwords can be referenced from Kubernetes Secrets
- **Transaction & session pool modes** — configurable per database pool
- **Client TLS** — pg_doorman terminates TLS on the pooler port using the CNPG server certificate, so `sslmode=verify-full` clients work through the pooler
- **Prometheus metrics** — built-in metrics endpoint for monitoring
- **In-place binary upgrades** — opt-in upgrades of the pg_doorman binary without pod restarts, see [docs/in-place-upgrades.md](docs/in-place-upgrades.md)

## Prerequisites

- Kubernetes 1.29+ (requires [native sidecar containers](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/) support)
- [CloudNativePG](https://cloudnative-pg.io/) 1.25+
- [cert-manager](https://cert-manager.io/)

## Installation

Every [GitHub Release](https://github.com/ermakov-oleg/cnpg-pg-doorman/releases) ships a `manifest.yaml` with image references pinned to the released multi-arch images on `ghcr.io`:

```bash
kubectl apply --server-side -f https://github.com/ermakov-oleg/cnpg-pg-doorman/releases/latest/download/manifest.yaml
```

This installs the CRD, RBAC, TLS certificates, and the plugin Deployment into the `cnpg-system` namespace.

### Install with Helm

The same manifests are packaged as a Helm chart, published to `ghcr.io` as an OCI artifact by every release:

```bash
helm install cnpg-pg-doorman oci://ghcr.io/ermakov-oleg/charts/cnpg-pg-doorman \
  --namespace cnpg-system --create-namespace
```

Image tags default to the chart `appVersion` (the release tag), so a released chart pulls the matching plugin and wrapper images. See `chart/values.yaml` for the configurable values (images, replicas, resources, PDB, scheduling).

The plugin must be installed into the namespace where the CloudNativePG operator runs (usually `cnpg-system`).

> **Note**: do not `kubectl apply -k` the raw `kubernetes/` directory — it is the development kustomization with local `:testing` image names and is guaranteed to ImagePullBackOff outside the e2e setup. Building your own images is covered in [Development](#development).

## Quick Start

Both the `PgDoorman` CR and the CNPG `Cluster` must be in the same namespace.

1. Create a `PgDoorman` resource with your pooler configuration:

```yaml
apiVersion: pg-doorman.cnpg.io/v1alpha1
kind: PgDoorman
metadata:
  name: my-cluster-doorman
spec:
  clusterRef:
    name: my-cluster
  pools:
    app:
      authQuery:
        user: doorman_auth
```

Without `passwordSecretRef` the plugin generates a `kubernetes.io/basic-auth`
Secret named `my-cluster-doorman-auth` with a random password for the
`doorman_auth` role — no secret material has to live in your manifests
(GitOps-friendly). To rotate the password, delete the Secret: the plugin
recreates it and CNPG syncs the role.

To bring your own password instead, create the Secret yourself and point
`authQuery.passwordSecretRef` at it:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-cluster-doorman-auth
  labels:
    # Required: the render controller only accepts secrets labeled as
    # belonging to the cluster (confused-deputy guard).
    cnpg.io/cluster: my-cluster
type: kubernetes.io/basic-auth
stringData:
  username: doorman_auth
  password: "generate-a-strong-password-here"
```

2. Create a CNPG `Cluster` that references the plugin:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: my-cluster
spec:
  instances: 3

  plugins:
    - name: pg-doorman.cnpg.io
      parameters:
        poolerPort: "6432"
        metricsPort: "9127"
        configName: "my-cluster-doorman"

  managed:
    roles:
      # CNPG keeps the doorman_auth password in sync with the Secret
      # (generated by the plugin, or your own), including rotation.
      - name: doorman_auth
        login: true
        inherit: false
        passwordSecret:
          name: my-cluster-doorman-auth

  postgresql:
    pg_hba:
      # doorman_auth authenticates with its password (from the Secret above)
      - host all doorman_auth 127.0.0.1/32 scram-sha-256

  bootstrap:
    initdb:
      database: app
      owner: app
      postInitSQL:
        - CREATE ROLE doorman_auth WITH LOGIN NOINHERIT
        - |
          CREATE OR REPLACE FUNCTION public.doorman_auth_query(username TEXT)
          RETURNS TABLE (usename name, passwd text)
          SECURITY DEFINER
          SET search_path = pg_catalog
          LANGUAGE SQL
          AS $$ SELECT usename, passwd FROM pg_shadow WHERE usename = $1 $$
        - GRANT EXECUTE ON FUNCTION public.doorman_auth_query(TEXT) TO doorman_auth

  storage:
    size: 10Gi
```

> **Security note**: do NOT use a `trust` pg_hba rule for `doorman_auth`. The
> pod network namespace is shared with all sidecars, and the `SECURITY DEFINER`
> auth function exposes password hashes of every role — always require
> `scram-sha-256` with a real password as shown above.

See the [examples/](examples/) directory for more configuration samples.

### Connecting to pg_doorman

pg_doorman listens on port `6432` inside each PostgreSQL pod. The standard CNPG Services still point to PostgreSQL on port `5432`. The plugin automatically creates a `<cluster>-doorman-rw` Service (port `5432`, targeting the pooler on the primary instance), so clients connect to it as a drop-in replacement for `<cluster>-rw`.

For in-pod communication (e.g. from application sidecars), connect to `localhost:6432`.

> **Propagation latency**: configuration changes reach pods via the kubelet's
> Secret volume sync, typically within ~1 minute.

> **Failover behavior**: when an instance is demoted (failover/switchover), the
> wrapper gracefully restarts pg_doorman on that pod to drop long-lived client
> sessions — otherwise they would keep hitting the demoted (now read-only)
> instance forever. Clients must implement reconnect-on-error; on reconnect the
> Service routes them to the new primary.

## Monitoring

pg_doorman exposes Prometheus metrics on the `metricsPort` (default `9127`),
declared as the named container port `pgd-metrics` on the sidecar. The
PodMonitor CNPG creates for the cluster scrapes only the postgres container —
add a separate scrape config for the pooler, e.g.
[examples/podmonitor-example.yaml](examples/podmonitor-example.yaml).

The wrapper exposes its own metrics on the health port `8081` (`/metrics`):
`pg_doorman_wrapper_reloads_total{result}`, `pg_doorman_wrapper_process_restarts_total`
and `pg_doorman_wrapper_config_stale`. Rendering state is reported on the
`PgDoorman` resource itself: the `Rendered` condition, `status.observedGeneration`
and Events (`kubectl describe pgd <name>`).

## Configuration Reference

### Plugin Parameters

Parameters passed via `spec.plugins[].parameters` in the CNPG Cluster:

| Parameter     | Default | Description                              |
| ------------- | ------- | ---------------------------------------- |
| `poolerPort`  | `6432`  | Port pg_doorman listens on               |
| `metricsPort` | `9127`  | Port for Prometheus metrics              |
| `configName`  | —       | **Required.** Name of the `PgDoorman` CR to watch |
| `sidecarCpuRequest`    | `100m`  | Sidecar CPU request                |
| `sidecarMemoryRequest` | `128Mi` | Sidecar memory request             |
| `sidecarCpuLimit`      | —       | Sidecar CPU limit (unset by default: a hard CPU cap throttles pooled traffic) |
| `sidecarMemoryLimit`   | `512Mi` | Sidecar memory limit; `none` removes it |
| `logLevel`             | `info`  | Log level of pg_doorman and the wrapper (`error`/`warn`/`info`/`debug`/`trace`) |
| `inPlaceUpgrades`      | `false` | Upgrade the pg_doorman binary of running sidecars without a pod restart |

### PgDoorman CRD

#### Top-level

| Field        | Description |
| ------------ | ----------- |
| `clusterRef` | **Required, immutable.** Name of the CNPG `Cluster` this configuration belongs to. Admission rejects a Cluster whose `configName` points at a PgDoorman owned by another cluster. |

#### Pool Settings (`spec.pools.<db>`)

| Field             | Default       | Description                                |
| ----------------- | ------------- | ------------------------------------------ |
| `poolMode`        | `transaction` | Pooling mode (`transaction` or `session`)  |
| `defaultPoolSize` | `40`          | Data pool size per auth_query user (`auth_query.pool_size`) |
| `authQuery`       | —             | Auth query configuration (see below)       |
| `users`           | —             | Static user credentials list (see below)   |

#### Auth Query (`spec.pools.<db>.authQuery`)

| Field                | Default                                              | Description                            |
| -------------------- | ---------------------------------------------------- | -------------------------------------- |
| `user`               | —                                                    | **Required.** PostgreSQL user for auth queries |
| `query`              | `SELECT * FROM public.doorman_auth_query($1)` | SQL query for authentication           |
| `database`           | `postgres`                                           | Database to run auth queries against   |
| `poolSize`           | `2`                                                  | Executor connections for auth queries (`auth_query.workers`) |
| `passwordSecretRef`  | —                                                    | Secret reference for auth user password |

#### Static User (`spec.pools.<db>.users[]`)

| Field               | Default | Description                            |
| -------------------- | ------- | -------------------------------------- |
| `username`           | —       | **Required.** PostgreSQL username      |
| `passwordSecretRef`  | —       | **Required.** Secret reference for user password |
| `poolSize`           | `20`    | Pool size for this user                |

#### General Settings (`spec.general`)

| Field              | Default     | Description                        |
| ------------------ | ----------- | ---------------------------------- |
| `maxConnections`   | `8192`      | Maximum total connections           |
| `workerThreads`    | `4`         | Number of worker threads            |
| `connectTimeout`   | `3s`        | Backend connection timeout          |
| `idleTimeout`      | `5m`        | Idle connection timeout             |
| `serverLifetime`   | `5m`        | Backend connection max lifetime     |
| `shutdownTimeout`  | `10s`       | Graceful shutdown timeout           |
| `adminUsername`    | `admin`     | Admin console username              |
| `adminPasswordSecretRef` | —   | Secret reference for the admin console password. When unset, the wrapper generates a random per-pod password. |

#### Prometheus (`spec.prometheus`)

| Field     | Default | Description                      |
| --------- | ------- | -------------------------------- |
| `enabled` | `true`  | Enable Prometheus metrics export |

> The metrics port is controlled by the `metricsPort` plugin parameter (default `9127`), not by the CRD.

## In-place pg_doorman upgrades

Opt-in per cluster via the plugin parameter `inPlaceUpgrades: "true"`. When the plugin is upgraded to a release that pins a newer pg_doorman, running sidecars download the new binary from the plugin (sha256-verified), validate it against the current config, and hand over via pg_doorman's built-in `SIGUSR2` mechanism — idle client connections migrate to the new process, and the pod never restarts.

See [docs/in-place-upgrades.md](docs/in-place-upgrades.md) for the full mechanics, security model, client-facing caveats, and how to pin the sidecar image tag so in-place upgrades actually get a chance to run.

## Development

```bash
# Build
go build ./...

# Run unit tests
go test ./internal/... -v

# Generate CRD manifests
task generate

# Run e2e tests (requires Docker, creates a kind cluster)
task e2e
```

## License

Apache-2.0
