.DELETE_ON_ERROR:
SHELL := /bin/bash

# ROOT_DIR is the directory that holds this Makefile. It does not use git: in
# a source tarball, a ZIP download, or a checkout nested in another repo,
# `git rev-parse --show-toplevel` is empty or names the wrong tree, and BIN_DIR
# would become /bin.
ROOT_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
BIN      := perch
BIN_DIR  := $(ROOT_DIR)/bin
PKG      := ./...
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

# The build is hermetic and reproducible: it uses vendored dependencies and needs no network access.
export GOFLAGS := -mod=vendor
# Dev tools use pinned versions. The container image bakes these exact versions
# (see the image target). Never use a floating install.
GOLANGCI := v2.11.4
GOVULN   := v1.3.0

# Production GUI builds link webkit2gtk-4.1 (with libsoup-3.0) through the
# webkit2_41 build tag. webkit2gtk-4.0 has reached end of life and is not in
# the container base (noble). This tag is the only supported link for the
# host and the container.
TAGS := production webkit2_41

# ── Container framework ────────────────────────────────────────────────────
# All versions come from .tool-versions only. The image hardcodes none of them.
IMAGE        := perch-dev
GO_VERSION   := $(shell awk '$$1=="golang"{print $$2}' $(ROOT_DIR)/.tool-versions)
NODE_VERSION := $(shell awk '$$1=="nodejs"{print $$2}' $(ROOT_DIR)/.tool-versions)
# The build runs in a container by default. Set CONTAINERIZE=0 to run the
# target directly. Use this flag when you re-enter the image, or when a
# pipeline already runs in a container.
CONTAINERIZE ?= 1

# Host Go is needed only by targets that run on the host. The containerized
# targets (image, shell, and the DZ set below with CONTAINERIZE=1) need
# podman, not Go, so a contributor without Go can still run them.
HOST_GO_GOALS := build install run gui-build gui-install gui-run coverage fmt tidy vendor verify doctor cross
ifeq (, $(shell command -v go))
ifneq (,$(strip $(filter $(HOST_GO_GOALS),$(MAKECMDGOALS)) $(if $(MAKECMDGOALS),,default) $(filter 0,$(CONTAINERIZE))))
$(error 'go' not found on PATH)
endif
endif

.PHONY: build install run gui-build gui-install gui-run desktop image shell test test-integration test-front test-e2e test-all gui-check coverage lint fmt vet tidy vendor verify vulncheck verify-all doctor clean cross

# build: only the backend binary. It embeds the committed
# frontend/dist/index.html stub and does not rebuild the frontend. For a full
# production artifact, run `make gui-build`.
build:                ## Build the binary into ./bin. The build is vendored and reproducible. Add -tags '$(TAGS)' for the GUI build.
	@mkdir -p $(BIN_DIR)
	@go build -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch

install:              ## Install the binary to GOBIN or ~/go/bin.
	@go install -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' ./cmd/perch

run: build            ## Build, then run. This needs an X or Wayland display for the GUI.
	@$(BIN_DIR)/$(BIN)

gui-build:            ## Build the production GUI binary: the frontend build, then go build -tags '$(TAGS)'.
	npm --prefix frontend ci --prefer-offline
	npm --prefix frontend run build
	@mkdir -p $(BIN_DIR)
	@go build -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BIN) ./cmd/perch
	@# vite overwrote the tracked go:embed stub. The binary already embeds the
	@# real index, so restore the stub to keep the work tree clean (and VERSION
	@# free of a -dirty suffix on the next make).
	@git checkout -- frontend/dist/index.html 2>/dev/null || true

gui-install: gui-build desktop  ## Build the GUI binary and install the launcher icon. This does not launch the app; run $(BIN_DIR)/$(BIN) yourself.

gui-run: gui-build desktop  ## Build the GUI binary, install the desktop entry, then launch the GUI.
	@$(BIN_DIR)/$(BIN)

desktop:              ## Install a user .desktop entry and icon. GNOME on Wayland shows the app icon through this entry.
	@mkdir -p $(HOME)/.local/share/icons/hicolor/512x512/apps $(HOME)/.local/share/applications
	@cp app/appicon.png $(HOME)/.local/share/icons/hicolor/512x512/apps/perch.png
	@printf '[Desktop Entry]\nType=Application\nName=perch\nComment=Cockpit for AI coding agents\nExec=%s\nIcon=perch\nTerminal=false\nCategories=Development;\nStartupWMClass=perch\n' "$(abspath $(BIN_DIR)/$(BIN))" > $(HOME)/.local/share/applications/perch.desktop
	@# Bump the theme dir mtime and refresh only an existing icon cache, as
	@# xdg-icon-resource does. Creating a new user-level cache would hide icons
	@# other apps add later (GTK checks a cache against the dir mtime only).
	@touch $(HOME)/.local/share/icons/hicolor
	@if [ -f $(HOME)/.local/share/icons/hicolor/icon-theme.cache ] && command -v gtk-update-icon-cache >/dev/null 2>&1; then gtk-update-icon-cache -f -t $(HOME)/.local/share/icons/hicolor >/dev/null 2>&1 || true; fi
	@command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database $(HOME)/.local/share/applications >/dev/null 2>&1 || true
	@echo "==> installed perch.desktop (StartupWMClass=perch). Log out/in if the icon does not refresh."

image:                ## Build the pinned perch-dev image when it is absent. Versions come from .tool-versions and the Makefile. Run `podman rmi $(IMAGE):latest` to force a rebuild after a Containerfile change.
	@podman image exists $(IMAGE):latest || podman build \
	  --build-arg GO_VERSION=$(GO_VERSION) \
	  --build-arg NODE_VERSION=$(NODE_VERSION) \
	  --build-arg GOLANGCI_VERSION=$(GOLANGCI) \
	  --build-arg GOVULN_VERSION=$(GOVULN) \
	  -t $(IMAGE):latest -f containers/dev/Containerfile .

shell: | image        ## Open an interactive shell in perch-dev.
	@bash containers/run.sh dev bash

# Every check runs in perch-dev. One rule containerizes the whole set and
# avoids duplication. Re-entering with CONTAINERIZE=0 runs the target
# directly, inside the image. test-e2e is the only dispatched target that
# runs `vite build`, so it alone masks frontend/dist. The Go targets keep the
# committed go:embed stub.
DZ := test test-integration test-front test-e2e lint vet vulncheck gui-check
ifeq ($(CONTAINERIZE),1)
test-e2e: export PERCH_MASK_DIST = 1
$(DZ): | image
	@bash containers/run.sh dev make CONTAINERIZE=0 $@
else
test:             ; go test -race -count=1 $(PKG)
test-integration: ; go test -race -count=1 -tags=integration $(PKG)
test-front:       ; npm --prefix frontend ci --prefer-offline && npm --prefix frontend audit --omit=dev --audit-level=high && npm --prefix frontend run check && npm --prefix frontend test
test-e2e:         ; npm --prefix frontend ci --prefer-offline && npm --prefix frontend run test:e2e
lint:             ; golangci-lint run
vet:              ; go vet $(PKG)
vulncheck:        ; govulncheck ./...
# Compile-only check of the production GUI binary (cgo + WebKitGTK, the '$(TAGS)'
# build). The other Go gates build without these tags, so a production-only
# break (for example a vendored file dropped by .gitignore) reaches them never.
# This gate compiles ./cmd/perch exactly as the release does, and discards the
# output. It uses the committed frontend/dist go:embed stub, not a vite build.
gui-check:        ; go build -tags '$(TAGS)' -o /dev/null ./cmd/perch
endif

test-all: test test-integration test-front lint vet vulncheck test-e2e gui-check ## Run every gate. Each one runs in its own container with the right masks.
	@echo "==> test-all: ALL gates PASSED."

coverage:             ## Show a coverage report for the internal/ packages.
	@go test -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | tail -1

fmt:                  ## Run gofmt and goimports.
	@if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then \
		gofiles() { git ls-files -z '*.go' ':!:vendor/**'; }; \
	else \
		gofiles() { find . \( -name vendor -o -name node_modules -o \( -name '.*' ! -name . \) \) -prune -o -name '*.go' -print0; }; \
	fi; \
	gofiles | xargs -0 -r gofmt -w; \
	if command -v goimports >/dev/null; then gofiles | xargs -0 -r goimports -w; fi

tidy:                 ## Tidy go.mod and go.sum, then refresh the vendor tree.
	@GOFLAGS= go mod tidy && go mod vendor

vendor:               ## Refresh the committed /vendor tree.
	@GOFLAGS= go mod vendor

verify:               ## Verify that every module checksum matches go.sum.
	@GOFLAGS= go mod verify

verify-all: vet lint vulncheck test-front ## Run the quality gates: vet, lint, govulncheck, tsc, and the frontend unit tests. All run in perch-dev.
	@echo "==> All quality gates PASSED."

doctor: build         ## Run perch's own dependency check.
	@$(BIN_DIR)/$(BIN) doctor

cross:                ## Cross-compile for linux/amd64. The GUI uses cgo and WebKit, so this works only on Linux. Other OS/arch targets need their own toolchain.
	@mkdir -p $(BIN_DIR)
	@echo "building linux/amd64"
	@GOOS=linux GOARCH=amd64 go build -tags '$(TAGS)' -trimpath -ldflags '$(LDFLAGS)' \
	  -o $(BIN_DIR)/$(BIN)-linux-amd64 ./cmd/perch

clean:
	@test -n "$(BIN_DIR)" && test "$(BIN_DIR)" != "/bin" && rm -rf "$(BIN_DIR)"
