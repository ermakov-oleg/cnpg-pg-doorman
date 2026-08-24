# Changelog

## [0.5.0](https://github.com/ermakov-oleg/cnpg-pg-doorman/compare/v0.4.1...v0.5.0) (2026-08-24)


### Features

* generate auth_query password Secret when passwordSecretRef is omitted ([b65b522](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/b65b52257a95e7ab5a963c83ae950f4f1f7a1c92))
* generate auth_query password Secret when passwordSecretRef is omitted ([c9541db](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/c9541db49d151e33d5e99ffb0003b1a34f5094e8))

## [0.4.1](https://github.com/ermakov-oleg/cnpg-pg-doorman/compare/v0.4.0...v0.4.1) (2026-08-21)


### Bug Fixes

* add missing go.sum zip hash for k8s.io/streaming v0.36.4 ([d687320](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/d6873200acc6a4ac287c27deb12a9f9155d5aa54))
* **deps:** update kubernetes monorepo to v0.36.4 ([0a984f6](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/0a984f66201bc654b1cfb57570a75aa97ca3785a))

## [0.4.0](https://github.com/ermakov-oleg/cnpg-pg-doorman/compare/v0.3.0...v0.4.0) (2026-08-20)


### Features

* binaries manifest and HTTPS delivery server ([2460ec2](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/2460ec264756ad3b3fcf989475e3939f02edaef6))
* binary spec contract and runtime binary paths ([899a48a](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/899a48a01bb7c2950199e2eab152a6e5e61a76cf))
* in-place pg_doorman binary upgrade (SIGUSR2 supervision) ([b3fd914](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/b3fd914f77d667d2bcb8f860b41433f9b5868b50))
* in-place upgrades are opt-in per cluster via inPlaceUpgrades parameter ([20d69c7](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/20d69c748df239e45cafa0a0b04063c6024488a3))
* live in-place binary upgrade on binary spec change ([c64fc47](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/c64fc4787df98585022fa2664543ba9a0bc5caf7))
* pg_doorman binary delivery channel for in-place upgrades ([9fdd8f1](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9fdd8f11a419521e47a994d713e914ed7a077159))
* plugin image carries pg_doorman binaries and serves them over TLS ([1886606](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/188660672bc2d54f6a6523b2f4e849a71b79401f))
* publish desired binary spec in the rendered config Secret ([739ecec](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/739ecec8672ed6d4fb29462570a97fc0a4c9ab75))
* SIGUSR2 binary upgrade with successor adoption in the supervisor ([44488d8](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/44488d86bfae494cab757c07a84b54fd7a4a6efc))
* wrapper runs pg_doorman from tmpfs and syncs the desired binary at startup ([a68b60a](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/a68b60a7c85a027ca9b8fcbfd3b59948e7002159))


### Bug Fixes

* bound the binary download, harden the delivery server, add an image-binary revert path ([c3bf718](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/c3bf718525fa0bb6357030087846b060deae81f7))
* bounded post-cancel drain in waitUntilGone ([9f6e522](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9f6e522ba3c86c8e4f9bbe7cf84e02168025e6b2))
* cap RLIMIT_NOFILE so pg_doorman upgrade children start within the readiness window ([9ba03a5](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9ba03a5806bd63e6686cc92200010df801cf96f2))
* enforce a single supervised pooler and gate concurrent binary upgrades ([1dc31a9](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/1dc31a92ecd47e88bc4b8b31f68ca00387971d06))
* harden upgrade handover against stale flags and validator adoption ([29cf53a](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/29cf53afe94222b95543528ded9768819577b7ad))
* lint and doc comments in binary spec helpers ([c50806b](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/c50806b6cadf878e35e5f56e23f74f7547a9bbd0))
* mirror binary-delivery SAN fix into the helm chart ([09e3211](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/09e32112c4890ace3b63d234bf44df74051cb17a))
* nolint deprecated GetEventRecorderFor pending events API migration ([9c058fa](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9c058fa8e834fb8505b15bee4a0348bd6dec1e26))
* plugin serving cert covers the service FQDN used for binary delivery ([7f74614](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/7f7461487389da3709ccc6595f72d37c8fa2a92c))
* retry the desired binary after a failed startup sync ([5ecaed5](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/5ecaed5936d3713a82ed307c2ed271600a37f31c))
* retry the initial config on the image binary and derive the spec path from the config source ([4b0a6b7](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/4b0a6b75073eba8a8b2454f0370f17e0705696ad))

## [0.3.0](https://github.com/ermakov-oleg/cnpg-pg-doorman/compare/v0.2.0...v0.3.0) (2026-08-19)


### ⚠ BREAKING CHANGES

* render config into per-cluster Secret - pods get zero RBAC
* PgDoorman spec.clusterRef - source of truth for the Cluster relation
* remove plaintext adminPassword from the API

### Features

* client TLS on the pooler port via CNPG server certificate ([fcc3272](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/fcc3272de48f280e7bb83ee1286df2815c173da7))
* configurable sidecar resources via plugin parameters ([901aa23](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/901aa237e33f0d4dabcd276a2c23797ef79ee3c3))
* configurable sidecar resources via plugin parameters ([f01f151](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/f01f1519b1eaab211c4a7dbbd8f39ccd1997bb8f))
* create &lt;cluster&gt;-doorman-rw Service; cleanup plugin resources when disabled ([28c9f55](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/28c9f552ef0f29d3d2455b48d38c4bc09cc15eda))
* drop pooler sessions on instance demotion ([77a791a](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/77a791a8190a06edfd0e25d93dc68ccf3dc924d0))
* drop pooler sessions on instance demotion via downward-API role watch ([5f6fa32](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/5f6fa32d179ba4c72dcb1f96699475b75aad3487))
* Helm chart with OCI publishing ([f41771a](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/f41771a3700791d7367b190de4bbf12de2165729))
* Helm chart with OCI publishing ([7aa3045](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/7aa304596090b5e17cbf12692eaec61f8d2406ee))
* managed pooler Service + cleanup of plugin resources when disabled ([6370c4e](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/6370c4e26cf6a8a4496f8794cb693b14fc46481b))
* PgDoorman spec.clusterRef - source of truth for the Cluster relation ([3d88407](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/3d88407e2169177c685e1890b10bfb5480cdd5b9))
* PgDoorman status conditions, render events, wrapper Prometheus metrics ([fa9cfa6](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/fa9cfa6e9e6b155b218ed5bbc0bbb21a5c79b579))
* plugin server metrics - endpoint enabled, hooks instrumented ([90f1712](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/90f1712e457971a06b8b0a2c088cc0baf730f7bd))
* plugin server metrics endpoint + hook duration/error instrumentation ([c358412](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/c3584126cf66d29b5517632b263238d89ef58f56))
* remove plaintext adminPassword from the API ([16b70f0](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/16b70f01947aaf63f367144e55aa6cd277cda346))
* render config into per-cluster Secret - pods get zero RBAC ([0cd5a20](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/0cd5a2008fb8071b4f148fef54810f8781738dc3))
* rendering status conditions, events, wrapper metrics ([cb0d0dc](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/cb0d0dcbe3a993ab4e6aa659728257f1c1ca898d))
* terminate client TLS on pooler port with CNPG server certificate ([114907f](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/114907fe936c3cb5920e49e085639cba1f6f65df))


### Bug Fixes

* convert TLS key to PKCS[#8](https://github.com/ermakov-oleg/cnpg-pg-doorman/issues/8) - pg_doorman rejects SEC1 EC keys from CNPG ([1b430ff](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/1b430ff2e711bdc7c1c2569d008b929d06e737c5))
* detect CR delete+recreate via UID; reset restart backoff after long uptime ([dd87c81](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/dd87c8145e812a76dc122411c98e996403a66315))
* detect delete+recreate of PgDoorman CR via UID; reset restart backoff after long uptime ([9e91f2d](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9e91f2d7536e550f28edcacc07ff79b4e3cd73d9))
* disable wrapper metrics listener on :8080; configurable log level ([f7939c4](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/f7939c43deaaf553bb60b583480a61bfbaf2993c))
* drop pg_doorman version qualifiers from docs, keep plan.md untouched ([7c42349](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/7c42349ab5a1fb7a695512ef0ab1a2e42b2b2f44))
* graceful shutdown - send SIGTERM to pg_doorman and wait for exit instead of SIGKILL ([804d224](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/804d2240bd0bf7138ece0d868ee2bf97e13ed98f))
* graceful shutdown of pg_doorman on pod termination ([936e69e](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/936e69e11b0a756566f543c4292c6986c3e61c55))
* gracefully restart pg_doorman when non-reloadable config fields change ([24bfc3c](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/24bfc3cda90af69bcdcba3d7cdd79c69d26ac1cb))
* issue plugin TLS certs from a shared CA, verify clients by CA ([accef85](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/accef85bbf20a5f353fdee18d3240e2ad336bbba))
* lint - avoid unchecked Close in tlskey test ([91e3ae6](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/91e3ae6e00753aff18c225a1c197dbce3c19a658))
* map auth_query pool sizes to pg_doorman v3.11.0 keys ([da40b35](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/da40b35d4d0a7d926a996bf2ca129c83fe39f064))
* map auth_query pool sizes to pg_doorman v3.11.0 keys (workers/pool_size) ([dfba0c7](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/dfba0c7341d6484c5b2bf41d29c97e7da30117e2))
* missing CR no longer freezes a running cluster; in-use finalizer on PgDoorman ([bff1e86](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/bff1e86e9c3346c8a8fc76d171c6584e0b3770d2))
* missing PgDoorman CR must not freeze a running cluster ([5b4b711](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/5b4b7114d3a7948451d25be5f6a93f54aa16f9d3))
* module path points at a non-existent repository ([fd22cd0](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/fd22cd05d3fbfcf81c2a4f6e50e33cb1c9e8932b))
* no :8080 listener in the PG pod; configurable log level ([45f2916](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/45f2916013502d9e59161b45d797fd632adba0ec))
* non-reloadable config fields now restart pg_doorman instead of silently no-op ([47c2d4d](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/47c2d4d8f022d74650e48b447f511ebaac8a55a1))
* plugin server HA - 2 replicas, RollingUpdate, PDB, anti-affinity, resources, liveness, leader election ([9aaed18](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9aaed18ea179508fd25eeb80b536477d788a1c91))
* plugin server is a SPOF - HA deployment with leader election ([978b7f1](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/978b7f1b5bcee9977a96c27e140a8c6423a5dca4))
* plugin server logging - --debug=false, context logr, correct levels ([d5ad091](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/d5ad09137ec4172c50a3cec0a068eb8a56ff951b))
* plugin server logging - disable debug default, context logr, correct levels ([a4edece](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/a4edecea38395291a71c5358becf8b4cf820cd17))
* plugin TLS from a shared CA - client cert rotation no longer breaks handshakes ([6fb114f](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/6fb114f4efc4a4ad6d4be7e1c166fb838de54c2f))
* random per-pod admin password instead of fixed change-me default ([e52678d](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/e52678da0cdabeb22c5a82b1c34ff5188b9f9947))
* random per-pod admin password instead of fixed change-me default ([28e075f](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/28e075ff184b1e34b362def6a966fb11bac6a531))
* reject pooler/metrics ports reserved by the CNPG instance pod ([5ab11a8](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/5ab11a80049d841f268ede8a781c452436df45a9))
* reject reserved pod ports in plugin parameter validation ([96246ee](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/96246eee29917bb6419022a8bfd26339a006e260))
* rejected config must not poison the runtime config file ([85b0e83](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/85b0e83f38238b97a80e2be60a03c0fa97b88747))
* rename module path to the real repository github.com/ermakov-oleg/cnpg-pg-doorman ([9c9a297](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9c9a297e4ec6305a206222e61188fbd03940cc60))
* rename sidecar metrics port to pgd-metrics, document metrics scraping ([5b3dfaa](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/5b3dfaa4406532da88b15b2f84037b791368168e))
* replace deprecated controller-runtime scheme.Builder ([35b2395](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/35b239539e0e6ae36176bb673494ad7cc4f0b9a1))
* replace deprecated controller-runtime scheme.Builder with apimachinery SchemeBuilder ([d6ab338](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/d6ab3386886f0e9503bd8a9630c6d122d0fd1e51))
* sidecar port 'metrics' collides with the CNPG postgres container ([6bd8e89](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/6bd8e89ac34c1222d04f57e3516dd99576e13a96))
* sidecar probes - broken pooler must not gate PostgreSQL pod readiness ([be3e99f](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/be3e99f8c46a3df39936ebccf6374c89c39ef4e2))
* sidecar probes - liveness on wrapper /healthz, drop readiness gating pod ([d0edfd7](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/d0edfd74f56bc645c382c6feb02323928dd5f2e3))
* tmpfs scratch volume and 0600 config file - plaintext passwords off node disk ([44a9b74](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/44a9b74c978f1fa68ef5cabbc097910429fa932b))
* validate config with pg_doorman --test-config before replacing runtime file ([7dbedfc](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/7dbedfc2ab6ff177103859ab0bb2428617183335))
* validate PgDoorman values (kubebuilder markers, CEL rule, wrapper value checks) ([718c3b1](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/718c3b1f715e078644cc534b3af156da0015fc22))
* validate PgDoorman values (kubebuilder markers, CEL rule, wrapper value checks) ([01451ce](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/01451ce6eab5b3331242c68eb042128eab044a63))

## [0.2.0](https://github.com/ermakov-oleg/cnpg-pg-doorman/compare/v0.1.0...v0.2.0) (2026-03-03)


### Features

* add cluster and configmap examples ([c493907](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/c493907e50200208d22e867e414efad1c4668357))
* add e2e test cases (sidecar injection, connection) ([cf4337f](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/cf4337f5b93fc13b2a13db3c51baa2350bba2929))
* add e2e test framework (suite, client, helpers) ([06ae045](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/06ae04591f4c29af0f8292c143a73466e6136628))
* add Go wrapper (process, watcher, validator) ([733c9d1](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/733c9d10a1874f14c69116735fe9ca4ca465c0d3))
* add Kubernetes manifests (deployment, service, certs) ([a9c77e2](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/a9c77e24eaf6a61149af8b7366b21492b9c702c4))
* add LifecycleHook with sidecar injection ([28281c3](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/28281c30dd8be1c7c65d131980390d9670ed8ee5))
* add Operator service (validation, mutations) ([6c3437e](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/6c3437e49d24649a1ce2ff5b9fd849c88e6a5a01))
* add plugin entry point with gRPC server ([4de193b](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/4de193b06dd4f7369f203b2761179500511b29b5))
* add unit tests and complete e2e test cases (11 scenarios) ([e3fe879](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/e3fe879e46e81de8ef0bbdd5e2b7f81ee13fc0d1))
* cnpg-pg-doorman — CNPG plugin for pg_doorman sidecar injection ([b8e6350](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/b8e6350645919967d81873527e1b79f9e672fdf2))
* migrate from ConfigMap to PgDoorman CRD ([b2e8b5a](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/b2e8b5a71a5f0e9dac8b21848023790bd8699761))
* migrate from ConfigMap to PgDoorman CRD for pooler configuration ([25a1d2f](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/25a1d2f06f279fb4d231980d2bb47d841ffc93cf))


### Bug Fixes

* add missing RBAC verbs, validate sidecar image, remove unnecessary debounce ([9aac14e](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/9aac14e658fc1f5be04d5d99973928b04930a247))
* address all code review findings from PR [#4](https://github.com/ermakov-oleg/cnpg-pg-doorman/issues/4) ([874ddd1](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/874ddd1e15f80bb8946e275eb3b4a937c1a9fb9c))
* admission skip for clusters without plugin, secret rotation detection, strict param validation ([02c7a1d](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/02c7a1db827d1a278f6c290b7e066dcb0460e2a8))
* admission skip, secret rotation detection, strict param validation ([90a488f](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/90a488f46dd69c781cdd760551323edbb0f957ab))
* e2e tests — WebSocket exec, auth_query, Optional ConfigMap, K8s 1.35 compat ([f525254](https://github.com/ermakov-oleg/cnpg-pg-doorman/commit/f525254d9f4dc2a1506d5ac8c4ad1523f1d4f5bf))
