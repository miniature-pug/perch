.DELETE_ON_ERROR:
SHELL := /bin/bash
.FORCE:

ROOT_DIR := $(shell git rev-parse --show-toplevel)
BIN      := perch
BIN_DIR  := $(ROOT_DIR)/bin
PKG      := ./...
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

# Hermetic, reproducible builds: vendored deps, no network at build time.
export GOFLAGS := -mod=vendor
# Pinned dev tools (run via `go run tool@version`, never a floating install).
GOLANGCI := v2.11.4
GOVULN   := v1.3.0

ifeq (, $(shell command -v go))
$(error 'go' not found on PATH)
endif

.PHONY: build install run gui-build gui-run test test-integration test-all coverage lint fmt vet tidy vendor verify vulncheck verify-all doctor clean cross

build:                ## build the binary into ./bin (vendored, reproducible); -tags production required for GUI
	@mkdir -p $(BIN_DIR)
	@go build -tags production -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch

install:              ## install to GOBIN / ~/go/bin
	@go install -tags production -trimpath -ldflags '$(LDFLAGS)' ./cmd/perch

run: build            ## build then run (needs an X/Wayland display for the GUI)
	@$(BIN_DIR)/$(BIN)

gui-build:            ## build the production GUI binary (frontend build + go build -tags production)
	npm --prefix frontend install
	npm --prefix frontend run build
	@mkdir -p $(BIN_DIR)
	@go build -tags production -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch

gui-run: gui-build    ## build then launch the GUI (needs an X/Wayland display)
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

verify-all: vet lint vulncheck ## quality gates: vet + lint + govulncheck + tsc
	npm --prefix frontend run check
	@echo "==> All quality gates PASSED."

doctor: build         ## run perch's own dependency check
	@$(BIN_DIR)/$(BIN) doctor

cross:                ## cross-compile for linux/amd64 (GUI uses cgo+WebKit; Linux-only; per-target toolchain needed for other OS/arch)
	@mkdir -p $(BIN_DIR)
	@echo "building linux/amd64"
	@GOOS=linux GOARCH=amd64 go build -tags production -trimpath -ldflags '$(LDFLAGS)' \
	  -o $(BIN_DIR)/$(BIN)-linux-amd64 ./cmd/perch

clean:
	@rm -rf $(BIN_DIR)
