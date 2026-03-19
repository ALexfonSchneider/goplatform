# goplatform

Микросервисный SDK на Go. Управляемый lifecycle, observability из коробки, единый порт для REST и gRPC.

```go
obs, _ := observe.New(
    observe.WithServiceName("orderservice"),
    observe.WithOTLPEndpoint("localhost:4317"),
    observe.WithBaseHandler(slog.NewJSONHandler(os.Stdout, nil)),
)
logger := obs.Logger() // stdout + OTel, trace_id в каждой строке

app := platform.New(platform.WithLogger(logger))
app.Register("observe", obs)

db, _ := postgres.New(postgres.WithDSN(dsn), postgres.WithTracerProvider(obs.TracerProvider()))
app.Register("postgres", db)

srv, _ := server.New(server.WithAddr(":8080"))
srv.Use(observe.HTTPMiddleware(obs.TracerProvider(), obs.MeterProvider()))
srv.Mount("/api/v1/items", itemhandler.New(svc).Routes())
app.Register("server", srv)

app.Run(context.Background()) // SIGINT/SIGTERM → graceful shutdown
```

## Установка

```bash
go get github.com/ALexfonSchneider/goplatform
```

## Быстрый старт

```bash
# CLI
go install github.com/ALexfonSchneider/goplatform/cmd/goplatform@latest

# Сгенерировать проект (Clean Architecture)
goplatform init myservice --postgres --kafka --redis --connect

# Инфраструктура
docker-compose up -d

# Dev server с hot reload
goplatform run
```

Генерируемая структура:

```
myservice/
├── cmd/main.go
├── internal/
│   ├── domain/                    # entities + interfaces (порты)
│   │   ├── model.go
│   │   └── repository.go
│   ├── app/                       # use cases
│   │   └── service.go
│   └── adapters/
│       ├── handlers/itemhandler/  # primary — HTTP
│       ├── postgresrepo/          # secondary — PostgreSQL
│       ├── kafkaconsumer/         # primary — Kafka
│       └── natsconsumer/          # primary — NATS
├── config/                        # per-component YAML
├── proto/myservice/v1/            # ConnectRPC + protovalidate
├── migrations/
├── Makefile
└── Dockerfile
```

## Пакеты

| Пакет | Описание |
|-------|----------|
| **Ядро** | |
| [`pkg/platform`](pkg/platform/) | App, Component, Logger, Hooks, Plugin, Domain Errors |
| [`pkg/config`](pkg/config/) | Multi-source config: defaults → YAML → env (koanf) |
| [`pkg/observe`](pkg/observe/) | OTel traces + metrics + logs с trace_id в stdout |
| **Транспорт** | |
| [`pkg/server`](pkg/server/) | HTTP (chi) + ConnectRPC, single port, Recovery, RequestLogging, Idempotency |
| **Данные** | |
| [`pkg/postgres`](pkg/postgres/) | pgxpool, WithTx, миграции, query hooks, OTel spans |
| [`pkg/redis`](pkg/redis/) | go-redis, JSON Set/Get, SetNX (IdempotencyStore), Unwrap() |
| [`pkg/s3`](pkg/s3/) | AWS SDK v2, MinIO совместимый, Upload/Download/PresignedURL |
| **Messaging** | |
| [`pkg/broker`](pkg/broker/) | Publisher(Message)/PublishBatch, Subscriber, Middleware, PublishHook |
| [`pkg/broker/kafka`](pkg/broker/kafka/) | Producer/Consumer, DLQ, retry, W3C trace, SASL/TLS |
| [`pkg/broker/nats`](pkg/broker/nats/) | Core + JetStream, DLQ, queue groups |
| [`pkg/broker/membroker`](pkg/broker/membroker/) | In-memory для unit-тестов |
| **Инфраструктура** | |
| [`pkg/discovery`](pkg/discovery/) | Registry, Resolver (round-robin + TTL cache) |
| [`pkg/discovery/consul`](pkg/discovery/consul/) | Consul с blocking query Watch |
| [`pkg/workflow`](pkg/workflow/) | Temporal с OTel tracing |
| [`pkg/workflow/saga`](pkg/workflow/saga/) | Saga step builder с компенсациями |
| **Инструменты** | |
| [`pkg/platformtest`](pkg/platformtest/) | NewTestApp, NewTestServer, NewTestBroker, NewTestDB |
| [`cmd/goplatform`](cmd/goplatform/) | CLI: init, run (hot reload), migrate up/down/create |

## Observability

Traces + metrics + logs через один OTLP endpoint:

```
Приложение → OTel Collector → Tempo (traces) + Prometheus (metrics) + Loki (logs) → Grafana
```

Сквозной trace_id:

```
HTTP request → handler span → postgres query span → kafka publish (trace_id в headers)
  → consumer → handler span → postgres query span
```

```go
obs, _ := observe.New(
    observe.WithServiceName("myservice"),
    observe.WithOTLPEndpoint("localhost:4317"),
    observe.WithBaseHandler(slog.NewJSONHandler(os.Stdout, nil)),
)

// Providers готовы сразу после New() — не нужно ждать Start()
db, _ := postgres.New(postgres.WithTracerProvider(obs.TracerProvider()))
srv.Use(observe.HTTPMiddleware(obs.TracerProvider(), obs.MeterProvider()))

// Logger с trace_id в stdout и OTel export
logger := obs.Logger()
logger.InfoContext(ctx, "order created", "id", "ord-123")
// → {"msg":"order created","id":"ord-123","trace_id":"4bf92f...","span_id":"00f067..."}

// Custom метрики
counter, _ := obs.Meter("orders").Int64Counter("orders_created")
counter.Add(ctx, 1)
```

## Broker

```go
// Publish с headers
pub.Publish(ctx, broker.Message{
    Topic:   "orders",
    Key:     []byte("order-123"),
    Value:   payload,
    Headers: map[string]string{"content-type": "application/json"},
})

// Batch
pub.PublishBatch(ctx, []broker.Message{msg1, msg2, msg3})

// Hook — мутирует сообщение перед отправкой
kafka.WithPublishHook(func(ctx context.Context, msg *broker.Message) error {
    msg.Value = encrypt(msg.Value)
    msg.Headers["encrypted"] = "true"
    return nil
})
```

## Saga

```go
func OrderSaga(ctx workflow.Context, input OrderInput) error {
    return saga.New(ctx).
        Step(ReserveStock, ReleaseStock).
        Step(ChargePayment, RefundPayment).
        Step(CreateShipment, CancelShipment).
        Execute(input)
    // Ошибка → компенсации в обратном порядке
}
```

## Конфигурация

Приоритет: defaults → YAML → env. Разделитель уровней в env: `__` (одиночный `_` — часть ключа).

```go
config.NewLoader(
    config.WithFiles("config/config.yaml", "config/config.postgres.yaml"),
    config.WithEnvPrefix("ORDER_SERVICE"),
)
// ORDER_SERVICE__SERVER__ADDR=":9090"       → server.addr
// ORDER_SERVICE__POSTGRES__MAX_CONNS=50     → postgres.max_conns
// ORDER_SERVICE__OBSERVE__SERVICE_NAME=...  → observe.service_name
```

## Тестирование

### Unit-тесты (platformtest)

```go
func TestOrderHandler(t *testing.T) {
    srv, baseURL := platformtest.NewTestServer(t) // random port, auto cleanup
    brk := platformtest.NewTestBroker(t)           // in-memory, no Kafka

    svc := app.NewService(brk)
    srv.Mount("/api/v1/items", itemhandler.New(svc).Routes())

    resp, _ := http.Post(baseURL+"/api/v1/items", "application/json", body)
    assert.Equal(t, 201, resp.StatusCode)
}
```

### Интеграционные тесты (testcontainers)

Контейнеры поднимаются автоматически — docker-compose не нужен:

```go
//go:build integration

func TestTC_Postgres_WithTx(t *testing.T) {
    ctx := context.Background()
    ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine",
        tcpostgres.WithDatabase("testdb"),
        tcpostgres.WithUsername("test"),
        tcpostgres.WithPassword("test"),
        tcpostgres.BasicWaitStrategies(),
    )
    testcontainers.CleanupContainer(t, ctr)

    dsn, _ := ctr.ConnectionString(ctx, "sslmode=disable")
    db, _ := postgres.New(postgres.WithDSN(dsn))
    db.Start(ctx)
    // ... test with real PostgreSQL
}
```

## CLI

```bash
goplatform init myservice --postgres --kafka --connect --s3  # scaffold
goplatform run                                                 # hot reload
goplatform run --no-reload                                     # без hot reload
goplatform migrate up                                          # миграции
goplatform migrate down --steps 2                              # откат
goplatform migrate create add_users_table                      # создать файлы
```

## Деплой

```bash
# Локально
docker-compose up -d
go run ./cmd/...

# Kubernetes
helm install myservice deploy/helm/goplatform \
  --set image.repository=myregistry/myservice \
  --set postgres.enabled=true \
  --set postgres.dsn="postgres://..."
```

Подробнее: [deploy/README.md](deploy/README.md)

## Команды

```bash
make build              # go build ./...
make test               # go test -race ./...
make lint               # golangci-lint run ./...
make tidy               # go mod tidy
make integration        # интеграционные тесты (docker-compose)
make integration-tc     # testcontainers тесты (автоматический Docker)
make integration-pg     # только PostgreSQL
make integration-redis  # только Redis
make integration-s3     # только S3/MinIO
make integration-kafka  # только Kafka
make integration-consul # только Consul
make proto              # buf generate
```

## Структура проекта

```
goplatform/
├── cmd/goplatform/           # CLI: init, run, migrate
├── internal/scaffold/        # Шаблоны для goplatform init
├── pkg/
│   ├── platform/             # App, Component, Logger, Hooks, Plugin, Errors
│   ├── config/               # Multi-source config (koanf)
│   ├── observe/              # OTel traces + metrics + logs
│   ├── server/               # HTTP (chi) + ConnectRPC + middleware
│   ├── postgres/             # pgxpool, WithTx, migrations
│   ├── redis/                # Redis KV (go-redis)
│   ├── s3/                   # S3/MinIO (AWS SDK v2)
│   ├── broker/               # Publisher/Subscriber interfaces
│   │   ├── kafka/            # Kafka
│   │   ├── nats/             # NATS
│   │   └── membroker/        # In-memory
│   ├── discovery/            # Registry + Resolver
│   │   └── consul/           # Consul
│   ├── workflow/             # Temporal
│   │   └── saga/             # Saga builder
│   └── platformtest/         # Test helpers
├── examples/orderservice/    # Рабочий пример
├── integration/              # Интеграционные тесты (testcontainers)
├── deploy/                   # Helm + OTel/Prometheus/Tempo configs
├── proto/                    # Protobuf + ConnectRPC
├── docker-compose.yaml       # Полная инфра
└── Makefile
```

## Стек

| Категория | Технология |
|-----------|------------|
| HTTP | chi/v5 |
| RPC | ConnectRPC + protovalidate |
| Protobuf | Buf CLI |
| PostgreSQL | pgx/v5 (pgxpool) |
| S3 | AWS SDK v2 (MinIO compatible) |
| Kafka | segmentio/kafka-go |
| NATS | nats-io/nats.go (core + JetStream) |
| Redis | go-redis/v9 |
| Consul | hashicorp/consul/api |
| OTel | opentelemetry-go (traces + metrics + logs + otelslog) |
| Temporal | temporal-sdk-go + saga builder |
| Config | koanf/v2 |
| CLI | cobra |
| Migrations | golang-migrate/v4 |
| Tests | testify, goleak, miniredis, testcontainers-go |

## Лицензия

MIT
