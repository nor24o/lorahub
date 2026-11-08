VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist

.PHONY: all build arm64 test run sim clean

all: test build arm64

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/lorahub ./cmd/lorahub
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/lorahub-sim ./cmd/lorahub-sim

# Static binary for the Bobcat Miner 300 (RK3566, aarch64)
arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/lorahub-linux-arm64 ./cmd/lorahub
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/lorahub-sim-linux-arm64 ./cmd/lorahub-sim

test:
	go vet ./...
	go test -race ./...

# Local development instance on http://localhost:8080
run: build
	$(DIST)/lorahub -config deploy/dev.yml

sim: build
	$(DIST)/lorahub-sim -server 127.0.0.1:1700

clean:
	rm -rf $(DIST)
