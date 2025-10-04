BINARY=bin/hyprpal
INSTALL_DIR?=$(HOME)/.local/bin

.PHONY: build run install service lint test

build:
        mkdir -p bin
        go build -o $(BINARY) ./cmd/hyprpal

run:
        go run ./cmd/hyprpal --config configs/example.yaml

install:
        mkdir -p $(INSTALL_DIR)
        GOBIN=$(INSTALL_DIR) go install ./cmd/hyprpal

service:
        systemctl --user daemon-reload
        systemctl --user enable --now hyprpal.service

lint:
	go vet ./...
	test -z "$(shell gofmt -l .)" || (echo 'Run gofmt on listed files' && exit 1)

test:
	go test ./...
