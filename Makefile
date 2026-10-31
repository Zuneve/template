-include .env
export

BIN := bin/trip-service
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build run test test-integration lint mutate migrate generate

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/trip-service

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/trip-service

test:
	go test -race -coverprofile=coverage.out ./...
	@echo "total coverage:"
	@go tool cover -func=coverage.out | tail -1
	@grep -E '^mode|internal/(usecase|domain)/' coverage.out > coverage.core.out
	@echo "usecase + domain coverage:"
	@go tool cover -func=coverage.core.out | tail -1

test-integration:
	go test -race -tags integration -count=1 ./...

lint:
	golangci-lint run ./...

generate:
	go tool oapi-codegen \
		-generate types,chi-server \
		-package api \
		-o internal/generated/api.gen.go \
		contracts/openapi/trip-service.openapi.yaml
	go generate ./...

mutate:
	go run github.com/go-gremlins/gremlins/cmd/gremlins@latest unleash ./internal/domain --timeout-coefficient 20
	go run github.com/go-gremlins/gremlins/cmd/gremlins@latest unleash ./internal/usecase --timeout-coefficient 20

migrate:
	go tool goose -dir migrations postgres "$(DATABASE_URL)" up
