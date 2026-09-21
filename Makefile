# Dorylinae (AgentNet) build. Requires Go and golangci-lint on PATH.
BINS    := agentnet agentnetd relay
BIN_DIR := bin
# Override for releases: make build VERSION=1.2.3
VERSION ?= 0.0.0-dev
LDFLAGS := -X github.com/Magazem/Dorylinae-Agentnet/internal/version.Version=$(VERSION)

.PHONY: build test lint vet verify-vectors

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ $(addprefix ./cmd/,$(BINS))

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# Independent recomputation of the pairing and mail test vectors (Docs/protocol).
verify-vectors:
	go run ./tools/verifyvectors
