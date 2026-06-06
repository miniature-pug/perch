# Round-9 Audit — 2026-06-05

**Branch:** `feat/perch-v1`
**Baseline:** `make test-all` GREEN at HEAD `db89887` (16 Go pkgs race+integration, golangci-lint, vet, govulncheck, vitest 397, 61 Playwright e2e).
**Method:** Full-codebase parity + quality re-audit (broader than the round 7–8 deltas — every file: config, logic, deps, docs). 10 read-only Sonnet agents fanned out across 10 dimensions, each held to a hard falsifiability bar. Findings reconciled here with an explicit disposition — **action** (backed by a falsifiable artifact) or **decline-with-reason**. No silent drops.

**Falsifiable artifact = one of:** a failing/missing test; a spec quote the code contradicts; an LSP-verified zero-caller; a literal that is repeated OR a tunable default/policy OR opaque. Opinion/style → declined.

---

## Headline

Nine rounds in, the un-swept surface was the **pre-round-3 core** + cross-cutting concerns (deps, build config, design-token discipline) that delta-audits never covered. Parity is clean (every spec feature implemented or spec-documented-deferred). The genuine yield this round is **design-system/config-centralization debt**: literals that bypass tokens/consts that already exist, two cross-package constant duplications (one with factually-false "single source of truth" comments), a small dependency/build-config cleanup, one spec drift in the Makefile, and one test-masking gap. All actionable items are mechanical and gate-verifiable.

The recorded failure mode (agents manufacturing findings that re-litigate settled decisions, then regressing on the fix) was actively guarded: every "dead code" claim LSP-verified; the round-8-settled `loopbackHost` decline was **reopened only because new evidence refuted its premise** (the `agent → hooklistener` import edge already exists); two cross-agent conflicts (`--perch-fs-h1/h2` "dead" vs "spec-frozen") adjudicated against primary sources before acting.

---

## Parity — CLEAN (3 specs)

All three specs map to code with zero cut corners.

- **Cockpit spec** (`2026-06-02`): every §1–§11 feature IMPLEMENTED. 3 spec-documented deferrals: `App.Worktrees` IPC → richer `DiscoverRepos` (frecency/dedup); `pty.Spawn` gained an additive `exitEvent` param; `Caps.Tokens` removed by design (token/cost subsystem deleted). All annotated in-spec.
- **Worktree-session-model spec** (`2026-06-05`): all 40+ features present incl. the in-repo non-worktree option end-to-end, BaseRef persistence, dirty-guard → `ErrWorktreeDirty`, safe-only cleanup default, home-shell mount-persistence, full model-selection removal.
- **Container-framework spec** (`2026-06-04`): 23/24 implemented; 1 narrow drift (see C-1).

**Declined parity nits** (capability present, not a cut corner):
- Spec "Export" drag-out command label — both named fallbacks (Copy path, Reveal in Files) are implemented; "Export" is an alternate label in the same spec section, not a distinct missing feature.
- `app.ErrBranchInUse` alias absent — only `git.ErrBranchInUse` exists; no functional gap (the app path that needs a sentinel, `RemoveWorkspace`, has the `ErrWorktreeDirty` alias; tests import `git.ErrBranchInUse` directly).
- Resume-preview "Open for more" affordance — the plan consciously simplified to Open/Cancel; the **Open** button reopens fully, so the capability exists. Plan is normative; declined.

---

## Actions (falsifiable-artifact-backed)

| ID | Dimension | Finding (evidence) | Disposition |
|----|-----------|--------------------|-------------|
| **C-1** | Container parity | Spec §6.2: "Makefile exports `PERCH_MASK_DIST=1` for the two frontend-building targets" names **test-e2e and gui-build**; `Makefile:76` exports it only for `test-e2e`. `gui-build` has no export (README documents a manual workaround). | **ACTION** — add `gui-build: export PERCH_MASK_DIST = 1`. Harmless (gui-build runs native by default; export only bites in-container) and honors the spec. |
| **G-1** | Go centralization | `configDirMode = 0o700` duplicated: `internal/registry/registry.go:20` + `app/app.go:56`, both creating `~/.config/perch/`. registry is the canonical config-dir owner (`DefaultConfigDir`, "perch name in exactly one place"); app already imports registry. | **ACTION** — export `registry.ConfigDirMode`; `app` references it; drop app's duplicate const. |
| **G-2** | Go centralization | `loopbackHost = "127.0.0.1"` duplicated: `internal/hooklistener/listener.go:19` + `internal/agent/opencode_monitor.go:48`. **Both comments claim "single source of truth" — factually false.** `agent` already imports `hooklistener` (`claude_monitor.go:16`). | **ACTION** — export `hooklistener.LoopbackHost`; `agent/opencode_monitor.go` references it; remove the duplicate const + correct the misleading comment. (Refutes round-8's "would force a new package" — the import edge exists.) |
| **G-3** | Test masking | `safe = clean && merged` (`app/app.go`): the only `safe==true` test (`app_test.go:2686`) sets clean=true AND merged=true. Reverting `&& merged` → `clean` keeps it green — the `merged` conjunct is unguarded. | **ACTION** — add a case: clean=true, merged=false ⇒ safe=false. |
| **F-1** | Frontend centralization | `border-radius: 4px` hardcoded at 28 sites while `--perch-radius-sm: 4px` (tokens.css:35) exists and is used at 12 other sites. | **ACTION** — replace all 28 with `var(--perch-radius-sm)`. |
| **F-2** | Frontend centralization | Focus-ring `outline: ...2px solid var(--perch-accent)` hardcodes `2px` at 22 sites while `--perch-ring-w: 2px` (tokens.css:23) exists and is used at App:1039. | **ACTION** — tokenize the width to `var(--perch-ring-w)`; keep the accent color per-element. |
| **F-3** | Frontend centralization | `font-size: 12px` at 3 sites bypasses `--perch-fs-caption: 12px` (tokens.css:9). | **ACTION** — replace with `var(--perch-fs-caption)`. |
| **F-4** | Frontend centralization | `::-webkit-scrollbar { width: 6px }` at 6 sites; no token. | **ACTION** — add `--perch-scrollbar-w: 6px`; replace all 6. |
| **F-5** | Frontend centralization | `-webkit-scrollbar-thumb { border-radius: 3px }` at 6 sites; no token. | **ACTION** — add `--perch-scrollbar-radius: 3px`; replace all 6. |
| **F-6** | Frontend centralization | `:disabled { opacity: 0.4 }` policy repeated at 4 sites; opaque at use site; no token. | **ACTION** — add `--perch-opacity-disabled: 0.4`; replace all 4. |
| **FE-1** | Frontend dead code | `export type ThemeName` (`constants.ts:53`) — zero consumers (definition only; grep-confirmed across .svelte/.ts). Cannot be wired to `AppSettings.theme` (that mirrors the Go `string` contract). | **ACTION** — remove the unused type. |
| **D-1** | Deps | `@codemirror/theme-one-dark@6.1.3` declared in `frontend/package.json` but zero imports anywhere (Editor builds its theme inline; the "one-dark" perch theme is a CSS block, not this pkg). | **ACTION** — remove from dependencies; regenerate lockfile. |
| **B-1** | Build config | Preview port `4173` hardcoded in both `frontend/package.json:8` and `playwright.config.ts:6,16` — must stay in sync, not centralized. | **ACTION** — single-source the port (vite `preview.port` + shared constant consumed by playwright config). |
| **B-2** | Build config | `wails.json:5` `"frontend:install": "npm install"` contradicts the reproducibility contract — every Makefile path uses `npm ci`. | **ACTION** — change to `npm ci`. |

---

## Declines (considered; below the bar or settled)

| Item | Reason |
|------|--------|
| `--perch-fs-h1/h2`, `--perch-lh-h1/h2` "unused" (frontend dead-code agent) | **Spec §7.4 line 136 explicitly freezes** "New heading tokens: `--perch-fs-h1` (24px) / `--perch-fs-h2` (20px)" as the design-system type scale; lh-h1/h2 are their companions. Removing contradicts the spec. Keep. |
| `Worktree.Prunable` write-only field (Go dead-code agent) | Parsed from `git worktree list --porcelain` into an exported struct that faithfully mirrors porcelain output; siblings (Head/Detached/Locked) are consumed. Removing one field makes parsing asymmetric. Not actionable. |
| `font-size: 14px` icon glyphs (2 sites) | Only 2 sites; nearest token `--perch-fs-code` is semantically wrong for icons; adding `--perch-fs-icon` for 2 sites is over-tokenization (the over-centralization trap). Declined. |
| `0o600` token/settings file modes; per-package 64-elem channel buffers; `border-radius: 3px` inline-code pill; `opacity: 0.7`; `999px` pill; xterm `SELECTION_ALPHA_HEX` | All used-once-and-local OR same-value-distinct-semantics. Naming would add indirection without removing duplication. Declined (advisor's over-centralization guard). |
| Model-removal registry test is "absence-only" | Go has no clean idiom to fence field-absence at compile time (a `_ = w.Model` probe wouldn't compile *now*, breaking the build). The behavioral guard `TestClaudeNewArgs_NoModel`/`TestOpencodeNewArgs_NoModel` (asserts no `--model` ever emitted) is the meaningful protection and is solid. Declined. |
| `DZ`/`test-all` structure differs from spec §6.3 code block | Intentional refinement — `test-all` outside `DZ` dispatches each sub-target into its own container with the right mask, which the spec's own comment endorses. Declined. |
| `ApprovalFlow.test.ts` filename (no `ApprovalFlow.svelte`) | Cosmetic naming only; tests real ApprovalCard + notification behavior. Not dead code; rename is churn-without-value. Declined. |

---

## Honest ceiling

Unchanged from prior rounds: WebKit + real-agent + attach-D-Bus + Playwright visual/feel behavior is **user-gated manual smoke** (`docs/superpowers/smoke-checklist.md`). No automated audit substitutes for it.

**NOT pushed; merge to main is user-only.**
