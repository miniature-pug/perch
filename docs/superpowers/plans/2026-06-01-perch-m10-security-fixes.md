# M10 Security Fixes — Implementation Plan

> Sub-plan of `2026-06-01-perch-v1-finalization.md`. Execute via subagent-driven-development.
> Every fix lands with a regression test that **encodes the exploit** (fails on un-fixed code),
> asserted via `FakeRunner.Calls` (no real spawns) + hermetic FS (`t.Setenv("HOME", t.TempDir())`,
> temp dirs). Findings + verdicts: `docs/security-audit.md`. Branch `feat/perch-v1`.

**Goal:** close the 8 confirmed holes from the M10 audit; clear the supply-chain pin.

**Tech stack:** Go 1.26, stdlib only (crypto/sha256, io, regexp avoided where a scan suffices),
Charm v1 TUI, the §20.1 Runner/FakeRunner test seam.

Order (by leverage): T1 trust (CRITICAL) → T2 tmux parse (HIGH) → T3 git argv (MED) → T4 state
(LOW) → T5 supply-chain + V5 guard-test (and docs).

---

## T1 — V1 trust-on-first-use for `.perch.toml` hooks (CRITICAL)

**Files:**
- Create: `internal/trust/trust.go`, `internal/trust/trust_test.go`
- Modify: `internal/config/config.go` (expose resolved project-config path + content hash)
- Modify: `internal/tui/modal.go` (new `modalTrustConfirm` + render)
- Modify: `internal/tui/worktree_actions.go` (gate hook execution; thread a trust decision)
- Modify: `internal/tui/app.go` (handle the trust message + modal keys)
- Modify: `internal/tui/help.go`, `internal/tui/keys.go` (hints)
- Tests: `internal/trust/trust_test.go`, `internal/config/config_test.go`,
  `internal/tui/worktree_actions_test.go` (the exploit test)

### Design

A repo's `post_create`/`pre_remove` hooks are NEVER run until that repo's `.perch.toml` is
approved once. Trust is keyed on the **resolved config-file path `config.Load` actually used**
(walk-up may place it above the project dir) **plus the sha256 of the file contents** (an edit
re-prompts). Store lives in the perch **state dir** (global, never in-repo), mode `0600`.
Non-interactive/test default = **DENY** (hooks skipped).

**`internal/trust/trust.go`:**
```go
// Package trust records which project .perch.toml files the user has approved to
// run shell hooks. A repo's hooks are never executed until its config is approved
// once; editing the config re-requires approval (the content hash changes).
package trust

// Hash returns the hex sha256 of a .perch.toml's raw bytes.
func Hash(content []byte) string

// Store is a set of approved configs: absolute config path -> approved content hash.
type Store struct { /* path string; entries map[string]string */ }

// Load reads the trust file at path. A missing file yields an empty Store (no
// error). A malformed file is an error (callers may treat as "nothing trusted").
func Load(path string) (*Store, error)

// Trusted reports whether configPath was approved with this exact content hash.
func (s *Store) Trusted(configPath, hash string) bool

// Approve records configPath@hash and atomically writes the trust file (0600,
// temp+rename, mkdir 0700). Replaces any prior hash for the same path.
func (s *Store) Approve(configPath, hash string) error
```
Tests (hermetic, temp dir): empty store trusts nothing; Approve then Trusted true; wrong hash →
false (simulates edited config); unknown path → false; malformed file → error; file written `0600`;
Approve replaces prior hash for same path; atomic (no `.tmp` left behind).

**`internal/config/config.go`:** expose the resolved config provenance so the gate can key on it.
- Add to `Config`: `ProjectConfigPath string` and `ProjectConfigHash string` (empty when no
  `.perch.toml` found).
- In `findAndLoadProject`: when the candidate is found, `os.ReadFile` it (instead of
  `toml.DecodeFile`), `toml.Decode(string(bytes), pc)`, and return the path + bytes so `merge` can
  set `ProjectConfigPath` + `ProjectConfigHash = trust.Hash(bytes)`. (Avoid an import cycle: compute
  sha256 inline in config, OR have `merge` accept the path+bytes and store the hash via a tiny
  local sha256 helper — do NOT import `internal/trust` from `internal/config`. trust.Hash and the
  config helper can both be thin wrappers over crypto/sha256; duplication of one line is fine.)
- Test: a project dir with a `.perch.toml` → `ProjectConfigPath` is the resolved candidate,
  `ProjectConfigHash` is stable and changes when the file content changes; no `.perch.toml` →
  both empty.

**TUI gate (the control flow — minimal-mutation-safe, TOCTOU-closed):**

Add a value type carried through messages and Cmds:
```go
// trustDecision is the resolved user choice for a pending hook-bearing action.
// nil means "not yet decided" — the Cmd must gate.
type trustDecision struct {
    allow        bool   // run hooks?
    approvedHash string // the hash the user approved (guards TOCTOU on re-load)
}
```

`worktreeCreateCmd(ms modalState, dec *trustDecision)` and
`removeCmd(spec modalState, force, skipPrep bool, dec *trustDecision)`:
1. Load + validate config FIRST (no FS mutation yet).
2. Let `hooks = cfg.PostCreate` (create) / `cfg.PreRemove` (remove).
3. If `len(hooks) > 0`:
   - If `dec == nil`: compute trust via the Store (`trust.Load(<baseDir>/trust.json)`); if
     `Trusted(cfg.ProjectConfigPath, cfg.ProjectConfigHash)` → treat as `dec = {allow:true,
     approvedHash: cfg.ProjectConfigHash}`. Otherwise return a `*Msg` carrying a `trustReq`
     (configPath, hash, phase, and enough context to resume) and DO NOTHING ELSE (no git work).
   - If `dec != nil`: proceed. Run hooks **only if** `dec.allow && dec.approvedHash ==
     cfg.ProjectConfigHash` (the hash re-check closes the edit-after-approval TOCTOU; on mismatch
     skip hooks and surface a non-fatal note). If `!dec.allow`, skip hooks but continue the
     create/remove (worktree creation itself is benign once base_branch is validated — see T3).
4. Everything after the hook step is unchanged.

`trustReq` (carried on `worktreeCreatedMsg`/`removeResultMsg` or a dedicated `trustNeededMsg`):
```go
type trustReq struct {
    configPath string
    hash       string
    phase      string      // "post_create" | "pre_remove"
    create     *modalState // non-nil to resume worktreeCreateCmd
    remove     *removeResume // non-nil to resume removeCmd: {spec modalState; force, skipPrep bool}
}
```

`app.go` Update:
- On a message carrying a non-nil `trustReq`: set `m.modal = modalState{kind: modalTrustConfirm, trust: req}` (add `trust *trustReq` field to `modalState`), `m.showHelp=false`.
- Key handling while `modalTrustConfirm`:
  - `a` (approve always): `store.Approve(req.configPath, req.hash)` (best-effort; toast on error), then re-dispatch the pending Cmd with `dec = {allow:true, approvedHash: req.hash}`; clear modal.
  - `o` (once): re-dispatch with `dec = {allow:true, approvedHash: req.hash}` WITHOUT Approve; clear modal.
  - `d` / `n` / `esc` (deny): re-dispatch with `dec = {allow:false}`; clear modal.

`modal.go` render `modalTrustConfirm`:
`".perch.toml in <dir> defines shell hooks (<phase>). Run them?\n(a) trust always  (o) once  (d) deny"`
— width-clamped exactly like the other modals (MaxWidth at the View layer already handles it; show
`filepath.Dir(req.configPath)`). Add `modalFooterHint` + help entries.

### Exploit test (must FAIL on un-fixed code)
`internal/tui/worktree_actions_test.go`: build a Model whose loader Config has
`PostCreate = ["touch /tmp/pwned"]` (or any cmd) and `ProjectConfigPath`/`ProjectConfigHash` set,
with a `FakeRunner` and a temp `BaseDir` containing NO trust entry. Drive the create flow
(pick "new worktree"). Assert: the result is the trust modal (kind `modalTrustConfirm`), and the
`FakeRunner.Calls` contain **zero `sh -c`** invocations (hooks did not run). Then simulate pressing
`d` (deny) → assert worktree create still proceeds (git worktree add IS called) but still zero
`sh -c`. Then a second test: pre-seed the trust store with the matching path@hash → create runs and
`FakeRunner.Calls` DO contain the `sh -c` hook. (Use a FakeRunner; never spawn a real shell.)

---

## T2 — V6a/V6b tmux parse hardening (HIGH/MED)

**Files:** Modify `internal/tmux/tmux.go` (`parsePanes`); maybe `internal/tui/data.go`
(`buildLiveIndex`). Tests: `internal/tmux/tmux_test.go`, `internal/tui/data_test.go`.

- **V6a:** in `parsePanes`, after `strings.Split(line, "\x1f")`, reject any record whose
  perch-controlled fields (`PerchSession`, `PerchStatus`) contain `\x1f`, `\n`, or `\r`; ensure a
  bounded parse so an injected extra field cannot shift offsets (the line is already split per `\n`;
  the risk is `\x1f` inside an option value desyncing columns — since `set-option` values can't
  themselves contain a literal newline that survives `list-panes` line framing the same way, the
  primary guard is: if `len(fields) != fieldCount`, skip the line; and validate the two `@perch_*`
  fields). Keep it simple and strict: `if len(fields) < fieldCount { continue }` already exists —
  add `if extra := len(fields); extra > fieldCount { continue }` OR cap via `SplitN(line,"\x1f",
  fieldCount)` so trailing `\x1f` is absorbed into the last field, then validate that last field.
  Decide the cleanest; the invariant to TEST: a crafted `@perch_session` containing `\x1f` does not
  cause `PerchStatus` to be read from the wrong column, and an embedded newline does not create a
  second bogus `Pane`.
- **V6b:** add `func validSessionID(s string) bool` (canonical UUID, e.g. matches
  `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`). Apply where
  `PerchSession` is used as an identity key (in `parsePanes` set it only if valid, OR in
  `buildLiveIndex` skip panes whose `PerchSession` is non-empty-but-invalid). Pick the layer that
  keeps `internal/tmux` free of perch-session semantics if cleaner — but the UUID shape is generic
  enough to live in tmux parse. Confirm claude session IDs ARE UUIDs (check
  `internal/agent/claude.go` id derivation: `strings.TrimSuffix(filepath.Base(path), ".jsonl")` —
  verify the on-disk transcript filenames are UUIDs; if they are NOT strict UUIDs, relax the
  validator to the actual id charset: `^[A-Za-z0-9_-]+$` with a length bound and NO `\x1f`/`\n`).
  **Verify against a real session-id sample before locking the regex.**

### Exploit tests
- V6a: feed `parsePanes` a raw blob where `@perch_session` field = `"realuuid\x1ffakestatus"` and a
  second line injected via embedded newline; assert no `Pane` carries the spoofed status and no
  extra phantom `Pane` appears.
- V6b: a pane whose `@perch_session = "not-a-uuid"` (or a path-y value) is NOT admitted to the live
  index / does not match a real session.

---

## T3 — V3-A/V3-B/V2′ git argv & path hardening (MED/LOW)

**Files:** Modify `internal/git/worktree.go` (validate base/branch; `--`), `internal/git/git.go`
(if a shared ref validator fits there), `internal/tmux/cleanup.go` (`--` before branch),
`internal/config/config.go` (constrain project-level `worktree_dir`). Tests in each package.

- **V3-A:** add `func ValidRef(name string) error` (reject empty, leading `-`, `..`, `~^:?*[`,
  control chars, trailing `/`, `.lock` suffix, space — the git check-ref-format essentials).
  Validate `cfg.BaseBranch` (when non-empty) before `AddWorktree`; and validate the derived
  `branch` defensively. Add `--` before user positionals: `git ... worktree add -b <branch> --
  <path> <base>` is wrong (base is a committish, not after `--`); instead validate base strictly
  (it cannot be a flag) — for `worktree add`, the safe form is to ensure neither `branch`, `path`,
  nor `base` begins with `-` (path is always absolute; branch is `perch/…`; base is validated).
  Prefer validation over `--` here since `git worktree add`'s positional grammar doesn't take a
  clean `--`. **Verify** the exact `git worktree add` flag/positional grammar before choosing
  `--` vs validation; encode whichever in a test.
- **V3-B:** `tmux/cleanup.go:83` → `git -C <q> branch -d -- <q branch>` (add `--`). Test the
  generated script string contains `branch -d --`.
- **V2′:** make project-level `worktree_dir` **relative-only**: a `.perch.toml` may only set a
  relative `worktree_dir` (resolved under the repo parent); an **absolute** `worktree_dir` is
  honored ONLY from global/user config. Implementation: track provenance — `merge` knows whether
  `worktree_dir` came from project vs global. Add a `Config` field or a validate-time parameter so
  `Validate` rejects an absolute `worktree_dir` that originated from project config. Keep the
  existing `.git`-containment check. Test: project config absolute `worktree_dir` → error; global
  absolute → allowed; project relative → allowed (and still `.git`-guarded).

### Exploit tests
- `base_branch = "--upload-pack=x"` and `"--no-checkout"` → rejected before any `git` argv (assert
  `FakeRunner.Calls` empty / create errors).
- project `worktree_dir = "/etc/perch"` → rejected; global `/abs` → ok.

---

## T4 — V7b/V7c state-store hardening (LOW)

**Files:** Modify `internal/state/state.go` (size-capped reads), `internal/resurrect/resurrect.go`
(reject out-of-root `Tree`). Tests in both.

- **V7b:** add `readLimited(path string, max int64) ([]byte, error)` (`os.Open` + `io.ReadAll(io.
  LimitReader(f, max+1))`, error if `> max`). Use it in `LoadState` and `LoadWindows` (cap e.g.
  16 MiB). Also cap the claude transcript whole-file growth if trivial — but keep scope to state.
- **V7c:** `resurrect` already loads windows; add a guard that skips/ignores any window whose
  `Tree` does not reside under the configured scan root(s). Thread the root(s) into the reconcile
  (resurrect already has discovery context — verify). Test: a `windows/x.json` with
  `Tree:"/tmp/evil"` is skipped; a `Tree` under root is kept.

### Tests
- V7b: a state.json larger than the cap → `LoadState` returns an error (or empty + error), never
  OOM; under cap → parses.
- V7c: out-of-root tree skipped; in-root kept.

---

## T5 — V11 supply chain + V5 guard-test + docs (LOW/INFO)

**Files:** `go.mod`, `go.sum`, `vendor/` (regenerated), `internal/tmux/tmux_test.go` (V5 guard),
`docs/security-audit.md` (already written — tick verdicts), `README`/`CHANGELOG` later (M13).

- **V11:** bump `golang.org/x/sys` `v0.38.0` → `v0.44.0` (proxy-verified 2026-04-23, 39d). Run
  `go get golang.org/x/sys@v0.44.0 && go mod tidy && go mod vendor`. Re-run **full** `-race` unit +
  integration suite (x/sys underlies bubbletea input on Linux/macOS) AND `make vulncheck` → clean.
- **teatest:** leave as-is (test-only, untagged module, no shipped risk); the audit ledger records
  the documented exception. Flag in the morning report.
- **V5 guard-test:** add a test asserting the `capture-pane` argv built by `CapturePane` contains
  `-p` and never `-e` (lock the control; if `-e` is ever added a CSI-SGR-only sanitizer becomes
  mandatory). Assert via `FakeRunner.Calls`.

---

## DoD M10
Every confirmed hole fixed with an exploit-encoding regression test; `docs/security-audit.md`
verdicts current; full gate green (build+embed, `go test ./...`, `-race` integration on real
git/tmux 3.6, `golangci-lint` 0, `gofmt`, `go mod verify`, `make vulncheck` clean); all non-`tui`
packages keep ≥80% coverage. Commit per task. Then proceed to M11 (after spike deep-read + advisor).
