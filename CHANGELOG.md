# Changelog

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
