# Инфраструктура и деплой

## Обзор стека

Полная инфраструктура для разработки и продакшена микросервисов на goplatform.

```
                    ┌──────────────┐
                    │  Приложение  │
                    │  (goplatform)│
                    └──────┬───────┘
                           │
        ┌──────────┬───────┼───────┬──────────┐
        ▼          ▼       ▼       ▼          ▼
   PostgreSQL    Kafka    Redis   Consul   Temporal
   (данные)    (события) (кэш)  (discovery) (workflow)
                           │
                    ┌──────┴───────┐
                    │ OTel Collector│
                    └──────┬───────┘
                     ┌─────┴─────┐
                     ▼           ▼
                   Tempo    Prometheus
                  (трейсы)  (метрики)
                     └─────┬─────┘
                           ▼
                        Grafana
                     (визуализация)
```

---

## Компоненты

### PostgreSQL 17

**Зачем**: основное хранилище данных. Используется для бизнес-данных, состояния приложения, результатов обработки.

**Почему PostgreSQL**: зрелая СУБД с JSON поддержкой, расширениями (PostGIS, pg_trgm), надёжными транзакциями. pgxpool обеспечивает connection pooling и автоматический reconnect.

**В goplatform**: `pkg/postgres` оборачивает pgxpool, предоставляет `WithTx` для транзакций, `Migrate`/`MigrateDown` для миграций, query hooks для логирования slow queries.

### Apache Kafka (KRaft)

**Зачем**: event bus для асинхронного обмена сообщениями между сервисами. Используется для event sourcing, CQRS, saga-паттернов.

**Почему KRaft**: Kafka без ZooKeeper — проще в эксплуатации, один процесс вместо двух. KRaft — встроенный консенсус-протокол, заменяющий ZooKeeper с Kafka 3.3+.

**Почему Kafka вообще**: гарантии доставки (at-least-once), упорядоченность внутри партиции, replay (перечитать события), consumer groups для горизонтального масштабирования.

**В goplatform**: `pkg/broker/kafka` — Producer (publish hooks, trace injection) и Consumer (middleware, retry, DLQ). При недоступности DLQ offset не коммитится — данные не теряются.

### Redis 7

**Зачем**: кэш, сессии, распределённые локи, idempotency store. Всё, что требует быстрого KV доступа с TTL.

**Почему Redis**: in-memory скорость (микросекунды), встроенный TTL, атомарные операции (SetNX), pub/sub для real-time уведомлений.

**В goplatform**: `pkg/redis` — JSON-based Set/Get/SetNX/Del. Реализует `server.IdempotencyStore` (duck typing) для дедупликации HTTP запросов. `Unwrap()` даёт доступ к любым Redis командам.

### Consul 1.17

**Зачем**: service discovery и health checking. Сервисы регистрируются в Consul, другие сервисы находят их через DNS или HTTP API.

**Почему Consul**: зрелый service mesh, HTTP API, DNS interface, health checks с TTL, blocking queries для real-time обновлений. Stateless HTTP клиент — не нужен reconnect.

**В goplatform**: `pkg/discovery/consul` — регистрация с TTL check, Discover с фильтрацией по health, Watch через blocking query. `Resolver` — client-side round-robin с TTL кэшем.

### Temporal

**Зачем**: оркестрация long-running workflows. Саги, scheduled tasks, человеческие задачи (human-in-the-loop), retry с backoff на уровне workflow.

**Почему Temporal**: durable execution — workflow переживает рестарты, сбои, деплои. State автоматически сохраняется. Retry, таймауты, сигналы — встроены. В отличие от самописных state machines — Temporal гарантирует exactly-once семантику для workflow logic.

**В goplatform**: `pkg/workflow` — Client (ExecuteWorkflow, SignalWorkflow) и Worker (регистрация workflows/activities) как `platform.Component` с lifecycle.

---

## Observability стек

### Как данные проходят через стек

```
Приложение
  │
  │ OTLP gRPC (:4317)
  ▼
OTel Collector          ← единая точка входа для всей телеметрии
  │
  ├── traces → Tempo     ← хранилище трейсов
  └── metrics → Prometheus ← хранилище метрик
                │
                ▼
             Grafana      ← единый UI для трейсов и метрик
```

### OpenTelemetry Collector

**Зачем**: прокси между приложением и бэкендами наблюдаемости. Приложение отправляет данные в один endpoint (Collector), Collector маршрутизирует их в нужные хранилища.

**Почему не напрямую в Tempo/Prometheus**: Collector развязывает приложение от конкретных бэкендов. Можно менять хранилище трейсов (Tempo → Jaeger → Zipkin) без изменения кода. Collector также батчит данные, retry'ит при сбоях, и может сэмплировать трейсы (сохранять только часть для экономии ресурсов).

**Конфигурация** (`deploy/otelcol-config.yaml`):
- **Receivers**: OTLP gRPC (:4317) и HTTP (:4318) — приложение шлёт сюда
- **Processors**: batch — группирует данные для эффективной отправки
- **Exporters**: otlp → Tempo (трейсы), prometheus → endpoint :8889 (метрики)
- **Pipelines**: traces (receiver → processor → exporter), metrics (аналогично)

### Grafana Tempo

**Зачем**: хранилище и поиск distributed traces. Каждый запрос через систему генерирует trace — цепочку spans показывающую путь запроса через сервисы, БД, очереди.

**Почему Tempo**: минимальная конфигурация, не требует отдельной БД (в отличие от Jaeger), нативная интеграция с Grafana. Поддерживает OTLP — стандартный протокол OpenTelemetry.

**Что видно в трейсах**:
- ConnectRPC request → handler span → postgres query span → kafka publish span
- Consumer: kafka consume span → handler span → postgres query span
- Полная цепочка с одним trace_id через все сервисы

### Prometheus

**Зачем**: сбор и хранение метрик (time series). Количество запросов, латенсия, ошибки, размер очередей, состояние пулов соединений.

**Почему Prometheus**: де-факто стандарт для метрик в Kubernetes. Pull-модель (Prometheus сам забирает метрики), PromQL для запросов, AlertManager для алертов. OTel Collector экспортирует метрики в Prometheus-формате.

**Что собирается**:
- `http_server_request_count` — количество HTTP запросов
- `http_server_duration` — латенсия запросов (гистограмма)
- Custom business metrics через `obs.Meter("myservice").Int64Counter(...)`

### Grafana

**Зачем**: единый UI для визуализации трейсов (из Tempo) и метрик (из Prometheus). Дашборды, алерты, корреляция трейсов с метриками.

**В docker-compose**: анонимный доступ с ролью Admin — для разработки. В продакшене настраивается аутентификация.

---

## Helm chart

### Что такое Helm

Пакетный менеджер для Kubernetes. Helm chart — это шаблонизированный набор K8s манифестов. Вместо ручного написания Deployment, Service, ConfigMap для каждого сервиса — `helm install` генерирует и применяет их из values.

### Структура chart

```
deploy/helm/goplatform/
├── Chart.yaml           # имя, версия, описание chart
├── values.yaml          # значения по умолчанию
└── templates/
    ├── _helpers.tpl      # Go template функции (fullname, labels)
    ├── deployment.yaml   # Pod spec, health probes, config mount
    ├── service.yaml      # K8s Service для сетевого доступа
    └── configmap.yaml    # config.yaml из values
```

### Почему именно такая структура

**Deployment** содержит:
- **Health probes** — K8s проверяет `/healthz/live` (процесс жив?) и `/healthz/ready` (компоненты подключены?). Это те же endpoints из `pkg/server`.
- **ConfigMap volume** — конфиг монтируется как файл в pod, приложение читает его через `config.Loader`
- **Resource limits** — защита от OOM и CPU starvation
- **Config checksum annotation** — при изменении конфига pod автоматически перезапускается

**Service (ClusterIP)** — внутренний DNS для доступа к сервису из других pods. Для внешнего доступа добавляется Ingress (не включён в базовый chart — зависит от инфраструктуры).

**ConfigMap** — генерируется из `.Values.config`, позволяет менять конфигурацию без пересборки Docker образа.

### Когда расширять chart

Базовый chart покрывает типичный stateless микросервис. Для более сложных случаев добавляются:
- **Ingress** — внешний HTTP доступ
- **HPA (HorizontalPodAutoscaler)** — автомасштабирование по CPU/custom metrics
- **PDB (PodDisruptionBudget)** — минимальное число живых pod при rolling update
- **ServiceAccount + RBAC** — доступ к K8s API из pod
- **InitContainer** — для миграций перед стартом основного контейнера

---

## Порты (сводка)

| Порт | Протокол | Сервис | Назначение |
|------|----------|--------|------------|
| 5432 | TCP | PostgreSQL | SQL запросы |
| 9092 | TCP | Kafka | Produce/consume сообщений |
| 6379 | TCP | Redis | KV операции |
| 8500 | HTTP | Consul | Service discovery API + UI |
| 4317 | gRPC | OTel Collector | OTLP приём телеметрии |
| 4318 | HTTP | OTel Collector | OTLP приём (HTTP fallback) |
| 3200 | HTTP | Tempo | API трейсов |
| 9090 | HTTP | Prometheus | Метрики + PromQL UI |
| 3000 | HTTP | Grafana | Дашборды |
| 7233 | gRPC | Temporal | Workflow API |
| 8233 | HTTP | Temporal UI | Workflow UI |
