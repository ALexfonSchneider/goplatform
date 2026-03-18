# goplatform

Микросервисный SDK на Go. Управляемый lifecycle, observability из коробки, единый порт для REST и gRPC.

```go
baseHandler := slog.NewJSONHandler(os.Stdout, nil)
obs, _ := observe.New(
    observe.WithServiceName("orderservice"),
    observe.WithOTLPEndpoint("localhost:4317"),
    observe.WithBaseHandler(baseHandler),
)

logger := obs.Logger() // stdout + OTel, trace_id в каждой строке
app := platform.New(platform.WithLogger(logger))

db, _ := postgres.New(
    postgres.WithDSN(dsn),
    postgres.WithTracerProvider(obs.TracerProvider()),
)
_ = app.Register("postgres", db)

srv, _ := server.New(server.WithAddr(":8080"))
srv.Use(observe.HTTPMiddleware(obs.TracerProvider(), obs.MeterProvider()))
srv.Mount("/api/v1/items", itemhandler.New(svc).Routes())
_ = app.Register("server", srv)

app.Run(context.Background()) // SIGINT/SIGTERM → graceful shutdown
```

## Установка

```bash
go get github.com/ALexfonSchneider/goplatform
```

Требования: Go 1.22+

## Быстрый старт

```bash
# Установить CLI
go install github.com/ALexfonSchneider/goplatform/cmd/goplatform@latest

# Сгенерировать проект (Clean Architecture)
goplatform init myservice --postgres --kafka --redis --connect

# Запустить инфраструктуру
docker-compose up -d

# Запустить с hot reload
goplatform run
```

Генерируемая структура проекта:

```
myservice/
├── cmd/main.go
├── internal/
│   ├── domain/                    # entities + interfaces (порты)
│   │   ├── model.go
│   │   └── repository.go
│   ├── app/                       # use cases (бизнес-логика)
│   │   └── service.go
│   └── adapters/
│       ├── handlers/itemhandler/  # primary — HTTP
│       ├── postgresrepo/          # secondary — PostgreSQL
│       ├── kafkaconsumer/         # primary — Kafka
│       └── natsconsumer/          # primary — NATS
├── config/
│   ├── config.yaml
│   ├── config.postgres.yaml
│   └── config.kafka.yaml
├── proto/myservice/v1/            # ConnectRPC + protovalidate
├── migrations/
├── Makefile
└── Dockerfile
```

## Пакеты

### Ядро

| Пакет | Описание |
|-------|----------|
| [`pkg/platform`](pkg/platform/) | App, Component, Logger, Hooks, Plugin, Domain Errors |
| [`pkg/config`](pkg/config/) | Multi-source config: defaults → YAML → env (koanf) |
| [`pkg/observe`](pkg/observe/) | OTel traces + metrics + logs, без глобального стейта |

### Транспорт

| Пакет | Описание |
|-------|----------|
| [`pkg/server`](pkg/server/) | HTTP (chi) + ConnectRPC на одном порту |
| [`pkg/server` middleware](pkg/server/middleware.go) | Recovery, RequestLogging, Idempotency |
| [`pkg/server` ErrorInterceptor](pkg/server/errorinterceptor.go) | platform.Error → connect.Code маппинг |

### Данные

| Пакет | Описание |
|-------|----------|
| [`pkg/postgres`](pkg/postgres/) | pgxpool, WithTx, миграции, query hooks, OTel spans |
| [`pkg/redis`](pkg/redis/) | go-redis обёртка, JSON Set/Get, IdempotencyStore, Unwrap() |

### Messaging

| Пакет | Описание |
|-------|----------|
| [`pkg/broker`](pkg/broker/) | Publisher(Message)/PublishBatch, Subscriber, Middleware, PublishHook |
| [`pkg/broker/kafka`](pkg/broker/kafka/) | Kafka producer/consumer, DLQ, retry, W3C trace propagation |
| [`pkg/broker/nats`](pkg/broker/nats/) | NATS core + JetStream, DLQ, queue groups |
| [`pkg/broker/membroker`](pkg/broker/membroker/) | In-memory broker для unit-тестов |

### Инфраструктура

| Пакет | Описание |
|-------|----------|
| [`pkg/discovery`](pkg/discovery/) | Registry интерфейс, Resolver с round-robin и TTL кэшем |
| [`pkg/discovery/consul`](pkg/discovery/consul/) | Consul реализация с blocking query Watch |
| [`pkg/workflow`](pkg/workflow/) | Temporal обёртка с OTel tracing |
| [`pkg/workflow/saga`](pkg/workflow/saga/) | Saga step builder с компенсациями |

### Инструменты

| Пакет | Описание |
|-------|----------|
| [`pkg/platformtest`](pkg/platformtest/) | Test helpers: NewTestApp, NewTestServer, NewTestBroker, NewTestDB |
| [`cmd/goplatform`](cmd/goplatform/) | CLI: init, run (hot reload), migrate |

## Архитектурные принципы

### Explicit Lifecycle

Каждый компонент реализует `Component` — Start/Stop с контекстом:

```go
type Component interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

`App.Run()` стартует компоненты по порядку, ловит SIGINT/SIGTERM, останавливает в обратном порядке. Ошибки собираются через `errors.Join` — ни одна не теряется.

### No Global State

Никаких `otel.SetTracerProvider()`, `http.DefaultClient`, `log.SetDefault()`. Провайдеры создаются локально в `observe.New()` и передаются явно. `SetAsGlobal()` — опциональный, для тех кому надо.

### Interface-First

Пакеты зависят от интерфейсов, не реализаций:

```
broker.Publisher ← kafka.Producer, nats.Publisher, membroker.MemBroker
discovery.Registry ← consul.Registry
platform.Component ← server.Server, postgres.DB, redis.Client, ...
```

### Single Port

Один HTTP-сервер обслуживает всё: REST, ConnectRPC, health, metrics. ConnectRPC handler — стандартный `http.Handler`, монтируется через `srv.Mount()`.

### Domain Errors

```go
// Бизнес-логика возвращает доменную ошибку
return platform.WrapError(platform.CodeNotFound, "order not found", err)

// ErrorInterceptor автоматически маппит в connect.CodeNotFound
// Обычный Go error → connect.CodeInternal (детали скрыты от клиента)
```

### Fail Loud

Ошибка инициализации — error, не молчаливый passthrough. DLQ недоступна — offset не коммитится, данные не теряются.

## Observability

Полная pipeline: traces + metrics + logs через один OTLP endpoint.

```
Приложение
  │ traces, metrics, logs (OTLP gRPC)
  ▼
OTel Collector
  ├── traces  → Tempo
  ├── metrics → Prometheus
  └── logs    → Loki
        ▼
     Grafana (единый UI)
```

### Trace propagation

Сквозной trace_id через все компоненты:

```
ConnectRPC request (trace_id в HTTP headers)
  → handler span
    → postgres query span (pgx tracer)
    → kafka/nats publish (trace_id в message headers)
      → consumer (извлекает trace_id)
        → handler span → postgres query span
    → temporal workflow (OTel interceptor)
      → activity span
```

### Настройка

```go
baseHandler := slog.NewJSONHandler(os.Stdout, nil)
obs, _ := observe.New(
    observe.WithServiceName("orderservice"),
    observe.WithOTLPEndpoint("localhost:4317"),
    observe.WithBaseHandler(baseHandler),
)

// TracerProvider/MeterProvider доступны сразу после New() — не нужно ждать Start()
db, _ := postgres.New(postgres.WithTracerProvider(obs.TracerProvider()))

// Logger: stdout + OTel export, trace_id автоматически
logger := obs.Logger()
logger.InfoContext(ctx, "order created", "id", orderID)
// stdout: {"msg":"order created","id":"ord-123","trace_id":"4bf92f...","span_id":"00f067..."}
// OTel:   log record с trace correlation → Loki

// HTTP middleware: spans + request count/duration metrics
srv.Use(observe.HTTPMiddleware(obs.TracerProvider(), obs.MeterProvider()))

// Custom метрики — стандартный OTel API
counter, _ := obs.Meter("orders").Int64Counter("orders_created")
counter.Add(ctx, 1)
```

### Logger

```go
type Logger interface {
    Debug(msg string, args ...any)
    DebugContext(ctx context.Context, msg string, args ...any) // с trace_id
    Info(msg string, args ...any)
    InfoContext(ctx context.Context, msg string, args ...any)  // с trace_id
    Warn(msg string, args ...any)
    WarnContext(ctx context.Context, msg string, args ...any)
    Error(msg string, args ...any)
    ErrorContext(ctx context.Context, msg string, args ...any)
    With(args ...any) Logger
}
```

`*Context` методы извлекают trace_id/span_id из активного span и добавляют в лог. Без ctx — логируется без trace correlation (для startup, config).

## Broker

```go
// Publish с headers и batch
pub.Publish(ctx, broker.Message{
    Topic:   "orders",
    Key:     []byte("order-123"),
    Value:   payload,
    Headers: map[string]string{"content-type": "application/json"},
})

pub.PublishBatch(ctx, []broker.Message{msg1, msg2, msg3})

// Hook — мутирует любое поле перед отправкой
producer, _ := kafka.NewProducer(
    kafka.WithPublishHook(func(ctx context.Context, msg *broker.Message) error {
        msg.Value = encrypt(msg.Value)
        msg.Headers["encrypted"] = "true"
        return nil
    }),
)
```

## Saga (Temporal)

```go
func OrderSaga(ctx workflow.Context, input OrderInput) error {
    return saga.New(ctx).
        Step(ReserveStock, ReleaseStock).     // action, compensation
        Step(ChargePayment, RefundPayment).
        Step(CreateShipment, CancelShipment).
        Execute(input)
    // При ошибке: компенсации в обратном порядке автоматически
}

// С опциями:
saga.New(ctx,
    saga.WithParallelCompensation(),        // компенсации параллельно
    saga.WithContinueOnCompensationError(), // продолжать при ошибке компенсации
)
```

## Конфигурация

Приоритет: defaults → YAML файлы → переменные окружения.

```go
loader, _ := config.NewLoader(
    config.WithFiles("config/config.yaml", "config/config.postgres.yaml"),
    config.WithEnvPrefix("ORDER_SERVICE"),
)
// Env: ORDER_SERVICE__SERVER__ADDR=":9090"  → server.addr
// Env: ORDER_SERVICE__POSTGRES__MAX_CONNS=50 → postgres.max_conns
// Разделитель уровней: __ (двойной underscore)
// Одиночный _ — часть имени ключа

var cfg AppConfig
loader.Load(&cfg)
```

Каждый компонент — отдельный конфиг файл:

```
config/
├── config.yaml           # server, observe
├── config.postgres.yaml  # postgres
├── config.kafka.yaml     # kafka
├── config.nats.yaml      # nats
└── config.redis.yaml     # redis
```

## Hooks и Plugins

```go
// Hooks — вклиниться в lifecycle
app.OnBeforeStart(func(ctx context.Context, name string) error {
    log.Info("starting", "component", name)
    return nil  // return error → abort startup
})

// Plugins — модули, конфигурирующие платформу
app.Use(auth.NewPlugin(cfg))  // регистрирует middleware + routes + health check
```

Plugin получает `PluginContext` (не `*App`) — может регистрировать компоненты и hooks, но не может вызвать `Run()`.

## Тестирование

```go
func TestOrderHandler(t *testing.T) {
    // Сервер на случайном порту, автоматический cleanup
    srv, baseURL := platformtest.NewTestServer(t)

    // In-memory broker, без Kafka
    brk := platformtest.NewTestBroker(t)

    svc := app.NewService(brk)
    items := itemhandler.New(svc)
    srv.Mount("/api/v1/items", items.Routes())

    resp, _ := http.Post(baseURL+"/api/v1/items", "application/json", body)
    assert.Equal(t, 201, resp.StatusCode)
}
```

## CLI

```bash
goplatform init myservice --postgres --kafka --connect  # scaffold проекта
goplatform init myservice --nats --temporal --redis      # другой набор
goplatform run                                            # dev server с hot reload
goplatform run --no-reload                                # без hot reload
goplatform migrate up                                     # запуск миграций
goplatform migrate down                                   # откат миграций
goplatform migrate create add_users_table                 # создание файлов миграции
```

## Деплой

```bash
# Локально
docker-compose up -d    # PostgreSQL, Kafka, Redis, Consul, OTel, Grafana, Temporal
go run ./cmd/...

# Kubernetes
helm install myservice deploy/helm/goplatform \
  --set image.repository=myregistry/myservice \
  --set postgres.enabled=true \
  --set postgres.dsn="postgres://..."
```

Подробнее: [deploy/README.md](deploy/README.md)

## Структура проекта

```
goplatform/
├── cmd/goplatform/           # CLI (cobra): init, run, migrate
├── internal/scaffold/        # Шаблоны для goplatform init (embed.FS)
├── pkg/
│   ├── platform/             # Ядро: App, Component, Logger, Hooks, Plugin, Errors
│   ├── config/               # Multi-source config (koanf)
│   ├── observe/              # OTel traces + metrics + logs
│   ├── server/               # HTTP (chi) + ConnectRPC + middleware
│   ├── postgres/             # pgxpool, WithTx, migrations
│   ├── redis/                # Redis KV (go-redis)
│   ├── broker/               # Publisher/Subscriber interfaces
│   │   ├── kafka/            # Kafka implementation
│   │   ├── nats/             # NATS implementation
│   │   └── membroker/        # In-memory (тесты)
│   ├── discovery/            # Registry + Resolver
│   │   └── consul/           # Consul implementation
│   ├── workflow/             # Temporal wrapper
│   │   └── saga/             # Saga step builder
│   └── platformtest/         # Test helpers
├── examples/orderservice/    # Рабочий пример сервиса
├── integration/              # Интеграционные тесты
├── deploy/                   # Helm charts + OTel/Prometheus/Tempo configs
├── proto/                    # Protobuf + ConnectRPC codegen
├── docker-compose.yaml       # Полная инфра для разработки
└── Makefile                  # build, test, lint, proto
```

## Команды

```bash
make build          # go build ./...
make test           # go test -race -count=1 ./...
make lint           # golangci-lint run ./...
make tidy           # go mod tidy
make clean          # go clean -cache -testcache
make integration    # go test -race -count=1 -tags=integration ./integration/...
make proto          # buf generate
```

## Стек

| Категория | Технология |
|-----------|------------|
| HTTP | chi/v5 |
| RPC | ConnectRPC + protovalidate |
| Protobuf | Buf CLI |
| PostgreSQL | pgx/v5 (pgxpool) |
| Kafka | segmentio/kafka-go |
| NATS | nats-io/nats.go (core + JetStream) |
| Redis | go-redis/v9 |
| Consul | hashicorp/consul/api |
| OTel | opentelemetry-go (traces + metrics + logs) |
| Temporal | temporal-sdk-go + saga builder |
| Config | koanf/v2 |
| CLI | cobra |
| Migrations | golang-migrate/v4 |
| Tests | testify, goleak, miniredis |

## Лицензия

MIT
