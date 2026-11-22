# Рабочее название проекта: gitaway (модуль, бинарник и имена файлов легко заменить).
BIN     := bin/gitaway
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test race vet fmt check run clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/gitaway

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l .)" || (echo "Нужен gofmt для:"; gofmt -l .; exit 1)

check: fmt vet test

run:
	go run ./cmd/gitaway

clean:
	rm -rf bin
