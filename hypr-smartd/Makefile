BINARY=bin/hypr-smartd

.PHONY: build run install service lint test

build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/hypr-smartd

run:
	go run ./cmd/hypr-smartd --config configs/example.yaml

install:
	go install ./cmd/hypr-smartd

service:
	systemctl --user daemon-reload
	systemctl --user enable --now hypr-smartd.service

lint:
	go vet ./...
	test -z "$(shell gofmt -l .)" || (echo 'Run gofmt on listed files' && exit 1)

test:
	go test ./...
