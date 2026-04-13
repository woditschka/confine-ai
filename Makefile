.PHONY: all fmt vet lint lint-fix test test-race test-e2e test-samples test-all build install uninstall clean tidy ci security deps-check update-samples help hooks podman-build podman-push podman podman-clean

BINARY := confine-ai
BIN_DIR := bin
PREFIX ?= /usr/local

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

# golangci-lint (binary install, recommended by upstream)
GOLANGCI_LINT_VERSION ?= v2.11.4
GOLANGCI_LINT := $(shell go env GOPATH)/bin/golangci-lint

# Container settings
IMAGE ?= confine-ai
TAG ?= latest

all: fmt vet test build

fmt:
	go fmt ./...

vet:
	go vet ./...

$(GOLANGCI_LINT):
	mkdir -p $(shell go env GOPATH)/bin
	# Strip 'install -d' from upstream script — not available on all platforms.
	curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh | sed -e '/install -d/d' | sh -s -- -b $(shell go env GOPATH)/bin $(GOLANGCI_LINT_VERSION)

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

lint-fix: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --fix

test:
	go test -v ./...

test-race:
	CGO_ENABLED=1 go test -race -v ./...

test-e2e:
	go test -tags integration -v -count=1 ./e2e/

test-samples:
	go test -tags "integration samples" -v -count=1 -timeout 30m ./e2e/

test-all: test test-e2e test-samples

test-coverage:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) .

install: build
	install -d $(PREFIX)/bin
	install -m 755 $(BIN_DIR)/$(BINARY) $(PREFIX)/bin/$(BINARY)

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY)

clean:
	rm -rf $(BIN_DIR) coverage.out
	go clean

tidy:
	go mod tidy

ci: tidy fmt vet lint deps-check test build

security:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	go mod verify

# Prohibited modules from docs/system-design.md#dependency-policy
PROHIBITED_MODULES := github.com/stretchr/testify github.com/onsi/gomega \
	go.uber.org/zap github.com/sirupsen/logrus \
	github.com/gin-gonic/gin github.com/go-chi/chi github.com/gorilla/mux \
	github.com/google/wire go.uber.org/dig \
	github.com/golang/mock github.com/vektra/mockery \
	github.com/prometheus/client_golang github.com/prometheus/common \
	k8s.io/client-go k8s.io/api k8s.io/apimachinery sigs.k8s.io/controller-runtime

deps-check:
	@VIOLATIONS=""; \
	DEPS=$$(go list -m all 2>/dev/null | awk '{print $$1}'); \
	for mod in $(PROHIBITED_MODULES); do \
		if echo "$$DEPS" | grep -q "^$$mod$$"; then \
			VIOLATIONS="$$VIOLATIONS  - $$mod\n"; \
		fi; \
	done; \
	if [ -n "$$VIOLATIONS" ]; then \
		echo "ERROR: prohibited dependencies found:"; \
		printf "$$VIOLATIONS"; \
		echo "See docs/system-design.md#dependency-policy"; \
		exit 1; \
	fi; \
	echo "deps-check: all dependencies comply with policy"

hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks installed from .githooks/"

# Container targets
podman-build:
	podman build -t $(IMAGE):$(TAG) -f deploy/Dockerfile .

podman-push:
	podman push $(IMAGE):$(TAG)

podman: podman-build podman-push

podman-clean:
	podman rmi $(IMAGE):$(TAG) 2>/dev/null || true

update-samples: ## Update pinned versions in samples/base/Dockerfile
	go run ./cmd/update-samples

help:
	@echo "Setup:   hooks"
	@echo "Build:   all fmt vet lint lint-fix test test-race test-e2e test-samples test-all test-coverage build install uninstall clean tidy ci security deps-check"
	@echo "Dev:     update-samples"
	@echo "Podman:  podman-build podman-push podman podman-clean"
	@echo ""
	@echo "Note: test-race requires CGO_ENABLED=1 and a C compiler (gcc)"
