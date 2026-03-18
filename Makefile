.PHONY: build test lint tidy clean integration proto

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
	go test -race -count=1 -tags=integration ./integration/...

proto:
	buf generate
