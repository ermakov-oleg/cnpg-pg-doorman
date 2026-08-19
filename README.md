# cnpg-pg-doorman

A [CloudNativePG](https://cloudnative-pg.io/) plugin that injects [pg_doorman](https://github.com/ozontech/pg_doorman) connection pooler as a sidecar into PostgreSQL pods.

![Maturity: alpha](https://img.shields.io/badge/maturity-alpha-orange)
![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)

## Overview

CloudNativePG provides connection pooling through its `Pooler` CRD, which deploys PgBouncer as a separate Deployment. This works well for many use cases, but doesn't provide per-pod connection pooling as a sidecar.

**cnpg-pg-doorman** solves this by leveraging the CNPG plugin API to automatically inject a [pg_doorman](https://github.com/ozontech/pg_doorman) sidecar into every PostgreSQL pod. pg_doorman is a high-performance connection pooler written in Rust.

## Features

- **Automatic sidecar injection** — pg_doorman is injected into PostgreSQL pods via CNPG plugin lifecycle hooks
- **CRD-based configuration** — pooler settings are managed through the `PgDoorman` custom resource
- **Hot-reload** — configuration changes are applied without pod restarts (via SIGHUP)
- **Dynamic authentication** — reuses PostgreSQL password verification via `auth_query`
- **Static user credentials** — passwords can be referenced from Kubernetes Secrets
- **Transaction & session pool modes** — configurable per database pool
- **Prometheus metrics** — built-in metrics endpoint for monitoring
- **Automatic RBAC management** — the plugin creates necessary Roles and RoleBindings for the wrapper sidecar

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│ Kubernetes Cluster                                           │
│                                                              │
│  ┌─────────────────┐         ┌──────────────────────────┐    │
│  │ CNPG Controller  │◄─gRPC─►│ pg-doorman Plugin Server │    │
│  │ Manager          │        │ (Deployment)             │    │
│  └────────┬─────────┘        └──────────────────────────┘    │
│           │                                                  │
│           │ manages                                          │
│           ▼                                                  │
│  ┌───────────────────────────────────────────────┐           │
│  │ PostgreSQL Pod                                │           │
│  │                                               │           │
│  │  Client ──► :6432 (pg_doorman) ──► :5432 (PG) │           │
│  │              ▲                                │           │
│  │              │ config reload (SIGHUP)         │           │
│  │              │                                │           │
│  │         doorman-wrapper                       │           │
│  │              ▲                                │           │
│  └──────────────┼────────────────────────────────┘           │
│                 │ watches                                    │
│           ┌─────┴──────┐                                     │
│           │ PgDoorman  │                                     │
│           │ CR         │                                     │
│           └────────────┘                                     │
└──────────────────────────────────────────────────────────────┘
```

## Prerequisites

- Kubernetes 1.29+ (requires [native sidecar containers](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/) support)
- [CloudNativePG](https://cloudnative-pg.io/) 1.24+ (with plugin support)
- [cert-manager](https://cert-manager.io/)

## Installation

> **Note**: Container images are not yet published to a registry. You need to build and push images to your own registry first (see [Development](#development)). Then update the image references in `kubernetes/kustomization.yaml` before applying.

```bash
kubectl apply -k https://github.com/ermakov-oleg/cnpg-pg-doorman/kubernetes
```

This installs the CRD, RBAC, TLS certificates, and the plugin Deployment into the `cnpg-system` namespace.

## Quick Start

Both the `PgDoorman` CR and the CNPG `Cluster` must be in the same namespace.

1. Create a `PgDoorman` resource with your pooler configuration:

```yaml
apiVersion: pg-doorman.cnpg.io/v1alpha1
kind: PgDoorman
metadata:
  name: my-cluster-doorman
spec:
  pools:
    app:
      authQuery:
        user: doorman_auth
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

  postgresql:
    pg_hba:
      # Allow doorman_auth to connect via localhost without password (for auth_query)
      - host all doorman_auth 127.0.0.1/32 trust

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

See the [examples/](examples/) directory for more configuration samples.

### Connecting to pg_doorman

pg_doorman listens on port `6432` inside each PostgreSQL pod. The standard CNPG Services still point to PostgreSQL on port `5432`. To route client traffic through the pooler, create a separate Service:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-cluster-pooler
spec:
  selector:
    cnpg.io/cluster: my-cluster
    role: primary
  ports:
    - port: 5432
      targetPort: 6432
      protocol: TCP
```

For in-pod communication (e.g. from application sidecars), connect to `localhost:6432`.

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

### PgDoorman CRD

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
| `adminPassword`    | `change-me` | Admin console password. **Use `adminPasswordSecretRef` in production.** |
| `adminPasswordSecretRef` | —   | Secret reference for admin password (takes precedence over `adminPassword`) |

#### Prometheus (`spec.prometheus`)

| Field     | Default | Description                      |
| --------- | ------- | -------------------------------- |
| `enabled` | `true`  | Enable Prometheus metrics export |

> The metrics port is controlled by the `metricsPort` plugin parameter (default `9127`), not by the CRD.

## How It Works

The plugin consists of two components:

**Plugin Server** — a gRPC server that integrates with the CNPG controller:
- **Lifecycle hooks**: inject the pg_doorman sidecar container into PostgreSQL pods via JSON Patch
- **Reconciler hooks**: block cluster creation until the corresponding `PgDoorman` CR exists, ensuring the pooler is always configured before the cluster starts
- **RBAC management**: automatically creates Roles and RoleBindings so the wrapper sidecar can read `PgDoorman` CRs and Secrets

**Wrapper Sidecar** — a process injected into each PostgreSQL pod:
- Watches the `PgDoorman` CR for configuration changes
- Resolves Secret references to actual passwords
- Generates pg_doorman configuration and writes it atomically
- Manages the pg_doorman process lifecycle (start, SIGHUP on config change, restart on crash)

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
