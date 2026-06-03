# perch — Security Audit (M10 + GUI pivot)

> Threat model: (a) a malicious/untrusted **repo** opened in perch (its `.perch.toml` is
> attacker-controlled), (b) malicious **agent output** rendered into the TUI, (c) malicious
> **tmux/env** state, (d) **supply chain**. perch routes every shell-out through one Runner
> (`internal/proc`) using `exec` argv — **no shell** — except two deliberate, audited seams
> (`worktree/hooks.go` `sh -c` for user hooks; `tmux/cleanup.go` `tmux run-shell`, all args
> `shellQuote`'d).
>
> **Scope:** this ledger covers the pre-M11 codebase (M0–M9). The M11 persistent-frame
> rebuild introduces NEW attack surface — self-re-exec bootstrap, `join/break/swap-pane`
> orchestration, and any perch-installed tmux key binding — which gets its own audit pass
> before the `v0.1.0` tag. M10 DoD is not "security forever," it is "every M0–M9 vector
> has a written verdict and every confirmed hole is fixed with an exploit-encoding test."
>
> Date: 2026-06-01 (M10). Method: one read-only investigator per vector group, primary-source
> code reading; every fix lands with a regression test that FAILS on the un-fixed code.
>
> **GUI-pivot pass (2026-06-02):** the new attack surface introduced by the Wails + WebKit2GTK
> + Svelte frontend (IPC binding, attach-pty, CSP/no-port, npm supply chain) is audited here as
> V14–V18. This fulfills the "new-surface pass before v0.1.0" commitment made in M10 scope.

## Verdict summary

| # | Vector | Severity | Verdict | Fix |
|---|--------|----------|---------|-----|
| V1 | `.perch.toml` hook RCE (`post_create`/`pre_remove` → `sh -c`, no trust gate) | **CRITICAL** | EXPLOITABLE | trust-on-first-use repo registry (`internal/trust`) + TUI gate |
| V6a | `@perch_session`/`@perch_pane_status` delimiter/newline injection desyncs `parsePanes` | **HIGH** | EXPLOITABLE | reject fields containing `\x1f`/`\n`/`\r`; bounded split |
| V6b | `@perch_session` not UUID-validated before use as session identity | MEDIUM | NEEDS-FIX | validate UUID before trusting in live-index |
| V3-A | `base_branch` from project config → unguarded positional in `git worktree add` | MEDIUM | NEEDS-FIX | ref-format validate + `--` separators |
| V2′ | project-level `worktree_dir` → arbitrary absolute placement | MEDIUM | NEEDS-FIX | project config: relative-only, contained under repo parent |
| V3-B | missing `--` before branch in `CleanupScript` `git branch -d` | LOW | NEEDS-FIX | add `--` |
| V7b | no size cap on state/window/transcript file reads (OOM DoS) | LOW | NEEDS-FIX | `io.LimitReader` cap |
| V7c | `windows/*.json` `Tree` not validated against scan root (resurrect) | LOW | NEEDS-FIX | reject out-of-root trees on load |
| V11 | `golang.org/x/sys@v0.38.0` GO-2026-5024 (Windows-only, unreachable) | LOW | NEEDS-ACTION | bump → v0.44.0 (verified 2026-04-23, 39d) |
| V5 | capture-pane escape injection into preview | INFO | **MITIGATED** | prod omits `-e`; lock with guard-test |
| V2 | `.perch.toml` agent-binary / `startup_command` injection | — | MITIGATED | global-only `Agents`; project `agent` validated to enum; `startup_command` dead |
| V4 | `[files]` copy/symlink traversal; worktree path flag-injection | — | MITIGATED | two-pass EvalSymlinks containment in `seed.go`; paths always absolute |
| V8 | discovery symlink/loop traversal | — | MITIGATED | `WalkDir` (no symlink follow) + `MaxDepth=8` |
| V9 | `perch setup` install (atomic/refuse-malformed/mode-preserve/UseNumber) | — | MITIGATED | all four properties verified + tested |
| V9b | embedded `perch-status.ts` shell injection | — | MITIGATED | only hardcoded enum flows to `$\`perch status set ${state}\`` |
| V9a | setup write path via `$HOME`/env | LOW | ACCEPTED | intentional (test sandbox via `t.Setenv HOME`); path is `Join(home,…)`; requires pre-owned env |
| V10 | `perch status set` sink | — | MITIGATED | enum-validated before any tmux write; empty `$TMUX_PANE`→exit 0; argv not shell |
| V12 | dependency/vendor integrity | — | CLEAN (1 note) | `go mod verify` ok; Charm v1 locked; `teatest` test-only untagged pseudo (see note) |
| V13 | build hygiene | — | CLEAN | `-trimpath`, version-only ldflags, no secrets, sane `.gitignore`/`.tool-versions` |
| V14 | WebKit2GTK rendering engine (dynamically linked; CVE patching via distro apt, not perch) | — | **ACCEPTED** | keep system library current via apt; documented operational dependency |
| V15 | script-message IPC / bound-method API (untrusted frontend → Go; session/path/ref/enum validation; argv-only tmux) | HIGH | **MITIGATED** | `validateSessionID` allowlist, `validateWorktreeUnderRoots` symlink-escape defeat, `CreateAgent` containment; TOCTOU residual = INFO (out of threat model) |
| V16 | attach-pty `WriteToPty` (keystroke bytes forwarded verbatim to user's own pty) | — | **ACCEPTED** | documented boundary; confers no privilege beyond the user's own |
| V17 | CSP + no listening port (restrictive `<meta>` CSP; no TCP port in production build) | — | **MITIGATED** | `connect-src 'self'`, no eval; `ws://` reload socket dev-tag-only; verified in production ELF |
| V18 | npm/frontend supply chain (exact-pinned, `package-lock.json` committed; 4 MODERATE Svelte advisories) | MEDIUM | **MITIGATED** | advisories non-reachable: no SSR, no `{@html}`, no `<svelte:element>`; re-evaluate if any are added |

## Confirmed holes — detail & fix

### V1 — `.perch.toml` hook RCE (CRITICAL)
`internal/worktree/hooks.go:32` runs each hook string verbatim: `r.RunInDir(ctx, treePath, "sh", "-c", script)`.
`post_create` (`tui/worktree_actions.go:257`) and `pre_remove` (`:106`) fire on the user's normal
worktree create/remove (`w`/`d`) against whatever `.perch.toml` `config.Load` finds by walking up
from the project dir. No trust gate, allowlist, or opt-in exists. (`pre_merge` was parsed but never
called; it has been removed from the config structs in M14-1 — §7.3 merge is plan-deferred.)

**Fix — trust-on-first-use (TOFU).** New `internal/trust` package backed by a JSON file in the
**global** state dir (never in-repo): `{ configPath → sha256(content) }`. Keyed on the **resolved
`.perch.toml` path `config.Load` actually used** (config is found by ancestor walk, so it may sit
above the project dir) plus its content hash, so an edit re-prompts. Before `RunHooks`, the TUI
checks trust; if untrusted/changed it opens a modal — **approve always** (persist hash) /
**approve once** (this run) / **deny** (skip hooks; worktree op still proceeds, hooks are not
mandatory). Default in non-interactive/test contexts = **DENY**. The already-hashed config bytes
are threaded to the gate (no re-read → no TOCTOU between check and exec). `worktree_actions.go` is
the only `RunHooks` caller and `AddWorktree` is TUI-only, so a TUI gate is complete coverage.
Exploit test: untrusted repo create ⇒ **zero `sh -c`** in `FakeRunner.Calls`.

### V6a — tmux option/format injection (HIGH)
`internal/tmux/tmux.go:18` packs panes with `\x1f` field + `\n` line delimiters; `parsePanes`
(`:129`) splits on them. `@perch_session`/`@perch_pane_status` are tmux user options any local
process can `set-option`. A value with an embedded `\n` injects a whole fake pane record; a value
with `\x1f` desyncs the field offsets (status read from the wrong column). **Fix:** after split,
reject any record whose trusted fields contain `\x1f`/`\n`/`\r`; use a bounded split so trailing
injected fields cannot shift offsets. Exploit test: `@perch_session` carrying `\x1f`+newline ⇒ the
fake record is dropped and field alignment holds.

### V6b — `@perch_session` identity not validated (MEDIUM)
`tui/data.go buildLiveIndex` trusts any non-empty `@perch_session` as a session id (first-write-
wins). A forged value equal to a real session UUID (readable from `~/.claude/projects/`) can
hijack the live mapping/preview. **Fix:** validate canonical UUID before admitting to the index.
Exploit test: non-UUID/forged `@perch_session` does not win the live-index.

### V3-A / V3-B / V2′ — git argv & path hardening (MEDIUM/LOW)
`git/worktree.go:94` passes `base` (= `cfg.BaseBranch` from project config) as the final positional
of `git worktree add -b <branch> <path> <base>` with no `--` and no validation → `base_branch =
"--upload-pack=…"`/`"--no-checkout"` is a flag-injection. **Fix:** validate branch/base against git
ref rules (no leading `-`, no `..`, no control chars/space, not empty) and add `--` before
user-controlled positionals in add/remove. `tmux/cleanup.go:83` `git branch -d <branch>` gets `--`
(defense-in-depth; branch is `perch/…` today). **V2′:** project-level `worktree_dir` may name any
absolute writable path; restrict project config to **relative** worktree dirs resolved under the
repo's parent (absolute paths only from global/user config). Exploit tests per case.

### V7b / V7c — state-store hardening (LOW)
`state.go` / `resurrect.go` `os.ReadFile` without a cap → OOM on a hostile large file; loaded
`Window.Tree` is used as a git working dir without checking it is under the scan root. **Fix:**
`io.LimitReader` (16 MiB) on state/window reads; on load, skip windows whose `Tree` is outside the
configured scan root.

### V5 — capture-pane (MITIGATED, locked)
Production capture is `capture-pane -t <target> -p` (`tmux.go:214`) — **no `-e`** — so tmux renders
its cell grid to plain text and control sequences (OSC 52/0/2/8, DCS/APC) never reach the user's
terminal. **Lock:** a guard-test asserts the capture argv never contains `-e`. If `-e` is ever
needed, a CSI-SGR-only sanitizer becomes mandatory before render.

## Supply chain

- **V11:** `golang.org/x/sys` `v0.38.0` → **v0.44.0** (proxy-verified: published 2026-04-23, 39 days
  old ≥30; v0.45.0 is 11 days — skipped). Vuln GO-2026-5024/CVE-2026-39824 is Windows-only and
  unreachable (no Windows build target; no source imports `x/sys/windows`; symbol never called) but
  the pin is cleared so vulncheck stays green. Re-run full `-race` suite after `go mod vendor`
  (x/sys underlies bubbletea input syscalls on Linux/macOS).
- **V12 — teatest note:** `github.com/charmbracelet/x/exp/teatest` is **test-only** (not in the
  shipped binary) and the module has **zero tagged releases** (pseudo-versions only); the current
  pin is 1 day old, which violates the ≥30-day rule but carries **no shipped supply-chain risk**.
  Disposition: keep, documented exception; revisit if a tagged release ages to ≥30 days. (Chasing an
  older pseudo-version risks API incompatibility with the locked bubbletea v1.3.10.)
- **V13:** clean — `-trimpath` on every build path, ldflags inject only `main.version`, no secrets in
  tracked files, `.gitignore`/`.tool-versions` well-formed, cross target Linux/macOS only.

## Resolution (M10 — all confirmed holes fixed on `feat/perch-v1`)

| Finding | Fix commit (subject) |
|---------|----------------------|
| V1 hook RCE | `feat(security): trust-on-first-use gate for .perch.toml hooks` |
| V6a/V6b tmux parse | `fix(security): harden tmux pane parsing against option-value injection` |
| V3-A/V3-B/V2′ git argv & path | `fix(security): validate git refs + relative-only project worktree_dir` |
| V7b/V7c state store | `fix(security): cap state-file reads + validate resurrect tree under roots` |
| V11 x/sys + V5 guard | `fix(security): bump golang.org/x/sys to v0.44.0; lock capture-pane -p (no -e)` |

Each fix ships an exploit-encoding regression test (asserted via `FakeRunner.Calls` /
hermetic FS). The trust gate was additionally adversarially reviewed (no untrusted-hook
execution path found). `make vulncheck` → "No vulnerabilities found"; `go mod verify` →
"all modules verified"; full `-race` + integration suite green on real git/tmux 3.6.

> **Note (go directive):** bumping `golang.org/x/sys` to v0.44.0 raised the module's `go`
> directive 1.24.2 → 1.25.0 (the dependency requires it); the toolchain was `go1.26.2`, bumped to `go1.26.4` to clear crypto/x509 CVEs.

## GUI Pivot — New Surface (V14–V18)

### V14 — WebKit2GTK rendering engine (ACCEPTED)
The GUI renders in the OS WebKit2GTK 4.0 webview, dynamically linked via cgo/pkg-config
(`libwebkit2gtk-4.0-dev`). No engine is bundled; CVEs are patched through the distro's apt security
feed, not perch's release cycle. **Verdict: ACCEPTED** — operational dependency, documented. Keep
the system library current via `apt upgrade`; perch cannot own the WebKit2GTK patch cadence.

### V15 — script-message IPC / bound-method API (MITIGATED, TOCTOU=INFO)
The untrusted Svelte frontend reaches Go only through bound methods over the WebKit2GTK
script-message channel (no HTTP). The IPC namespace (`window.go.app.App.<Method>`) was verified
against the vendored Wails binding generator. Every argument is validated before any tmux/git work
(which is always argv, never a shell):

- **`validateSessionID`** — charset allowlist `[A-Za-z0-9_-]`, length 1–128, byte-level (immune to
  Unicode-confusion); rejects shell metachars, control chars, path separators, traversal sequences.
- **`validateWorktreeUnderRoots`** — requires absolute + clean + existing path; `filepath.EvalSymlinks`
  on path AND roots; trailing-separator prefix check (defeats the `/root` vs `/root-evil` sibling
  trick); rejects symlink-escape by containment.
- **`CreateAgent` containment** — `containedUnderRoots` confines the derived (not-yet-existing)
  worktree path under a configured root, rejecting absolute or `..` `worktree_dir` config values.
  `branch` is `git.ValidRef`-validated (rejects leading `-` flag-injection); `tool` is an exhaustive
  enum (`claude`/`opencode`). **This mitigates the pre-existing V2′ finding for the GUI create path.**
- **`KillSession`/`OpenTerminal`** enforce a LIVE allowlist: the kill/attach target is derived from a
  matched live session's own tmux session/window — the frontend-supplied id never enters argv
  directly.

Backed by failing-first adversarial tests (`TestValidateSessionID_AdversarialCases`,
`TestValidateWorktreeUnderRoots_SymlinkEscape`, `TestApp_CreateAgent_ContainmentGuard`,
`TestApp_CreateAgent_EndToEnd`) and two independent offensive security reviews (validation
primitives; `CreateAgent`) that found no exploitable bypass.

Residual: a same-user TOCTOU gap exists between worktree-path validation and the later `git -C`
use. **NOT exploitable under the threat model** (local single OS user — such an attacker already
holds the user's privileges). TOCTOU = INFO, out of threat model. **Verdict: MITIGATED.**

### V16 — attach-pty `WriteToPty` (ACCEPTED)
Keystroke bytes from xterm.js are forwarded verbatim to the agent's pty (the user's own
shell/agent). This is the intended terminal channel; it confers no privilege beyond what the user
already holds, and the bytes reach a pty (not a shell parsed by perch). **Verdict: ACCEPTED** —
documented boundary.

### V17 — CSP + no listening port (MITIGATED)
`frontend/index.html` declares a restrictive Content-Security-Policy via `<meta http-equiv>`:

```
default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
img-src 'self' data:; connect-src 'self'; font-src 'self';
object-src 'none'; base-uri 'none'; frame-ancestors 'none'
```

No remote origins, no `eval`, no inline script. The production binary (built `go build -tags
production`) exposes **no listening TCP port**: IPC is the WebKit script-message channel, assets
are served via the `wails://` custom scheme, and the `ws://localhost:34115` reload socket is
compiled in only under `//go:build dev`. Verified: the production ELF has no `net.Listen` /
DevServer path outside the dev-tagged code. **Verdict: MITIGATED.**

### V18 — npm/frontend supply chain (MITIGATED, advisories non-reachable)
Frontend dependencies are exact-pinned (no `^`/`~`), `frontend/package-lock.json` is committed, and
`npm audit --audit-level=high` is clean (Task 17A gate). Residual: 4 MODERATE Svelte advisories:

| Advisory | Class | Reachable? |
|----------|-------|------------|
| GHSA-pr6f-5x2q-rwfp | SSR XSS | No — no SSR; client-only `mount()` |
| GHSA-f3cj-j4f6-wq85 | SSR XSS | No — no SSR |
| GHSA-rcqx-6q8c-2c42 | DOM-clobbering XSS via `{@html}` | No — grep of `frontend/src` finds zero `{@html}` |
| GHSA-9rmh-mm8f-r9h6 | `<svelte:element>` ReDoS | No — grep finds zero `<svelte:element>` |

All four are non-reachable. The pin is retained (fixes require `--force` past the pinned range and
would violate the ≥30-day-old-library rule). **Verdict: MITIGATED.** Re-evaluate and bump if perch
ever adds SSR, `{@html}`, or `<svelte:element>`.

## Follow-up (M11 surface — audit before tag)
Bootstrap self-re-exec argv; `join/break/swap-pane` target construction (argv-safe, session names
validated); any perch-installed tmux key binding (must NOT mutate the user's `~/.tmux.conf`/global
bindings — session-scoped or perch-owned key-table only; binding command must not interpolate
attacker-controlled session names unsafely).
