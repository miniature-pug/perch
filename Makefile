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

# Production GUI builds link webkit2gtk-4.1 (+libsoup-3.0) via the webkit2_41
# build tag. webkit2gtk-4.0 is EOL and absent from the container base (noble),
# so this is the single supported link for host and container alike (spec §5.1).
TAGS := production webkit2_41

# ── Container framework (spec 2026-06-04) ──────────────────────────────────
# Versions are single-sourced from .tool-versions; the image hardcodes none.
IMAGE        := perch-dev
GO_VERSION   := $(shell awk '$$1=="golang"{print $$2}' .tool-versions)
NODE_VERSION := $(shell awk '$$1=="nodejs"{print $$2}' .tool-versions)
# Container-first by default. CONTAINERIZE=0 runs the target natively — set when
# re-entering the image, or in a pipeline that is already containerised.
CONTAINERIZE ?= 1

ifeq (, $(shell command -v go))
$(error 'go' not found on PATH)
endif

.PHONY: build install run gui-build gui-run image shell test test-integration test-front test-e2e test-all coverage lint fmt vet tidy vendor verify vulncheck verify-all doctor clean cross

# build: backend binary only — embeds the committed frontend/dist/index.html stub
# (no frontend rebuild). For a full production artifact, use `make gui-build`.
build:                ## build the binary into ./bin (vendored, reproducible); -tags '$(TAGS)' for GUI
	@mkdir -p $(BIN_DIR)
	@go build -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch

install:              ## install to GOBIN / ~/go/bin
	@go install -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' ./cmd/perch

run: build            ## build then run (needs an X/Wayland display for the GUI)
	@$(BIN_DIR)/$(BIN)

gui-build:            ## build the production GUI binary (frontend build + go build -tags '$(TAGS)')
	npm --prefix frontend ci
	npm --prefix frontend run build
	@mkdir -p $(BIN_DIR)
	@go build -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch

gui-run: gui-build    ## build then launch the GUI (needs an X/Wayland display)
	@$(BIN_DIR)/$(BIN)

image:                ## build the pinned perch-dev image (versions from .tool-versions / Makefile)
	@podman build \
	  --build-arg GO_VERSION=$(GO_VERSION) \
	  --build-arg NODE_VERSION=$(NODE_VERSION) \
	  --build-arg GOLANGCI_VERSION=$(GOLANGCI) \
	  --build-arg GOVULN_VERSION=$(GOVULN) \
	  -t $(IMAGE):latest -f containers/dev/Containerfile .

shell: | image        ## drop into an interactive shell in perch-dev
	@bash containers/run.sh dev bash

# Every check runs in perch-dev (container-first). One DRY rule containerises the
# whole set; re-entering with CONTAINERIZE=0 runs the target natively in-image.
# test-e2e is the only dispatched target that runs `vite build`, so it alone
# masks frontend/dist (spec §5.3) — Go targets keep the committed go:embed stub.
DZ := test test-integration test-front test-e2e lint vet vulncheck
ifeq ($(CONTAINERIZE),1)
gui-build: export PERCH_MASK_DIST = 1
test-e2e: export PERCH_MASK_DIST = 1
$(DZ): | image
	@bash containers/run.sh dev make CONTAINERIZE=0 $@
else
test:             ; go test -race -count=1 $(PKG)
test-integration: ; go test -race -count=1 -tags=integration $(PKG)
test-front:       ; npm --prefix frontend ci && npm --prefix frontend run check && npm --prefix frontend test
test-e2e:         ; npm --prefix frontend ci && npm --prefix frontend run test:e2e
lint:             ; golangci-lint run
vet:              ; go vet $(PKG)
vulncheck:        ; govulncheck ./...
endif

test-all: test test-integration test-front lint vet vulncheck test-e2e ## the everything-gate (each runs in its own container with the right masks)
	@echo "==> test-all: ALL gates PASSED."

coverage:             ## coverage report for internal/ packages
	@go test -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | tail -1

fmt:                  ## gofmt + goimports
	@gofmt -w . && (command -v goimports >/dev/null && goimports -w . || true)

tidy:                 ## tidy go.mod/go.sum then refresh the vendor tree
	@GOFLAGS= go mod tidy && go mod vendor

vendor:               ## refresh the committed /vendor tree
	@GOFLAGS= go mod vendor

verify:               ## verify every module checksum matches go.sum
	@GOFLAGS= go mod verify

verify-all: vet lint vulncheck test-front ## quality gates: vet + lint + govulncheck + tsc + frontend unit (all in perch-dev)
	@echo "==> All quality gates PASSED."

doctor: build         ## run perch's own dependency check
	@$(BIN_DIR)/$(BIN) doctor

cross:                ## cross-compile for linux/amd64 (GUI uses cgo+WebKit; Linux-only; per-target toolchain needed for other OS/arch)
	@mkdir -p $(BIN_DIR)
	@echo "building linux/amd64"
	@GOOS=linux GOARCH=amd64 go build -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' \
	  -o $(BIN_DIR)/$(BIN)-linux-amd64 ./cmd/perch

clean:
	@rm -rf $(BIN_DIR)
