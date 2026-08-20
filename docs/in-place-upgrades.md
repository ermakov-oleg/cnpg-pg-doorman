# In-place pg_doorman upgrades

This feature is opt-in per cluster via the plugin parameter `inPlaceUpgrades: "true"` (default: off). Without it, the sidecar always runs the pg_doorman binary baked into its image, and the plugin never publishes `binary.json` for that cluster.

The plugin image carries the pg_doorman binaries for every supported architecture. When the plugin is upgraded to a release that pins a newer pg_doorman, running sidecars of clusters with `inPlaceUpgrades: "true"` pick up the new binary without a pod or container restart:

1. The leader-elected plugin controller publishes `binary.json` (per-arch download URL, sha256 digests, and the CA bundle for the delivery endpoint) into the same rendered `<cluster>-doorman-config` Secret used for pooler config.
2. The wrapper polls the mounted `binary.json`. On a digest change it downloads the binary for its architecture from the plugin's binary delivery endpoint, verifying the sha256.
3. The downloaded binary is validated against the current config (`pg_doorman --test-config`) before anything is swapped; a rejected config keeps the old binary running.
4. The wrapper atomically installs the new binary at the runtime path, then sends `SIGUSR2` to pg_doorman. Upstream pg_doorman handles the rest of the handover itself: it validates the new binary, re-executes it as its successor, and migrates idle client connections to it over `SCM_RIGHTS`. The old process drains any in-flight work for up to `shutdown_timeout` and exits; the wrapper adopts the successor pid and keeps supervising it. The pod and its container never restart.

## Requirements

The plugin's binary delivery endpoint (port `9091`, served by every plugin replica) must be reachable from every PostgreSQL pod, and the plugin server must be started with `--binary-base-url` (the URL published to wrappers via `binary.json`) and `--binary-ca-file` (the CA bundle wrappers use to verify that endpoint's TLS certificate) — both are set by the shipped manifests and Helm chart, pointing at the plugin's own Service.

## Security model

The rendered config Secret is the trust root of this flow: the download URL, the per-arch sha256 digests, and the CA bundle the sidecar verifies the endpoint against all come from it, so a principal able to write Secrets in a cluster's namespace can point the sidecar at an arbitrary binary and have it executed — restrict write access to Secrets in those namespaces accordingly.

## Observability

Wrapper metrics for this flow: `pg_doorman_wrapper_binary_upgrades_total{result}` (handover outcomes) and `pg_doorman_wrapper_binary_stale` (1 while the installed binary does not yet match `binary.json`).

## Rollback

Turning `inPlaceUpgrades` off stops future upgrades but does not roll back an already-upgraded running binary: that only happens on the next pod or container restart, when the wrapper's startup sync finds no `binary.json` and installs the binary baked into the image instead.

## Client-facing caveats

- The bundled pg_doorman binary is built without the `tls-migration` feature, so TLS client sessions are **not** migrated to the successor: they drain and must reconnect, same as a plain restart.
- Clients still in a transaction when `shutdown_timeout` elapses are terminated with PostgreSQL error `58006` (`connection_failure`) rather than being migrated. Client libraries and connection pools must handle reconnects on this error — see e.g. [lib/pq#939](https://github.com/lib/pq/issues/939) for a case where a driver did not.

## Version skew during a plugin rollout

During a rolling update of the plugin Deployment, pods across the cluster briefly run different plugin replicas, and PostgreSQL pods pick up a new pg_doorman binary asynchronously (each wrapper polls and upgrades independently). As a result, different PostgreSQL pods of the same `Cluster` can run different pg_doorman versions for a short window while sharing the *same* rendered config.

This means the config schema must stay compatible between any two adjacent pinned pg_doorman versions the plugin ships across a rollout. A breaking config change upstream (precedent: the `pool_size` → `workers` rename in pg_doorman v3.11.0) cannot be delivered as an in-place hop and requires an explicit migration path in the plugin release that adopts it.

## Keep the sidecar image tag stable

In-place upgrades only help when the sidecar image tag does **not** change together with the plugin release: if it does, CNPG still rolls the PostgreSQL pods to pick up the new sidecar image, and the in-place swap never gets a chance to run. By default the Helm chart's `image.tag` and `sidecarImage.tag` both follow the chart `appVersion`, so they move together on every release. To benefit from in-place upgrades, pin `sidecarImage.tag` to a fixed version and let `image.tag` (the plugin) advance on its own — the upgraded plugin then delivers the new pg_doorman binary to the already-running sidecars itself.
