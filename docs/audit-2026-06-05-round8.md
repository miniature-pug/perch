# Round-8 Audit — 2026-06-05

**Branch:** `feat/perch-v1`
**Baseline:** `make test-all` GREEN at HEAD `77fdb9f` (the last round-7 commit); no code changed this round, so the gate state is unchanged.
**Method:** Scope-narrowed verification, NOT a fresh parity fan-out. The only unaudited surface since round 7 is round-7's own 6 commits (`d590b18..77fdb9f`, ~80 lines of production code, all already covered by anti-masking tests). Two read-only Sonnet agents, both held to a hard falsifiability bar.

---

## Headline

**Round-7 delta is clean. Zero actionable findings.** This is the expected convergence outcome — eight rounds in, the risk class is no longer "missed feature" but "self-inflicted regression in the most recent delta" + "agent false-positive re-litigating a settled call." Neither materialized. The remaining unverifiable surface (rendered glass/sheen pixels) is gate-blind and routed to the manual smoke checklist, where it has always belonged.

---

## Why this round was scope-narrowed (not a 9-agent sweep)

Round 7 was a 9-agent fan-out, correctly sized to its surface (the rounds 3–6 delta, `12789e2..HEAD`). Round 8's surface is *only* round-7's 6 commits. A 9-agent sweep over ~80 already-tested lines invites the one failure mode this codebase records repeatedly: agents manufacturing false-positives that re-litigate adjudicated decisions (round-7's dead-code grep false-positive; round-3's 3 falsified subagent claims). The right-sized check is **verification**, not rediscovery.

The round-7 delta splits cleanly:

- **Gate-verifiable** (`app.go` R7-1/R7-3, `DiffView` R7-5, `notify`/`listener`/`opencode_monitor` Go): already tested, already verified by anti-masking. A read pass confirms coherence; nothing more.
- **Gate-blind** (`glass.css` R7-7 shadow reset, `App.svelte` R7-2 dock-bg removal, `themes.css` sheen removal): no read-only agent can adjudicate rendered CSS any better than the test gate can. Re-auditing produces opinion, not findings → routed to smoke.

---

## Verification results

### Agent A — round-7 delta regression checks (grep/read-falsifiable)

| Check | Result | Evidence |
|-------|--------|----------|
| `--perch-glass-sheen` dangling ref (deleted in R7-9) | PASS | 0 matches in `frontend/` |
| New token `--perch-dur-attn-approval` consumed | PASS | `Sidebar.svelte:210` |
| New token `--perch-dur-attn-input` consumed | PASS | `Sidebar.svelte:215` |
| New token `--perch-attn-opacity-min` consumed | PASS | `Sidebar.svelte:227` |
| `COUNTUP_FALLBACK_MS` consumed | PASS | `actions.ts:1,30` |
| `AGENT_CLAUDE` consumed | PASS | `NewSessionDialog.svelte:3,57`, `App.svelte:30,711` |
| `AGENT_OPENCODE` consumed | PASS | `NewSessionDialog.svelte:3,58,73`, `App.svelte:30,717` |
| `DEFAULT_AGENT` consumed | PASS | `NewSessionDialog.svelte:3,15,22` |
| No bare `"127.0.0.1:0"` net.Listen literal | PASS | `listener.go:56`, `opencode_monitor.go:190` use `loopbackHost+":0"` |
| No bare agent-identifier string literals in prod frontend | PASS | 0 outside `constants.ts` + tests |
| No bare `380` in `actions.ts` | PASS | only a comment; const lives in `constants.ts:20` |
| Deleted testdata zero surviving reference | PASS | 0 refs; `testdata/opencode/` holds only `events.sse` |
| R7-1 case reads `Kind=="approval"` (not `"state"`) | PASS | `app.go:727`; monitors emit `Kind:"approval"` (`claude_monitor.go:105`, `opencode_monitor.go:424`) |

**DELTA CLEAN.**

### Agent B — breadth honor pass (hard bar: delta-regression OR new-evidence + falsifiable citation; exclusion set honored)

**CLEAN — no finding passed the hard bar.** Confirmed R7-1..R7-7 fixes coherent at source (including the 4 updated masking tests + the seam test's blocking-tier and OS-notify assertions). The two `loopbackHost` consts are per-package by design (a shared exported const would force a new package) — not a centralization gap.

**Routed to smoke (visual, not findings):**
- Glass frost rendering through the hub after the opaque dock wrapper was removed (R7-2 visual result).
- Absence of specular sheen on solid cards after the `--perch-glass-shadow` reset (R7-7 visual result).

---

## Disposition

- **Actions:** none. No code edit, no test change, no doc drift (round-7 already synced `ARCHITECTURE.md`, diagrams, `CONTRIBUTING.md`, `CHANGELOG.md`; nothing changed since).
- **Deferrals vs cut corners:** unchanged from round 7 — 0 cut corners; every spec promise implemented or carries a recorded deferral rationale.
- **Config centralization / magic numbers:** verified clean both ways — no escaped literals, and no newly-introduced unused tokens/constants.
- **Remaining honest ceiling:** WebKit + real-agent + attach-D-Bus + Playwright visual behavior, including R7-1's OS-notification path and R7-2/R7-7's glass rendering — **user-gated manual smoke** per `docs/superpowers/smoke-checklist.md`. An automated audit cannot substitute for it; that is the gate that breaks the find-new-things cycle, not another audit round.

**NOT pushed; merge to main is user-only.**
