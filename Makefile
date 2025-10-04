BIN_DIR=bin
HYPRPAL_BIN=$(BIN_DIR)/hyprpal
HSCTL_BIN=$(BIN_DIR)/hsctl
INSTALL_DIR?=$(HOME)/.local/bin

.PHONY: build run tui install service lint test

build:
	mkdir -p $(BIN_DIR)
	go build -o $(HYPRPAL_BIN) ./cmd/hyprpal
	go build -o $(HSCTL_BIN) ./cmd/hsctl

run:
        go run ./cmd/hyprpal --config configs/example.yaml

tui:
        go run ./cmd/hsctl tui

install:
        mkdir -p $(INSTALL_DIR)
        GOBIN=$(INSTALL_DIR) go install ./cmd/hyprpal ./cmd/hsctl

service:
	systemctl --user daemon-reload
	systemctl --user enable --now hyprpal.service

lint:
	go vet ./...
	test -z "$(shell gofmt -l .)" || (echo 'Run gofmt on listed files' && exit 1)

test:
	go test ./...
