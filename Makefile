.PHONY: build test lint tidy clean integration integration-tc proto

build:
	go build ./...

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	go clean -cache -testcache

integration:
	go test -race -count=1 -tags=integration -timeout=5m ./integration/...

integration-tc:
	go test -race -count=1 -tags=integration -timeout=10m -v ./integration/... -run TestTC_

integration-pg:
	go test -race -count=1 -tags=integration -timeout=5m -v ./integration/... -run TestTC_Postgres

integration-redis:
	go test -race -count=1 -tags=integration -timeout=5m -v ./integration/... -run TestTC_Redis

integration-s3:
	go test -race -count=1 -tags=integration -timeout=5m -v ./integration/... -run TestTC_S3

integration-kafka:
	go test -race -count=1 -tags=integration -timeout=10m -v ./integration/... -run TestTC_Kafka

integration-consul:
	go test -race -count=1 -tags=integration -timeout=5m -v ./integration/... -run TestTC_Consul

proto:
	buf generate
