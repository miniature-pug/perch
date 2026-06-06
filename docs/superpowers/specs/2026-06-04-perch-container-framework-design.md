# perch — Container & Test Framework Design

**Status:** Approved — built and verified 2026-06-04. The `perch-dev` image builds from `ubuntu:24.04` (3.43 GB; baked toolchain matches every single source: Go 1.26.4, Node 22.22.3, golangci-lint 2.11.4, govulncheck v1.3.0, webkit2gtk-4.1 = 2.52.3, chromium baked). All three distinct target classes run green in it with the host tree byte-clean after each: `make test` (Go compile + `//go:embed`, dist mask **off** — 16 pkgs ok, embed resolved), `make test-e2e` (Playwright chromium on the from-scratch noble base — **61 passed**, dist stub byte-identical), and in-container `gui-build` (cgo + WebKit2GTK-4.1 link — 15.6 MB ELF). The production binary links `libwebkit2gtk-4.1.so.0` + `libsoup-3.0.so.0` **both in the container and on the host** (identical link), confirming the `webkit2_41` standardization (§5.1).
**Date:** 2026-06-04
**Relates to:** `2026-06-02-perch-cockpit-design.md` (the GUI spec). This document is its sibling: the cockpit spec defines *what perch is and how it is tested* (§10 Testing Strategy); this spec defines *the environment those tests run in*. The two are intended to be **reviewed together** — GUI behaviour and the harness that verifies it. There is **no released version and no backward-compatibility requirement**.

---

## 1. Vision

perch treats **containers as first-class**, not as a CI afterthought. There is **one image — the perch dev/test environment** — and every check (Go, frontend, e2e, lint, vuln, build) runs **inside it**. The same image is what a developer drops into, what `make test` uses, and what a pipeline will eventually pull. Identical bits everywhere → identical results everywhere.

**Identity in one line:** one pinned environment that every developer and every pipeline shares — *not* a pile of host tools each person installs differently.

**The differentiator:** the host distro (Ubuntu 26.04) is **not supported by Playwright 1.60.0** (`Playwright does not support chromium on ubuntu26.04-x64`). Rather than patch the host with an unofficial platform override + `sudo apt` of `t64` libraries — a host mutation that helps no one else and drifts — perch makes the *whole* test surface containerised. The blocker becomes the forcing function for a better, reproducible framework.

### Principles (non-negotiable)
- **Containers first-class.** All testing goes through containers and runs in containers. Consistency by construction; trivially plugs into pipelines later.
- **One image = the environment.** The union of every check's needs is just "the perch dev environment." We don't split it into a "test image" and a "dev image."
- **Single source of truth for every version.** Nothing is re-pinned or inherited-and-hoped: Go/Node from `.tool-versions`, npm deps + Playwright browser from the lockfile, go-tools from the `Makefile`. No magic numbers.
- **Host stays clean.** A container run never mutates the host tree — tracked build artifacts (`frontend/dist`) and host-native `node_modules` are masked.
- **Pipeline-ready by construction.** The same `make` targets run locally (spin up the image) and inside a pipeline (already in the image) via one toggle. No second test entrypoint.
- **Honest about the platform.** Where the host can't run a check natively, we say so and run it in the image; where full automation isn't reachable yet (the WebKit GUI smoke), we keep the manual checklist and say so.

---

## 2. Non-Goals

- **No CI/CD pipeline files (yet).** We design *for* pipelines (pushable image, `CONTAINERIZE=0` native path) but add no `.github/workflows`. "No CI/CD pipeline integration" is an explicit cockpit non-goal; this framework makes adding one a one-file step, later, on request.
- **Not shipping perch as a container.** perch is a Linux **desktop GUI** app; the image is a build/test environment, not a runtime artifact. No published runtime image.
- **No multi-arch images (yet).** The image targets `linux/amd64` (matches `make cross`). arm64 is a later sibling concern, not designed away.
- **No compose-tooling dependency.** `podman compose`/`podman-compose` are **absent** on the host and would themselves be a new install. The framework uses plain `podman run` (podman 5.7.0 is present); a compose file is not required and is not added.
- **The image does not replace the manual WebKit smoke.** The GUI + real-agent + D-Bus single-instance smoke (cockpit §10, smoke-checklist) stays human-gated for now (see §8, §12). The image is built to host it later.

---

## 3. Platform & Build (verified facts)

- **Host:** Ubuntu **26.04** (Resolute), kernel 7.0.0-22, x86_64. **Playwright 1.60.0 refuses chromium install/launch** here (`ERROR: Playwright does not support chromium on ubuntu26.04-x64`; `install-deps`: "your OS is not officially supported"). Supported Playwright targets are Debian 12/13, Ubuntu 22.04/24.04, Windows/WSL, macOS 14+.
- **Container runtime:** **podman 5.7.0**, rootless. Container-root maps to the host UID, so bind-mounted artifacts come out host-owned (no `--userns` gymnastics needed). **No compose tooling** present.
- **Base image:** `ubuntu:24.04` (Noble) — a Playwright-supported distro. Chosen over `mcr.microsoft.com/playwright:v1.60.0-noble` so **every** version is single-sourced (the playwright base inherits an unpinned Node); browsers + their system libs come from `npx playwright install --with-deps`, driven by the lockfile pin. **Verified 2026-06-04:** the from-scratch `ubuntu:24.04` build — Go install, Node install, **cgo + WebKit2GTK-4.1 linking**, and `playwright install --with-deps chromium` — runs both e2e (61 passed) and `gui-build` (15.6 MB ELF) green. The `mcr` noble image remains a possible fallback but is **not needed**: the single-sourced from-scratch base is the one shipped.
- **Version sources (single, authoritative):**
  - Go `1.26.4`, Node `22.22.3` ← `.tool-versions` (also `claude`/`opencode` pins live here; unused by the image).
  - `golangci-lint v2.11.4`, `govulncheck v1.3.0` ← `Makefile` (`GOLANGCI` / `GOVULN`).
  - npm deps + Playwright browser (`@playwright/test 1.60.0` → Chromium) ← `frontend/package-lock.json`.
  - The image passes these as `--build-arg`s; the `Containerfile` hardcodes none of them.
- **Reproducibility carried over from the Makefile ethos:** vendored Go deps (`GOFLAGS=-mod=vendor`), pinned tools fetched by exact version (never a floating install), hermetic builds. The image extends this ethos to the frontend/e2e surface.
- **Proof of concept (verified 2026-06-04):** a `podman run` of the **`mcr` noble image** over the bind-mounted repo ran the Playwright suite **61 passed (52.6s)** with `frontend/dist/index.html` byte-unchanged and `git status` showing only intended edits. This validates the bind-mount + dist-mask model **for the e2e path on the `mcr` base only** — the e2e target runs `vite build`, which repopulates the masked `dist` volume before Playwright reads it. It does **not** cover the `ubuntu:24.04` base, nor the Go-compile targets, where the dist mask must be **off** or `//go:embed` breaks (§5.3).

---

## 4. What Needs a Container (enumerate first, then the pattern)

Every check, so the environment is identical for everyone and pipeline-ready:

| Check | Environment it needs |
| --- | --- |
| `go test` unit / integration / `vet` | Go toolchain + git |
| `golangci-lint`, `govulncheck` | Go toolchain (pinned tools) |
| frontend unit (`vitest`) + typecheck (`tsc`) | Node |
| e2e (`playwright`, chromium) | Node + browsers + system libs |
| `gui-build` / build verification | Go + cgo + GTK3 + WebKit2GTK |
| *(future)* headless WebKit GUI smoke | all of the above + `xvfb` + mocked agents |

The **union of these is the perch dev environment.** That union — not a per-check image — is what the framework builds (§5).

---

## 5. Image Architecture

### 5.1 The `perch-dev` image (`containers/dev/Containerfile`)
`FROM ubuntu:24.04`, then, in cache-friendly layers:
- **System + GUI libs:** `ca-certificates curl git xz-utils build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev xvfb` (the GTK/WebKit/`xvfb` set lets the same image cover `gui-build` and the future GUI smoke). `libwebkit2gtk-4.1-dev` pulls `libsoup-3.0-dev` transitively.
  - **WebKit version decision (forced by the base, 2026-06-04).** Noble ships **only** `libwebkit2gtk-4.1-dev` — the `-4.0-dev` package was dropped. Wails links `webkit2gtk-4.0` (+`libsoup-2.4`) by default and `webkit2gtk-4.1` (+`libsoup-3.0`) only under the `webkit2_41` Go build tag. So the project standardizes every production build on **`-tags "production webkit2_41"`** (`Makefile`: `build`/`install`/`gui-build`/`cross`). This keeps the GUI link **identical on host and in the container** (the host, Ubuntu 26.04, ships native `libwebkit2gtk-4.1-dev 2.52.3` too) — Path B (a container-only tag) would reintroduce the host/container divergence this framework exists to eliminate. webkit2gtk-4.0 is EOL; there is no backward-compat requirement. Non-production targets (`go test`, `vet`) compile the non-production stub and link **no** WebKit, so they are unaffected.
- **Go** (`ARG GO_VERSION`) — official tarball into `/usr/local/go`; `PATH`/`GOPATH` exported.
- **Node** (`ARG NODE_VERSION`) — official tarball into `/usr/local`.
- **Pinned go-tools** (`ARG GOLANGCI_VERSION`, `ARG GOVULN_VERSION`) — `go install`ed into the image so `lint`/`vulncheck` run an offline prebaked binary, not `go run …@version` every invocation.
- **Playwright browsers** — `COPY frontend/package.json frontend/package-lock.json`, `npm ci`, `npx playwright install --with-deps chromium`; the browser version follows the lockfile pin and the binaries are baked into the image cache (`/root/.cache/ms-playwright`), so e2e never re-downloads at run time. The temp `node_modules` is discarded (runtime re-installs into a masked volume — §5.3).

The image is **toolchain-only**: it bakes no application source. The repo is **bind-mounted live at run time** (§6.2), so editing code never invalidates an image layer.

### 5.2 Single-source version policy (no magic numbers)
The `Containerfile` declares only `ARG`s. The `Makefile` `image` target reads each value from its authoritative source and passes it through:
```
GO_VERSION   := $(shell awk '$$1=="golang"{print $$2}' .tool-versions)
NODE_VERSION := $(shell awk '$$1=="nodejs"{print $$2}' .tool-versions)
# GOLANGCI / GOVULN already exist as Makefile vars
```
Bumping a tool is a one-line edit in its existing home; the image follows. There is no second place to forget.

### 5.3 Host cleanliness (artifact masks — target-aware)
`run.sh` bind-mounts the repo read-write. Masks are **anonymous volumes**, and a crucial fact governs them: **an anonymous volume seeds from the image layer at that path, not from the host bind-mount.** The image is toolchain-only (no repo baked), so a masked path is *empty* inside the container unless something writes it during the run.

- **`frontend/node_modules` — masked on every target.** The host's copy is built against the host distro; the container re-runs `npm ci` into the empty volume. Harmless to mask universally (every target either ignores it or repopulates it).
- **`frontend/dist` — masked ONLY for frontend-building targets (`test-e2e`, `gui-build`).** Those run `vite build`, which would otherwise clobber the **tracked** `//go:embed frontend/dist` stub on the host; the build repopulates the masked volume instead, leaving the host stub intact. **[Round-10 operational note]** This is automatic only for `test-e2e` (dispatched through `run.sh` with the export in scope). `gui-build` is native-by-default and not in `DZ`, so it is never dispatched through `run.sh`: its Makefile export is inert (`run.sh` reads `PERCH_MASK_DIST` from the host invocation, not the in-container make env), and the in-container verify check protects the host stub only when `PERCH_MASK_DIST=1` is set on the host invocation (see `containers/README.md`). §6.2/§128's "exports … for the two frontend-building targets" stays literally true. **Go targets must NOT mask dist** — they compile the root package carrying `//go:embed frontend/dist` but never write dist, so they get the plain bind-mount (committed stub present → embed resolves; nothing written → host clean). Masking dist for a Go target yields an empty dir → `pattern frontend/dist: no matching files found` at compile time. This is why the mask set is **per-target**, not a single uniform set.

Named cache volumes (`go-build`, baked Playwright browsers) persist across runs for speed. Report dirs (`playwright-report/`, `test-results/`) are already gitignored.

### 5.4 Future images (sibling dirs)
New images are **new subdirectories**, never a restructure:
- `containers/smoke/` — `FROM perch-dev`, adds the GUI smoke harness (`xvfb-run`, mocked agents) to automate parts of the manual checklist.
- `containers/ci/` — a leaner runner, only if a pipeline ever wants less than the full dev image.
- `containers/shared/` — shared `Containerfile` snippets, only if duplication emerges.
`run.sh` already takes an image argument (default `dev`), so a new image needs only a new `Makefile` target.

---

## 6. Execution Model

### 6.1 The `CONTAINERIZE` toggle (local ≙ pipeline)
One variable makes every target work in both worlds:
- **Local (default, `CONTAINERIZE=1`):** the target re-enters the image — `run.sh dev make CONTAINERIZE=0 <target>`.
- **Inside the image (pipeline, `CONTAINERIZE=0`):** the target runs natively, no nesting.

A pipeline is therefore just "run the image, call `make` inside" — and the image is pushable, so CI pulls identical bits. No separate CI script, no docker-in-docker.

### 6.2 `run.sh` (the generic exec)
`containers/run.sh <image> <cmd…>` is the one place podman flags live. `node_modules` and caches are always masked; the **`dist` mask is opt-in** via `PERCH_MASK_DIST=1` (set by `test-e2e` / `gui-build` only — §5.3):
```
mask_dist=${PERCH_MASK_DIST:+-v /work/frontend/dist}
podman run --rm \
  -v "$ROOT":/work:Z \
  -v /work/frontend/node_modules \
  $mask_dist \
  -v perch-go-build:/root/.cache/go-build \
  -w /work perch-dev:latest "$@"
```
(`:Z` is a harmless no-op on this AppArmor host; kept for SELinux portability.) The `Makefile` exports `PERCH_MASK_DIST=1` for the two frontend-building targets and leaves it unset elsewhere. `run.sh` is the single definition of "how perch runs in a container," reused by `make`.

### 6.3 Make surface (one front door)
The dispatch is DRY — one rule containerises every listed target:
```
CONTAINERIZE ?= 1
DZ := test test-integration test-front test-e2e lint vet vulncheck test-all
ifeq ($(CONTAINERIZE),1)
$(DZ): | image
	@bash containers/run.sh dev make CONTAINERIZE=0 $@
else
test:            ; go test -race -count=1 ./...
test-integration:; go test -race -count=1 -tags=integration ./...
test-front:      ; npm --prefix frontend run check && npm --prefix frontend test
test-e2e:        ; npm --prefix frontend run test:e2e
lint:            ; golangci-lint run
vet:             ; go vet ./...
vulncheck:       ; govulncheck ./...
test-all:        ; $(MAKE) test-integration test-front test-e2e CONTAINERIZE=0
endif
```
New/changed targets: `image` (build the pinned image), `shell` (drop into `perch-dev`), `test-front`, `test-e2e`. Existing Go targets keep their meaning, now executed in the image. `test-all` becomes the genuine everything-gate (the prior `test-all` was identical to `test-integration` — a redundancy this removes). The proof-of-concept `frontend/e2e/run-in-container.sh` is **deleted**, folded into this pattern.

---

## 7. Directory Layout

```
containers/
├── README.md                  # the container framework: images, build/run, the CONTAINERIZE pattern
├── run.sh                     # generic exec: run a cmd in an image (image arg; bind-mount, masks, caches)
└── dev/                       # ── image: perch-dev (the dev/test environment) ──
    └── Containerfile
# future siblings: containers/smoke/  containers/ci/  containers/shared/

.devcontainer/
└── devcontainer.json          # build.dockerfile → ../containers/dev/Containerfile (reuses the one def)

.dockerignore                  # at repo root (= build-context root): prune the build context
Makefile                       # + image / shell / test-front / test-e2e targets + CONTAINERIZE dispatch
```

**Shape rationale:** image *definitions* nest one-per-dir under `containers/`; cross-image tooling (`run.sh`, `README.md`) sits at `containers/` root. Test *specs stay co-located* with code (Go `_test.go`, `frontend/e2e/*.spec.ts`); only *execution* is centralised. A bare `containers/Containerfile` would force a rename the moment a second image lands — so it is nested from day one.

---

## 8. Testing Strategy

This framework is the **execution layer for cockpit §10**. It does not redefine what is tested; it defines where it runs.

- **The pyramid, all behind `make`:** Go unit → Go integration (`-tags=integration`, incl. the headless full-loop fake-agent test) → frontend unit + `tsc` → e2e (Playwright, chromium) → *(future)* headless WebKit GUI smoke → manual WebKit + real-agent smoke.
- **Container by default, native by toggle.** Every layer runs in `perch-dev` for consistency. `CONTAINERIZE=0` exists for speed inside an already-containerised pipeline, not as a host escape hatch.
- **The one honest gap:** the **manual** WebKit + real-agent + D-Bus single-instance smoke (cockpit §10 spikes 4–5, smoke-checklist) is not yet automatable here — real `claude`/`opencode` binaries, a live WebKit window, and cross-process D-Bus single-instance are out of reach of the headless image today. The image bakes `xvfb` + WebKit so a `containers/smoke/` sibling can close part of this gap later; until then the checklist stays manual and **says so** (no mock-green masquerading as a passing GUI).
- **Quality gates** (`vet`, `golangci-lint`, `govulncheck`, `tsc`) run in the same image, so "green locally" and "green in a pipeline" are the same statement.
- **Build-verification gate (verify-first — the first implementation steps, before any doc polish).** Build the image, then run the **three distinct failure surfaces** in it and confirm a clean host tree (`git status`) after each:
  1. `make test` — exercises Go compile + `//go:embed frontend/dist` with the dist mask **off** (catches the §5.3 trap).
  2. `make test-e2e` — re-proves the e2e path on the **real `ubuntu:24.04` base** (the PoC only proved the `mcr` base).
  3. `make gui-build` — exercises **cgo + WebKit2GTK linking**, a surface neither of the above touches.
  Only once all three are green does the spec flip to *Approved* and the docs (CONTRIBUTING/README/smoke) get written. Verify-first, doc-second — the discipline that caught the masking tests in the cockpit rounds.

---

## 9. devcontainer Integration

**The seam:** `devcontainer.json` `build.args` is static JSON — it cannot shell out to `.tool-versions`. So "one Containerfile + no hardcoded versions + devcontainer" cannot all hold if the devcontainer *builds* the image itself (it would either hardcode versions → drift, or need a generated args file).

**Resolution — devcontainer consumes the `make`-built image, it doesn't rebuild it:**
```jsonc
{
  "name": "perch-dev",
  "initializeCommand": "make image",   // runs on the host: single-sourced build via the Makefile
  "image": "perch-dev:latest"          // then attach to the image make just built
}
```
This reuses the **one** build path (the `Makefile`, which fills ARGs from `.tool-versions`/lockfile), so there is zero duplicate version pinning and zero duplicate image definition. Opening the repo in a devcontainer-aware editor yields the byte-identical environment `make` uses. (`build.dockerfile` with static `build.args` is the rejected alternative — it reintroduces the very version drift §5.2 eliminates.)

---

## 10. Security

- **No secrets in the image.** Token-bearing files (per-session `.claude/settings.json`, hook bearer tokens — cockpit §9) are produced at runtime, never baked into a layer. The `.dockerignore` excludes `.claude`, local config, and any `*.env` from the build context.
- **Rootless by default.** podman runs rootless; the image needs no privileged flags. No daemon socket is mounted.
- **No network surface added.** The image opens no ports; it is a build/test sandbox. The cockpit's only local surface (the localhost token-authed Claude hook listener) is unaffected and untested-by-network here.
- **Bind-mount is scoped + masked.** Only the repo is mounted; tracked artifacts and host `node_modules` are masked (§5.3) so a container run cannot corrupt the working tree.
- **Test hygiene unchanged (cockpit §9):** tests never run a real `claude`/`opencode`, never write the real `$HOME`, never touch a shared server — and now run in an isolated container on top of that.
- **Image is not pushed without explicit auth.** Publishing to a registry (for a future pipeline) is a deliberate, authenticated step, never automatic.

---

## 11. Package / File Layout (touched)

```
containers/dev/Containerfile   # new: the pinned perch-dev image (ARGs only; no hardcoded versions)
containers/run.sh              # new: generic in-image exec (bind-mount, masks, cache volumes)
containers/README.md           # new: framework doc (images, build/run, CONTAINERIZE)
.devcontainer/devcontainer.json# new: reuses containers/dev/Containerfile
.dockerignore                  # new: lean build context
Makefile                       # edit: image/shell/test-front/test-e2e + CONTAINERIZE dispatch
frontend/e2e/run-in-container.sh # DELETE: folded into the unified pattern
frontend/package.json          # edit: test:e2e:container delegates to containers/run.sh (or removed)
CONTRIBUTING.md                # edit: "Testing" section — the pyramid + the single make front door
README.md                      # edit: build/test note points at the containerised flow
docs/superpowers/smoke-checklist.md # edit: e2e via `make test-e2e`; manual GUI smoke still manual
```

---

## 12. Open Risks (tracked, not deferred)

- **Node-version drift if the base bumps.** Mitigated: Node is installed by pinned tarball from `.tool-versions`, not inherited from the base image — so an `ubuntu:24.04` refresh cannot silently change it.
- **Playwright tag availability on bump.** `npx playwright install` resolves the browser from the lockfile pin, so bumping `@playwright/test` Just Works; the *base* distro must remain a Playwright-supported one (Noble is, for the 1.x line).
- **Image size / first-build time.** Go + Node + browsers + WebKit libs is a multi-hundred-MB image and a multi-minute first build. Accepted: it is built once, layer-cached, and (later) pulled in CI. Cache volumes keep repeat runs fast.
- **GUI smoke automation is hard.** Real-agent + live-WebKit + cross-process D-Bus single-instance is not closed by the headless image (§8). Accepted limitation; `containers/smoke/` is the designed path to chip at it, and the manual checklist remains authoritative until then.
- **Host distro is the forcing function, not a permanent constraint.** When Playwright ships official 26.04 support, native runs become possible again — but the container framework remains the canonical, consistent path regardless.
