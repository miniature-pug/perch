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
# so this is the single supported link for host and container alike.
TAGS := production webkit2_41

# ── Container framework ────────────────────────────────────────────────────
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

.PHONY: build install run gui-build gui-install gui-run desktop image shell test test-integration test-front test-e2e test-all coverage lint fmt vet tidy vendor verify vulncheck verify-all doctor clean cross

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

gui-install: gui-build desktop  ## build the GUI binary + install the launcher icon (no launch; run $(BIN_DIR)/$(BIN) yourself)

gui-run: gui-build desktop  ## build, install the desktop entry, then launch the GUI
	@$(BIN_DIR)/$(BIN)

desktop:              ## install a user .desktop entry + icon (GNOME/Wayland shows the app icon via this)
	@mkdir -p $(HOME)/.local/share/icons/hicolor/512x512/apps $(HOME)/.local/share/applications
	@cp app/appicon.png $(HOME)/.local/share/icons/hicolor/512x512/apps/perch.png
	@printf '[Desktop Entry]\nType=Application\nName=perch\nComment=Cockpit for AI coding agents\nExec=%s\nIcon=perch\nTerminal=false\nCategories=Development;\nStartupWMClass=perch\n' "$(abspath $(BIN_DIR)/$(BIN))" > $(HOME)/.local/share/applications/perch.desktop
	@command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -f -t $(HOME)/.local/share/icons/hicolor >/dev/null 2>&1 || true
	@command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database $(HOME)/.local/share/applications >/dev/null 2>&1 || true
	@echo "==> installed perch.desktop (StartupWMClass=perch). Log out/in if the icon does not refresh."

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
# masks frontend/dist. Go targets keep the committed go:embed stub.
DZ := test test-integration test-front test-e2e lint vet vulncheck
ifeq ($(CONTAINERIZE),1)
gui-build: export PERCH_MASK_DIST = 1
test-e2e: export PERCH_MASK_DIST = 1
$(DZ): | image
	@bash containers/run.sh dev make CONTAINERIZE=0 $@
else
test:             ; go test -race -count=1 $(PKG)
test-integration: ; go test -race -count=1 -tags=integration $(PKG)
test-front:       ; npm --prefix frontend ci && npm --prefix frontend audit --omit=dev --audit-level=high && npm --prefix frontend run check && npm --prefix frontend test
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
