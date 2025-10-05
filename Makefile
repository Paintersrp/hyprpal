BIN_DIR=bin
HYPRPAL_BIN=$(BIN_DIR)/hyprpal
HSCTL_BIN=$(BIN_DIR)/hsctl
SMOKE_BIN=$(BIN_DIR)/smoke
BENCH_BIN=$(BIN_DIR)/bench
INSTALL_DIR?=$(HOME)/.local/bin

.PHONY: build run tui smoke bench install service lint test

build:
        mkdir -p $(BIN_DIR)
        go build -o $(HYPRPAL_BIN) ./cmd/hyprpal
        go build -o $(HSCTL_BIN) ./cmd/hsctl
        go build -o $(SMOKE_BIN) ./cmd/smoke
        go build -o $(BENCH_BIN) ./cmd/bench

run:
        go run ./cmd/hyprpal --config configs/example.yaml

tui:
        go run ./cmd/hsctl tui

smoke:
        go run ./cmd/smoke --config configs/example.yaml

bench:
        if [ "$(PROFILE)" = "1" ]; then \
                mkdir -p docs/flamegraphs; \
                go run ./cmd/bench --config configs/example.yaml --fixture fixtures/coding.json --iterations 25 --cpu-profile docs/flamegraphs/v0.5-bench-cpu.pb.gz --mem-profile docs/flamegraphs/v0.5-bench-heap.pb.gz; \
        else \
                go run ./cmd/bench --config configs/example.yaml --fixture fixtures/coding.json --iterations 25; \
        fi

install:
        mkdir -p $(INSTALL_DIR)
        GOBIN=$(INSTALL_DIR) go install ./cmd/hyprpal ./cmd/hsctl ./cmd/smoke ./cmd/bench

service:
	systemctl --user daemon-reload
	systemctl --user enable --now hyprpal.service

lint:
	go vet ./...
	test -z "$(shell gofmt -l .)" || (echo 'Run gofmt on listed files' && exit 1)

test:
	go test ./...
