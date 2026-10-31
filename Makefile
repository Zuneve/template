-include .env
export

BIN := bin/trip-service

.PHONY: build run test lint migrate generate

build:
	go build -o $(BIN) ./cmd/trip-service

run:
	go run ./cmd/trip-service

test:
	go test -race ./...

lint:
	golangci-lint run ./...

generate:
	go tool oapi-codegen \
		-generate types,chi-server \
		-package api \
		-o internal/generated/api.gen.go \
		contracts/openapi/trip-service.openapi.yaml

migrate:
	go tool goose -dir migrations postgres "$(DATABASE_URL)" up
