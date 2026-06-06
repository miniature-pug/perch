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

## Resolution

All 13 actions landed in 4 commits on `feat/perch-v1`:
- `c530da0` — G-1 (`registry.ConfigDirMode`), G-2 (`hooklistener.LoopbackHost`), G-3 (`TestApp_ListStaleSessions_UnmergedNotSafe`, teeth-confirmed), C-1 (`gui-build` mask).
- `202b83d` — F-1..F-6 (radius-sm/ring-w/fs-caption/scrollbar-w/scrollbar-radius/opacity-disabled tokens), FE-1 (`ThemeName` removed).
- `fcbef35` — D-1 (`@codemirror/theme-one-dark` dropped + lockfile), B-1 (preview-port single-source via `preview-port.mjs`), B-2 (`wails.json` → `npm ci`).
- `9c11278` — reviewer residuals: F-2 completed (focus-ring **width** tokenized in 2 remaining non-accent rings, `ConfirmDialog`/`ApprovalCard`); B-1 completed (`PREVIEW_PORT` imported into `e2e/views.spec.ts` + `e2e/themes.spec.ts`, which still hard-coded `4173` — pre-existing, now single-sourced). Port `4173` now exists in exactly one place.

A full review (`git diff 0d9c431..HEAD`) verified all changes correct, no excluded literal touched, no import cycle, G-3 teeth, staging hygiene (no `frontend/dist/index.html`). The container gate (`make test-all`) is **GREEN at `9c11278`**: 16 Go pkgs race+integration, golangci-lint, vet, govulncheck, vitest 397, Playwright e2e 61 — and the new `e2e` `.mjs` import resolves in-container.

## Honest ceiling

Unchanged from prior rounds: WebKit + real-agent + attach-D-Bus + Playwright visual/feel behavior is **user-gated manual smoke** (`docs/superpowers/smoke-checklist.md`). No automated audit substitutes for it.

**NOT pushed; merge to main is user-only.**

---

# Round-10 Addendum — 2026-06-05

**State:** Round-9 branch since pushed to `origin/feat/perch-v1`. Code unchanged since gate-green code-HEAD `9c11278` (only docs commits `0ce35cc`, `9a94d02` followed). So round 10 was **not** a fresh full fan-out — that would re-audit ground settled at this exact commit (the recorded "manufacturing findings" failure mode). Scope was narrowed (advisor-confirmed) to where yield is still possible: the round-9 delta's semantics, docs accuracy (gate validates no prose), and a fresh-eyes falsifiability pass instructed to return convergence when nothing clears the bar.

**Method:** 3 read-only Sonnet agents.

## Results

- **Round-9 delta (semantics):** CONVERGENCE — every token swap value-preserving (token def == replaced literal at every site), Go const renames exact, `preview-port.mjs` truly single-sourced, no over-centralization. No falsifiable findings.
- **Fresh-eyes parity / dead-code / centralization:** CONVERGENCE — all 3 specs sampled to code; dead-code candidates re-verified against round-7 adjudication (all have prod/black-box callers); `86400000` ms/day divisor (2 prod + 2 test sites) correctly **declined** as a self-evident math constant (naming adds indirection — recorded anti-pattern). No findings beyond settled decisions.
- **Docs accuracy:** the round's only yield. One real defect that fanned into a 3-file cluster.

## Action — DOC-1: gui-build mask overclaim (3 files) + z-index ladder floor

| Sub | File | Defect (primary-source-verified) | Fix |
|-----|------|----------------------------------|-----|
| a | `containers/README.md` §65-66, §88-90 | "Both `test-e2e` and `gui-build` export `PERCH_MASK_DIST=1` … set automatically" and the verify command "no manual prefix needed" are **false**. `run.sh:35` reads `PERCH_MASK_DIST` from the **host** shell, not the in-container make env; `gui-build` is not in `DZ`, so it is never dispatched through `run.sh` — its `ifeq(CONTAINERIZE,1)` export is inert in every path. The documented `… CONTAINERIZE=0` verify command (a) skips the `ifeq` block and (b) leaves the host var unset → `vite build` clobbers the host stub. | Verify command prefixed `PERCH_MASK_DIST=1 …`; prose corrected: `test-e2e` auto-masks via dispatch, `gui-build` is native-by-default and its in-container verify check needs the host-side prefix. |
| b | `CHANGELOG.md` :40-41 | Same overclaim ("now also exports it automatically, removing the need for a manual prefix"). | Reframed as spec-§6.2 parity; notes the host-prefix caveat, points to `containers/README.md`. |
| c | `ARCHITECTURE.md` :276 | z-index ladder described as `--perch-z-editor-send` through `--perch-z-command-palette`; actual floor is `--perch-z-sidebar-rail: 1` (`tokens.css:48`), below `--perch-z-editor-send: 10`. | Floor corrected to `--perch-z-sidebar-rail`. |
| d | `docs/superpowers/specs/2026-06-04-perch-container-framework-design.md` §95 | **(advisor-caught; would otherwise have been a silent drop.)** The container-framework spec carries the *same* false claim as README/CHANGELOG — gui-build's mask "repopulates the masked volume instead, leaving the host stub intact" — which fails the same falsifiability test (spec quote the code contradicts). Fixing only the derived docs would leave the canonical design source contradicting them. | Added an in-spec `[Round-10 operational note]` (the project's established annotation pattern): masking is automatic only for `test-e2e`; gui-build is native-by-default/not-in-DZ → never dispatched through `run.sh` → export inert → verify check protects the stub only with a host-side `PERCH_MASK_DIST=1`. §6.2/§128's "exports for the two targets" stays literally true. Design-intent text preserved; operational truth recorded and pointed at README. |

**Why docs-only, not a deferral:** the code is spec-correct. `gui-build` native-by-default is intentional (it is the host GUI build, not a containerized gate target); the spec-§128 parity export is present. The defect was purely the prose claiming an effect the export cannot have. Making `gui-build` auto-mask would mean wrongly auto-containerizing the host build or adding a target for a once-run manual check — over-engineering. The project already tolerates stub regeneration in `CONTAINERIZE=0` paths (devcontainer caveat §100-104).

**Landed:** `648a317` (docs-only). No gate run — zero code touched since `9c11278`; the container gate validates code/tests, not markdown, and cannot be affected.

**NOT pushed; merge to main is user-only.**

---

# Round-11 Addendum — 2026-06-06

**State:** Code had been frozen at gate-green code-HEAD `9c11278` since round 8 (rounds 8, 9, and 10 each converged; round 10 was docs-only). This round's audit surface = the `9c11278..HEAD` docs diff (verified accurate against primary sources) + a fresh-eyes adversarial falsification pass over the code. That falsification pass found the codebase was **NOT fully converged** — one gate-blind logic bug remained on an unexercised path.

**Method:** 2 read-only Sonnet agents — docs-prose verification (re-checking the 5 round-10 claims against run.sh/Makefile/tokens.css) + adversarial falsification pass over the code (hard empirical-repro bar). Scope narrowed from a fresh full fan-out (advisor-confirmed): a full re-audit would re-litigate settled decisions at this exact commit (the recorded manufacturing-findings failure mode); yield is still possible on unexercised paths.

## Results

### Docs-prose: CONVERGENCE

All 5 round-10 docs-prose claims re-checked against primary sources:

| Claim | File | Primary source | Verdict |
|-------|------|----------------|---------|
| `test-e2e` auto-masks via dispatch; `gui-build` needs host-side `PERCH_MASK_DIST=1` prefix | `containers/README.md` §65-66, §88-90 | `run.sh:35` reads from host shell; `gui-build` not in DZ → never dispatched | CONFIRMED |
| Verify command prefixed `PERCH_MASK_DIST=1 …` | `containers/README.md` §88-90 | Makefile `ifeq(CONTAINERIZE,1)` branch skipped by CONTAINERIZE=0 | CONFIRMED |
| CHANGELOG: spec-§6.2 parity export; host-prefix caveat for gui-build | `CHANGELOG.md` :40-41 | Same run.sh/Makefile analysis | CONFIRMED |
| z-index ladder floor = `--perch-z-sidebar-rail: 1` | `ARCHITECTURE.md` :276 | `tokens.css:48` | CONFIRMED |
| Round-10 operational note on gui-build mask | container-framework spec §95 | Same run.sh analysis | CONFIRMED |

No new inaccuracy introduced by round-10's fixes.

### BUG-11-1 (HIGH, data-integrity, gate-blind): `CreateWorkspace` swallowed `ErrBranchExists` → broken workspace persisted

**Finding:** `app/app.go` `CreateWorkspace`, new-branch mode (`baseRef != ""`), contained the guard:

```go
if !errors.Is(err, gitpkg.ErrBranchExists) {
    return ...
}
```

`git worktree add -b <branch>` exits 255 and creates **no worktree directory** when the branch already exists (verified empirically). The swallow fell through and persisted a workspace record whose `WorktreePath` pointed at a directory git never created — every subsequent `OpenWorkspace` on it then failed with "no such file or directory".

**Root cause / blame:** the swallow was a stale remnant of the pre-two-mode design. Commit 2983d63 added the comment "an already-existing branch is not fatal" when the single `CreateWorkspace` path needed to handle both new-branch and existing-branch cases. Commit 64043ff rewrote the function into two modes (new-branch path via `AddWorktree -b`; existing-branch path via `AddWorktreeExisting` — no `-b`, reached via the UI "use existing branch" toggle) but carried the swallow forward unexamined. In the two-mode design a name collision in new-branch mode is a genuine user error that must surface.

**Gate-blindness:** no existing test exercised the new-branch path with an already-existing branch name; the container gate (and all prior audit rounds) were blind to the bug.

**Fix (commit `ddc7266`):** removed the swallow so any `AddWorktree` error returns directly. `ErrBranchExists` propagates wrapped; `errors.Is` semantics are preserved at every call site. Regression test `TestApp_CreateWorkspace_NewBranch_BranchAlreadyExists_Fails` asserts the returned error wraps `ErrBranchExists` and that no broken workspace is persisted (workspace list remains empty after the failed call).

**Gate re-run:** `make test-all` ALL GREEN at `ddc7266` — 16 Go pkgs race+integration, golangci-lint, vet, govulncheck clean, vitest 397, Playwright e2e 61.

## Lesson

A code commit that is frozen and converged across multiple audit rounds can still harbor a latent bug on an unexercised path. A genuine adversarial falsification pass — empirical repro, hard bar, primary sources — is the discriminator between convergence and complacency. This is distinct from the manufacturing-findings trap: the bug on this path was never previously settled or exercised; it was simply missed. Convergence claims are valid only for paths that have been tested or falsifiably inspected.

**NOT pushed; merge to main is user-only.**
