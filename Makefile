.DELETE_ON_ERROR:
SHELL := /bin/bash
.FORCE:

ROOT_DIR := $(shell git rev-parse --show-toplevel)
BIN      := perch
BIN_DIR  := $(ROOT_DIR)/bin
PKG      := ./...
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Hermetic, reproducible builds: vendored deps, no network at build time.
export GOFLAGS := -mod=vendor
# Pinned dev tools (run via `go run tool@version`, never a floating install).
GOLANGCI := v2.11.4
GOVULN   := v1.3.0

ifeq (, $(shell command -v go))
$(error 'go' not found on PATH)
endif

.PHONY: build install run test test-integration test-all coverage lint fmt vet tidy vendor verify vulncheck doctor clean cross

build:                ## build the binary into ./bin (vendored, reproducible)
	@mkdir -p $(BIN_DIR)
	@go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch

install:              ## install to GOBIN / ~/go/bin
	@go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/perch

run: build            ## build then run
	@$(BIN_DIR)/$(BIN)

test:                 ## unit tests
	@go test -race -count=1 $(PKG)

test-integration:     ## integration tests (requires tmux and git)
	@go test -race -count=1 -tags=integration $(PKG)

test-all:             ## unit + integration
	@go test -race -count=1 -tags=integration $(PKG)

coverage:             ## coverage report for internal/ packages
	@go test -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | tail -1

lint:                 ## golangci-lint at the pinned version
	@GOFLAGS= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI) run

fmt:                  ## gofmt + goimports
	@gofmt -w . && (command -v goimports >/dev/null && goimports -w . || true)

vet:
	@go vet $(PKG)

tidy:                 ## tidy go.mod/go.sum then refresh the vendor tree
	@GOFLAGS= go mod tidy && go mod vendor

vendor:               ## refresh the committed /vendor tree
	@GOFLAGS= go mod vendor

verify:               ## verify every module checksum matches go.sum
	@GOFLAGS= go mod verify

vulncheck:            ## scan deps for known CVEs (pinned govulncheck)
	@GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@$(GOVULN) ./...

doctor: build         ## run perch's own dependency check
	@$(BIN_DIR)/$(BIN) doctor

cross:                ## cross-compile all platforms into ./bin
	@mkdir -p $(BIN_DIR); for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  echo "building $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $(BIN_DIR)/$(BIN)-$$os-$$arch ./cmd/perch; \
	done

clean:
	@rm -rf $(BIN_DIR)
