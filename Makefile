.PHONY: build test run fmt docker-up docker-down smoke

BINARY  := bin/engine
PKG     := ./cmd/engine

build:
	go build -o $(BINARY) $(PKG)

test:
	go test -race ./...

run:
	go run $(PKG)

fmt:
	gofmt -w cmd internal

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

smoke:
	powershell -ExecutionPolicy Bypass -File scripts/smoke.ps1
