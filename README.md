# cnpg-pg-doorman

A [CloudNativePG](https://cloudnative-pg.io/) plugin that injects [pg_doorman](https://github.com/ozontech/pg_doorman) connection pooler as a sidecar into PostgreSQL pods.

![Maturity: alpha](https://img.shields.io/badge/maturity-alpha-orange)
![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)

## Overview

CloudNativePG provides connection pooling through its `Pooler` CRD, which deploys PgBouncer as a separate Deployment. This works well for many use cases, but doesn't provide per-pod connection pooling as a sidecar.

**cnpg-pg-doorman** solves this by leveraging the CNPG plugin API to automatically inject a [pg_doorman](https://github.com/ozontech/pg_doorman) sidecar into every PostgreSQL pod. pg_doorman is a high-performance connection pooler written in Rust.

## Features

- **Automatic sidecar injection** — pg_doorman is injected into PostgreSQL pods via CNPG plugin lifecycle hooks
- **CRD-based configuration** — pooler settings are managed through the `PgDoorman` custom resource; the plugin renders them centrally into a per-cluster Secret, so PostgreSQL pods need zero RBAC
- **Hot-reload** — configuration changes are applied without pod restarts (via SIGHUP; non-reloadable fields trigger a graceful in-place process restart)
- **Dynamic authentication** — reuses PostgreSQL password verification via `auth_query`
- **Static user credentials** — passwords can be referenced from Kubernetes Secrets
- **Transaction & session pool modes** — configurable per database pool
- **Client TLS** — pg_doorman terminates TLS on the pooler port using the CNPG server certificate, so `sslmode=verify-full` clients work through the pooler
- **Prometheus metrics** — built-in metrics endpoint for monitoring

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
│                 │ mounts                                     │
│        ┌────────┴─────────────┐      renders                 │
│        │ <cluster>-doorman-   │◄─────────────┐               │
│        │ config Secret        │              │               │
│        └──────────────────────┘   ┌──────────┴───────────┐   │
│                                   │ Plugin controller    │   │
│           ┌────────────┐ watches  │ (leader-elected)     │   │
│           │ PgDoorman  │◄─────────┤                      │   │
│           │ CR         │          └──────────────────────┘   │
│           └────────────┘                                     │
└──────────────────────────────────────────────────────────────┘
```

## Prerequisites

- Kubernetes 1.29+ (requires [native sidecar containers](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/) support)
- [CloudNativePG](https://cloudnative-pg.io/) 1.25+
- [cert-manager](https://cert-manager.io/)

Verified by CI: the latest kindest/CNPG combination on every run, plus a manual
bounds check (`e2e-bounds` workflow job). CNPG 1.25.2 on Kubernetes v1.31 passes
the full suite; CNPG 1.24 works except automatic pod rollout when the sidecar
image changes (its drift detection misses plugin-injected pod spec changes), so
plugin upgrades there require a manual rollout.

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

1. Create a Secret with the password of the `doorman_auth` role (used both by
   PostgreSQL via `managed.roles` and by pg_doorman via `authQuery.passwordSecretRef`):

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

2. Create a `PgDoorman` resource with your pooler configuration:

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
        passwordSecretRef:
          name: my-cluster-doorman-auth
          key: password
```

3. Create a CNPG `Cluster` that references the plugin:

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
      # CNPG keeps the doorman_auth password in sync with the Secret,
      # including rotation.
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

> **Security note**: do NOT use a `trust` pg_hba rule for `doorman_auth`
> (e.g. `host all doorman_auth 127.0.0.1/32 trust`). The pod network
> namespace is shared: any compromised sidecar could connect to
> `127.0.0.1:5432` as `doorman_auth` without a password and dump password
> hashes of every role (including `postgres` and `streaming_replica`) through
> the `SECURITY DEFINER` auth function. Always require `scram-sha-256` with a
> real password as shown above.

See the [examples/](examples/) directory for more configuration samples.

### Connecting to pg_doorman

pg_doorman listens on port `6432` inside each PostgreSQL pod. The standard CNPG Services still point to PostgreSQL on port `5432`. The plugin automatically creates a `<cluster>-doorman-rw` Service (port `5432`, targeting the pooler on the primary instance), so clients connect to it as a drop-in replacement for `<cluster>-rw`.

For in-pod communication (e.g. from application sidecars), connect to `localhost:6432`.

> **Propagation latency**: configuration changes reach pods via the kubelet's
> Secret volume sync, typically within ~1 minute (vs. instant API reads in the
> pre-rendered architecture). In exchange, PostgreSQL pods hold no Kubernetes
> credentials at all.

> **Failover behavior**: when an instance is demoted (failover/switchover), the
> wrapper gracefully restarts pg_doorman on that pod to drop long-lived client
> sessions — otherwise they would keep hitting the demoted (now read-only)
> instance forever. Clients must implement reconnect-on-error; on reconnect the
> Service routes them to the new primary.

## Monitoring

The wrapper exposes its own metrics on the health port `8081` (`/metrics`):
`pg_doorman_wrapper_reloads_total{result}`, `pg_doorman_wrapper_process_restarts_total`
(in-process restarts are invisible to Kubernetes: `restartCount` stays 0) and
`pg_doorman_wrapper_config_stale`. Rendering state is reported on the
`PgDoorman` resource itself: the `Rendered` condition, `status.observedGeneration`
and Events (`kubectl describe pgd <name>`).

pg_doorman exposes Prometheus metrics on the `metricsPort` (default `9127`),
declared as the named container port `pgd-metrics` on the sidecar. The
PodMonitor CNPG creates for the cluster scrapes only the `metrics` port of the
postgres container (9187) — pg_doorman metrics need their own scrape config,
e.g. [examples/podmonitor-example.yaml](examples/podmonitor-example.yaml).

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

## How It Works

The plugin consists of two components:

**Plugin Server** — a gRPC server that integrates with the CNPG controller:
- **Lifecycle hooks**: inject the pg_doorman sidecar container into PostgreSQL pods via JSON Patch
- **Reconciler hooks**: block cluster creation until the corresponding `PgDoorman` CR exists, ensuring the pooler is always configured before the cluster starts
- **Config rendering**: (leader-elected) renders `PgDoorman` CRs and the referenced Secrets into a per-cluster `<cluster>-doorman-config` Secret, mounted read-only into the PostgreSQL pod — the wrapper needs no RBAC of its own

**Wrapper Sidecar** — a process injected into each PostgreSQL pod, with no Kubernetes client of its own:
- Applies the pg_doorman configuration rendered by the plugin controller into the mounted `<cluster>-doorman-config` Secret
- Manages the pg_doorman process lifecycle (start, SIGHUP on config change, restart on crash, in-place binary upgrades — see below)
- Watches the instance role and drops pooler sessions on demotion

## In-place pg_doorman upgrades

This feature is opt-in per cluster via the plugin parameter `inPlaceUpgrades: "true"` (default: off). Without it, the sidecar always runs the pg_doorman binary baked into its image, and the plugin never publishes `binary.json` for that cluster.

The plugin image carries the pg_doorman binaries for every supported architecture. When the plugin is upgraded to a release that pins a newer pg_doorman, running sidecars of clusters with `inPlaceUpgrades: "true"` pick up the new binary without a pod or container restart:

1. The leader-elected plugin controller publishes `binary.json` (per-arch download URL, sha256 digests, and the CA bundle for the delivery endpoint) into the same rendered `<cluster>-doorman-config` Secret used for pooler config.
2. The wrapper polls the mounted `binary.json`. On a digest change it downloads the binary for its architecture from the plugin's binary delivery endpoint, verifying the sha256.
3. The downloaded binary is validated against the current config (`pg_doorman --test-config`) before anything is swapped; a rejected config keeps the old binary running.
4. The wrapper atomically installs the new binary at the runtime path, then sends `SIGUSR2` to pg_doorman. Upstream pg_doorman handles the rest of the handover itself: it validates the new binary, re-executes it as its successor, and migrates idle client connections to it over `SCM_RIGHTS`. The old process drains any in-flight work for up to `shutdown_timeout` and exits; the wrapper adopts the successor pid and keeps supervising it. The pod and its container never restart.

This requires the plugin's binary delivery endpoint (port `9091`, served by every plugin replica) to be reachable from every PostgreSQL pod, and the plugin server to be started with `--binary-base-url` (the URL published to wrappers via `binary.json`) and `--binary-ca-file` (the CA bundle wrappers use to verify that endpoint's TLS certificate) — both are set by the shipped manifests and Helm chart, pointing at the plugin's own Service.

The rendered config Secret is the trust root of this flow: the download URL, the per-arch sha256 digests, and the CA bundle the sidecar verifies the endpoint against all come from it, so a principal able to write Secrets in a cluster's namespace can point the sidecar at an arbitrary binary and have it executed — restrict write access to Secrets in those namespaces accordingly.

Wrapper metrics for this flow: `pg_doorman_wrapper_binary_upgrades_total{result}` (handover outcomes) and `pg_doorman_wrapper_binary_stale` (1 while the installed binary does not yet match `binary.json`).

Turning `inPlaceUpgrades` off stops future upgrades but does not roll back an already-upgraded running binary: that only happens on the next pod or container restart, when the wrapper's startup sync finds no `binary.json` and installs the binary baked into the image instead.

### Client-facing caveats

- The bundled pg_doorman binary is built without the `tls-migration` feature, so TLS client sessions are **not** migrated to the successor: they drain and must reconnect, same as a plain restart.
- Clients still in a transaction when `shutdown_timeout` elapses are terminated with PostgreSQL error `58006` (`connection_failure`) rather than being migrated. Client libraries and connection pools must handle reconnects on this error — see e.g. [lib/pq#939](https://github.com/lib/pq/issues/939) for a case where a driver did not.

### Version skew during a plugin rollout

During a rolling update of the plugin Deployment, pods across the cluster briefly run different plugin replicas, and PostgreSQL pods pick up a new pg_doorman binary asynchronously (each wrapper polls and upgrades independently). As a result, different PostgreSQL pods of the same `Cluster` can run different pg_doorman versions for a short window while sharing the *same* rendered config.

This means the config schema must stay compatible between any two adjacent pinned pg_doorman versions the plugin ships across a rollout. A breaking config change upstream (precedent: the `pool_size` → `workers` rename in pg_doorman v3.11.0) cannot be delivered as an in-place hop and requires an explicit migration path in the plugin release that adopts it.

### Keep the sidecar image tag stable

In-place upgrades only help when the sidecar image tag does **not** change together with the plugin release: if it does, CNPG still rolls the PostgreSQL pods to pick up the new sidecar image, and the in-place swap never gets a chance to run. By default the Helm chart's `image.tag` and `sidecarImage.tag` both follow the chart `appVersion`, so they move together on every release. To benefit from in-place upgrades, pin `sidecarImage.tag` to a fixed version and let `image.tag` (the plugin) advance on its own — the upgraded plugin then delivers the new pg_doorman binary to the already-running sidecars itself.

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
