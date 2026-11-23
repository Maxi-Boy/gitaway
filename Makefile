# Рабочее название проекта: gitaway (модуль, бинарник и имена файлов легко заменить).
BIN     := bin/gitaway
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build build-pam test test-pam race vet fmt check run clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/gitaway

# Вход паролем учётной записи (auth_mode "pam"): нужны cgo и pam-devel (RED OS: dnf install pam-devel).
build-pam:
	go build -tags pam -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/gitaway

test:
	go test ./...

test-pam:
	go test -tags pam ./internal/auth/

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
