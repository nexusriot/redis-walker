BINARY   := redis-walker
PKG      := github.com/nexusriot/redis-walker
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X $(PKG)/pkg/view.Version=$(VERSION)
COMPOSE  := docker compose -f test/e2e/docker-compose.yml

.PHONY: all build install test race cover lint fmt vet e2e e2e-down clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o build/$(BINARY) ./cmd/redis-walker

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/redis-walker

## test runs the unit tests; they need no Redis server (miniredis is embedded).
test:
	go test ./...

race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -l -w cmd pkg test

vet:
	go vet ./...
	go vet -tags=e2e ./...

## e2e runs the hermetic suite: throw-away Redis servers in Docker, the whole
## TUI driven through a simulated terminal.
e2e:
	$(COMPOSE) up --build --abort-on-container-exit --exit-code-from tests
	$(COMPOSE) down -v

e2e-down:
	$(COMPOSE) down -v

clean:
	rm -rf build coverage.out
