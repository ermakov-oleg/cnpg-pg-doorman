# CNPG Plugin для pg_doorman

## Context

CloudNativePG (CNPG) — оператор для управления PostgreSQL в Kubernetes. pg_doorman — высокопроизводительный connection pooler для PostgreSQL (Rust, многопоточный). Цель — создать CNPG плагин, который автоматически инжектирует pg_doorman как sidecar-контейнер в поды кластера, используя auth_query в passthrough-режиме для аутентификации пользователей.

Плагин реализуется через cnpg-i (gRPC-протокол для плагинов CNPG) — по паттерну cnpg-i-hello-world и plugin-barman-cloud.

## Архитектура

```
┌──────────────────────────────────────────────────────────────────┐
│ Kubernetes Pod (CNPG Instance)                                   │
│                                                                  │
│  ┌──────────────────┐                   ┌──────────────────┐     │
│  │ Sidecar container │                   │  PostgreSQL       │     │
│  │ (Go wrapper +     │  localhost:5432   │  (CNPG managed)   │     │
│  │  pg_doorman)       │ ───────────────► │                   │     │
│  │                   │  passthrough auth │                   │     │
│  │ wrapper:          │                   │                   │     │
│  │  - validates YAML │  auth_query ────► │  pg_shadow        │     │
│  │  - watches config │  (doorman_auth)   │                   │     │
│  │  - sends SIGHUP   │                   └──────────────────┘     │
│  │ pg_doorman:       │                                           │
│  │  - :6432 clients  │    ┌──────────────────┐                   │
│  │  - :9127 metrics  │◄───│ ConfigMap volume  │ (user-managed)   │
│  └──────────────────┘    └──────────────────┘                   │
│       ▲                                                          │
└───────│──────────────────────────────────────────────────────────┘
        │ client connections
   Kubernetes Service :6432
```

- Плагин — standalone Deployment в `cnpg-system`, общается с CNPG оператором по gRPC + mTLS (порт 9090)
- Через LifecycleHook инжектирует pg_doorman sidecar в каждый Pod кластера
- pg_doorman подключается к локальному PostgreSQL (`localhost:5432`), слушает клиентов на `0.0.0.0:6432`
- Конфиг pg_doorman — пользовательский ConfigMap, монтируется как volume в sidecar

## Обновление конфига (hot reload)

pg_doorman поддерживает hot reload конфига через **SIGHUP** и **RELOAD** admin-команду. Конфиг управляется пользователем через ConfigMap.

### Архитектура обновлений:

```
User updates ConfigMap → K8s propagates volume (~60s) → Wrapper detects change → SIGHUP
                                                                                    ↓
                                                         pg_doorman reloads config (zero downtime)
```

- Пользователь сам создаёт и обновляет ConfigMap с конфигом pg_doorman
- В `spec.plugins[].parameters` указывается имя ConfigMap
- LifecycleHook монтирует этот ConfigMap как volume в sidecar
- Go wrapper следит за изменением файла, валидирует новый конфиг и шлёт SIGHUP
- **Ноль дополнительного RBAC** — плагин не читает и не пишет ConfigMap, только ссылается на него в pod spec
- При reload: неизменённые пулы переиспользуются, кеш auth_query сохраняется

### Что вызывает rolling update (неизбежно):
- Смена `image` sidecar → другой образ контейнера
- Смена `poolerPort` / `metricsPort` → другие порты в pod spec
- Смена `configMapName` → другой volume mount

### Что НЕ вызывает rolling update (hot reload через ConfigMap):
- Всё содержимое pg_doorman конфига (poolMode, poolSize, auth_query, worker_threads и т.д.)
- Пользователь просто редактирует ConfigMap

## Модель подключений (справка для примеров)

Пользователь настраивает всё через ConfigMap. Для примеров и документации:

### Два уровня лимитов в pg_doorman:
| Настройка конфига | Что ограничивает | Default |
|-------------------|-----------------|---------|
| `general.max_connections` | Client → pg_doorman (макс клиентов) | 8192 |
| `auth_query.pool_size` (до v3.11.0 — `default_pool_size`) | pg_doorman → PostgreSQL (пул на пользователя) | 40 |

### Auth query (passthrough):
- **Executor**: спец. роль (`doorman_auth`) подключается к PG для `SELECT passwd FROM pg_shadow`
- **Data**: клиентские креды переиспользуются passthrough (MD5/SCRAM ClientKey)
- Нужно: pg_hba trust для localhost + SECURITY DEFINER функция + роль doorman_auth

> **Known limitation (security):** `pg_hba trust` для localhost означает, что любой процесс внутри пода может подключиться к PostgreSQL без пароля. Это стандартный подход в CNPG (сам оператор использует local trust), риск ограничен периметром пода. В будущем можно перейти на SCRAM-аутентификацию для `doorman_auth` (пароль в Secret).

## Структура проекта

```
cnpg-pg-doorman/
├── cmd/
│   ├── plugin/plugin.go          # gRPC plugin entry point
│   └── wrapper/main.go           # Sidecar wrapper entry point
├── internal/
│   ├── identity/impl.go
│   ├── lifecycle/
│   │   ├── lifecycle.go          # GetCapabilities, LifecycleHook dispatch
│   │   └── reconcile_pod.go      # Sidecar injection
│   ├── operator/
│   │   ├── impl.go               # GetCapabilities
│   │   ├── validation.go         # ValidateClusterCreate/Change
│   │   └── mutations.go          # MutateCluster (defaults)
│   ├── config/config.go          # Plugin parameters parsing
│   ├── wrapper/                  # Shared between cmd/wrapper and tests
│   │   ├── watcher.go            # File watcher (ConfigMap changes)
│   │   ├── validator.go          # pg_doorman config validation
│   │   └── process.go            # pg_doorman process management (start, SIGHUP)
│   └── utils/utils.go            # Helpers (GetKind и т.д.)
├── main.go                       # Plugin main
├── pkg/metadata/metadata.go
├── kubernetes/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── certificate-issuer.yaml
│   ├── client-certificate.yaml
│   ├── server-certificate.yaml
│   └── kustomization.yaml
├── test/e2e/
│   ├── e2e_suite_test.go         # Ginkgo suite setup
│   └── internal/
│       ├── client/client.go      # K8s client factory
│       ├── certmanager/          # cert-manager installation
│       ├── cloudnativepg/        # CNPG operator installation
│       ├── cluster/cluster.go    # Cluster readiness checks
│       ├── command/command.go    # Pod exec utilities
│       ├── namespace/            # Namespace management
│       └── tests/
│           └── pooler/
│               ├── pooler_test.go  # Connection pooling e2e tests
│               └── fixtures.go     # Cluster fixtures
├── examples/
│   ├── cluster-example.yaml      # Cluster spec с плагином
│   └── configmap-example.yaml    # ConfigMap с конфигом pg_doorman
├── Taskfile.yml
├── Dockerfile                    # Plugin gRPC server
├── Dockerfile.wrapper            # Wrapper sidecar (Go + pg_doorman)
├── go.mod
└── .gitignore
```

### Два Docker-образа:

1. **Plugin** (`Dockerfile`) — gRPC сервер (standalone Deployment в cnpg-system)
2. **Wrapper** (`Dockerfile.wrapper`) — Go wrapper + pg_doorman binary (sidecar в Pod):
   ```dockerfile
   # Build Go wrapper
   FROM golang:1.25 AS go-builder
   COPY . /app
   RUN CGO_ENABLED=0 go build -trimpath -o /doorman-wrapper ./cmd/wrapper/

   # Get pg_doorman binary from official image (пиннить по digest в production)
   FROM ghcr.io/ozontech/pg_doorman:latest AS doorman

   # Final image
   FROM gcr.io/distroless/static:nonroot
   COPY --from=go-builder /doorman-wrapper /usr/local/bin/doorman-wrapper
   COPY --from=doorman /usr/bin/pg_doorman /usr/local/bin/pg_doorman
   USER 10001:10001
   ENTRYPOINT ["/usr/local/bin/doorman-wrapper"]
   ```

   > **Рекомендации по образам:** В production пиннить `ghcr.io/ozontech/pg_doorman` по digest вместо `latest`. Убедиться что pg_doorman — статически слинкованный бинарь (совместим с distroless/static). Использовать `-trimpath` для воспроизводимой сборки.

## Реализуемые cnpg-i сервисы

1. **Identity** (обязательный) — метаданные плагина, capabilities, health probe
2. **OperatorLifecycle** — инжекция pg_doorman sidecar в Pod через JsonPatch
3. **Operator** — валидация параметров при создании/изменении Cluster, defaulting

## Параметры плагина (Cluster spec)

```yaml
spec:
  plugins:
  - name: pg-doorman.cnpg.io
    parameters:
      poolerPort: "6432"                               # Порт для клиентов
      metricsPort: "9127"                              # Порт метрик
      configMapName: "my-cluster-pg-doorman"           # ConfigMap с конфигом pg_doorman (обязательный)
```

Минимальный набор параметров — только то, что влияет на pod spec. `image` sidecar определяется env `SIDECAR_IMAGE` на Deployment плагина (содержит Go wrapper + pg_doorman). Вся конфигурация pg_doorman живёт в ConfigMap, которую пользователь создаёт сам.

## Шаги реализации

### Шаг 1: Инициализация проекта
- `git init`, `go mod init github.com/o-ermakov/cnpg-pg-doorman`
- Зависимости: `cnpg-i v0.3.1`, `cnpg-i-machinery v0.4.2`, `cnpg-api`, `machinery`, `cobra`
- `.gitignore`, `Dockerfile`, `Dockerfile.wrapper`, `Taskfile.yml`

### Шаг 2: Metadata и Identity
- `pkg/metadata/metadata.go` — PluginName = `pg-doorman.cnpg.io`
- `internal/identity/impl.go` — GetPluginMetadata, GetPluginCapabilities (LIFECYCLE + OPERATOR), Probe

### Шаг 3: Конфигурация плагина
- `internal/config/config.go` — парсинг параметров:
  - `poolerPort` (default: `6432`)
  - `metricsPort` (default: `9127`)
  - `configMapName` (обязательный) — имя ConfigMap с конфигом pg_doorman
- Валидация: configMapName не пустой, порты > 0
- Sidecar image берётся из env `SIDECAR_IMAGE` на plugin Deployment

### Шаг 4: Go wrapper (`cmd/wrapper/`)
Go-бинарь, запускаемый как entrypoint в sidecar. Функции:

- `internal/wrapper/process.go` — запуск pg_doorman как child process, передача сигналов, graceful shutdown, restart с exponential backoff при crash
- `internal/wrapper/watcher.go` — polling ConfigMap-файла каждые 5 секунд (hash comparison), debounce 2-3с для защиты от signal storm
- `internal/wrapper/validator.go` — парсинг и валидация pg_doorman YAML:
  - Проверка обязательных полей (general.host, general.port, pools)
  - Проверка auth_query конфигурации
  - Проверка что server_host/port указаны
  - При невалидном конфиге: **не применять**, логировать ошибку (видно в `kubectl logs`)

Логика wrapper:
```
1. Прочитать конфиг из /etc/pg_doorman/configmap/pg_doorman.yaml
2. Валидировать YAML (если невалидный — лог ошибки, ждать исправления)
3. Atomic write в /tmp/pg_doorman.yaml (write temp → fsync → rename)
4. Запустить pg_doorman /tmp/pg_doorman.yaml как child process
5. Цикл:
   a. Каждые 5 секунд проверять hash файла
   b. Если изменился → debounce 2-3с (защита от быстрых последовательных правок)
   c. Валидировать новый конфиг
   d. Если валидный → atomic write (temp → fsync → rename) + SIGHUP
   e. Проверить результат reload (парсить stdout pg_doorman или проверить admin-порт)
   f. Лог "config reloaded successfully" или "config reload failed: <reason>"
   g. Если невалидный → лог ошибки, НЕ применять, pg_doorman работает со старым конфигом
6. При неожиданном завершении pg_doorman → restart с backoff (1s, 2s, 4s, max 30s)
7. При SIGTERM/SIGINT → forward сигнал pg_doorman → graceful shutdown
```

### Шаг 5: LifecycleHook — инжекция sidecar
- `internal/lifecycle/lifecycle.go` — GetCapabilities (Pod CREATE + EVALUATE), dispatch
- `internal/lifecycle/reconcile_pod.go`:
  1. Декодировать Cluster и Pod
  2. Парсить параметры (ports, configMapName)
  3. Добавить ConfigMap volume (имя из `configMapName`, readOnly)
  4. Добавить emptyDir volume для `/tmp` (writable scratch для atomic config writes)
  5. Sidecar контейнер (init container с RestartPolicy: Always):
     - image: wrapper (из env `SIDECAR_IMAGE`, содержит Go wrapper + pg_doorman binary)
     - volumeMount: `/etc/pg_doorman/configmap` (ConfigMap, readOnly)
     - volumeMount: `/tmp` (emptyDir, writable)
     - ports: pooler + metrics
     - resources: requests (cpu: 50m, memory: 64Mi), limits (cpu: 500m, memory: 256Mi) — дефолтные, подбираются под нагрузку
     - readiness/liveness: TCP на poolerPort
     - securityContext: non-root, drop ALL, allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, seccompProfile: RuntimeDefault
  6. JsonPatch через `object.CreatePatch()`

### Шаг 6: Operator Service
- `internal/operator/impl.go` — VALIDATE_CLUSTER_CREATE, VALIDATE_CLUSTER_CHANGE, MUTATE_CLUSTER
- `internal/operator/validation.go` — configMapName обязателен, порты валидны
- `internal/operator/mutations.go` — defaults для ports

### Шаг 7: Entry point
- `main.go` — cobra root command
- `cmd/plugin/plugin.go` — `http.CreateMainCmd()`, регистрация Identity + Operator + Lifecycle

### Шаг 8: Kubernetes-манифесты
- Service с `cnpg.io/pluginName: pg-doorman.cnpg.io`
- Deployment с TLS volumes + env `SIDECAR_IMAGE`
- cert-manager Certificate/Issuer для mTLS
- kustomization.yaml (namespace: cnpg-system)
- Без дополнительного RBAC — плагин не обращается к K8s API

### Шаг 9: Примеры
- `examples/configmap-example.yaml` — ConfigMap с полным конфигом pg_doorman для типичного использования (auth_query passthrough, transaction mode, prometheus)
- `examples/cluster-example.yaml` — Cluster spec с плагином + pg_hba + bootstrap SQL

### Шаг 10: E2E тесты
По паттерну plugin-barman-cloud (`/Users/o.ermakov/projects/plugin-barman-cloud/test/e2e/`):

**Фреймворк:** Ginkgo v2 + Gomega

**Структура `test/e2e/`:**
- `e2e_suite_test.go` — SynchronizedBeforeSuite:
  1. Установить cert-manager (kustomize + wait deployments)
  2. Установить CNPG оператор (kustomize image patch)
  3. Собрать и задеплоить плагин (kustomize с image override)
- `internal/client/` — создание K8s клиента (controller-runtime)
- `internal/certmanager/` — установка cert-manager
- `internal/cloudnativepg/` — установка CNPG оператора
- `internal/cluster/` — IsReady() проверка + Eventually polling
- `internal/command/` — ExecuteInContainer (pod exec)
- `internal/namespace/` — CreateUniqueNamespace

**Тест-кейсы (`test/e2e/internal/tests/pooler/`):**

1. **Sidecar injection** — создать Cluster с плагином, проверить что pg_doorman контейнер запустился в каждом поде
2. **Connection via pooler** — подключиться к порту 6432, выполнить `SELECT 1`
3. **Auth query works** — подключиться разными пользователями через пулер, проверить что данные разделены
4. **Transaction pooling** — проверить что в transaction mode подключения переиспользуются
5. **Metrics endpoint** — проверить что порт 9127 отдаёт Prometheus метрики
6. **Config hot reload** — обновить ConfigMap (например poolMode), проверить что pg_doorman перезагрузил конфиг через SIGHUP БЕЗ рестарта пода
7. **Config validation** — задать невалидный конфиг в ConfigMap, проверить что wrapper логирует ошибку и pg_doorman продолжает работать со старым конфигом
8. **Image update → rolling restart** — изменить sidecar image, проверить что поды пересозданы (rolling update)
9. **Missing ConfigMap** — создать Cluster с несуществующим configMapName, проверить поведение пода (wrapper ждёт появления файла, логирует ошибку)
10. **Rapid config updates** — обновить ConfigMap несколько раз подряд за короткий период, проверить что debounce работает и финальный конфиг применён
11. **Sidecar restart on crash** — убить процесс pg_doorman внутри sidecar, проверить что wrapper перезапускает его с backoff

**Fixtures:**
- ConfigMap с pg_doorman конфигом (auth_query passthrough, transaction mode)
- Cluster spec с плагином (configMapName ссылается на ConfigMap) + pg_hba + bootstrap SQL для doorman_auth

**Запуск:** `Taskfile.yml` с таргетами:
- `task build-plugin-image` — собрать Docker-образ gRPC плагина
- `task build-wrapper-image` — собрать Docker-образ wrapper sidecar (Go wrapper + pg_doorman)
- `task start-kind` — создать kind cluster
- `task e2e` — полный цикл: kind + cert-manager + CNPG + deploy plugin + run tests

## Ключевые файлы-образцы

| Файл | Для чего |
|------|----------|
| `/Users/o.ermakov/projects/pg_doorman/pg_doorman.yaml` | Формат конфига pg_doorman (для примера ConfigMap) |
| `/Users/o.ermakov/projects/pg_doorman/src/auth/auth_query.rs` | Реализация auth_query и SIGHUP reload |
| `/Users/o.ermakov/projects/plugin-barman-cloud/test/e2e/` | Паттерн e2e тестов |
| `/Users/o.ermakov/projects/plugin-barman-cloud/go.mod` | Актуальные версии зависимостей |
| cnpg-i-hello-world (GitHub) | Эталон структуры плагина (identity, lifecycle, operator) |

## Known Limitations

- **pg_hba trust для localhost** — стандартный подход в CNPG, но любой процесс в поде может подключиться к PG без пароля. Риск ограничен периметром пода.
- **Нет валидации существования ConfigMap** — плагин не имеет RBAC для чтения ConfigMap. Тайпо в `configMapName` приведёт к ошибке при старте пода, а не при admission. Wrapper логирует понятную ошибку и ждёт появления файла.
- **Reload latency** — до ~65с (ConfigMap propagation ~60с + polling 5с). Это ограничение Kubernetes projected volumes.

## Верификация

1. `go build ./...` — компиляция
2. `go vet ./...` — статический анализ
3. E2E тесты в kind-кластере:
   - cert-manager + CNPG оператор + плагин задеплоен
   - Cluster с плагином создан, все инстансы ready
   - pg_doorman sidecar запущен в каждом поде
   - Подключение через порт 6432 работает
   - Auth query аутентифицирует пользователей
   - Prometheus метрики доступны
   - **Hot reload**: обновить ConfigMap → подождать ~60с → pg_doorman перезагрузил конфиг через SIGHUP → поды НЕ перезапущены
   - **Config validation**: невалидный конфиг → wrapper логирует ошибку → pg_doorman работает со старым
