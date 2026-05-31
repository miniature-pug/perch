# M3 — Adapters: claude + opencode

**Status:** ✅ DONE (commit chain `6ccb06a`…`d06d21b`; agent 94.0% / proc 100% coverage; all gates green)
**Milestone (master plan):** M3 — Adapters: claude + opencode *(no tmux needed)*
**Package:** `internal/agent/` (+ a small `internal/proc` extension)
**Spec refs:** `plan.md §4`, `§16.3`, `§18.2/3/4`, `§20.1`, `§20.3 agent`.

> **This document is the authoritative spec for M3.** It supersedes the master
> plan wherever they conflict. Every divergence from the master plan below is
> backed by empirical evidence captured on this machine (claude-code 2.1.158,
> opencode 1.15.12) and is tagged **[DIVERGENCE]** with its one-line proof. The
> master plan will be corrected at closeout (see *Closeout*). Spec reviewers must
> review against THIS document, not the master plan.

---

## Objective

An `Adapter` abstraction with two implementations (claude, opencode) that:
- enumerate existing agent sessions as `[]model.Session` (fault-tolerant), and
- build launch arg slices for resume / new / fork.

No tmux, no real processes in unit tests, no TUI. Discovery + arg-building only.

---

## Evidence base (captured this session — do not re-assume)

### Claude on-disk layout
- Transcripts: `<claudeHome>/projects/<dir-slug>/<session-uuid>.jsonl`.
- `<dir-slug>` = absolute project path with every `/` → `-` (leading `/` → leading
  `-`). `_` and literal `-` in path components are **preserved**, so the slug is
  lossy: `/home/u/claude-kb` and `/home/u/claude/kb` both slugify identically.
- Each session uuid MAY have a sibling data dir `<slug>/<uuid>/` (contains
  `subagents/agent-*.jsonl`). **Enumerate only top-level `*.jsonl`; never descend
  into the `<uuid>/` data dirs or treat `agent-*.jsonl` as sessions.**
- JSONL records carry `sessionId` on (nearly) every line; substantive records
  (`user`/`assistant`/`system`) also carry `cwd` (absolute) and `gitBranch`.
  Control records (`mode`, `ai-title`, `last-prompt`, `bridge-session`) carry only
  light fields.
- **[DIVERGENCE — title]** The master plan says "parse first line for id, last
  line for title/summary." Reality: there is **no `summary` record type** in any
  real transcript; the first line is an unstable control record (`mode` /
  `last-prompt` / `bridge-session`), and the last line is just the most recent
  event. **Title comes from the `ai-title` record's `aiTitle` field** (rewritten
  repeatedly — take the **last** occurrence). In 4/10 real files `ai-title` is
  absent → fallback to the **first non-meta user message** (skip records with
  `isMeta:true` and skip user text wrapped in `<...>` synthetic tags such as
  `<command-name>`, `<local-command-stdout>`).
- `timestamp` fields are ISO-8601 strings; `Updated` for a session uses the
  transcript file's **mtime** (unix seconds), per master plan.
- Pid tracker: `<claudeHome>/sessions/<pid>.json`, e.g.
  `{"pid":…, "sessionId":…, "cwd":"/abs", "status":"idle|busy", "startedAt":<ms>,
  "updatedAt":<ms>, …}`. **[DIVERGENCE — fields]** It has many more fields than the
  master plan's `{sessionId,cwd,status}`; timestamps are unix **ms**; `status` ∈
  {idle, busy}. Treat as undocumented/version-fragile: parse defensively, skip any
  file that doesn't yield `{sessionId, cwd}`.

### Claude CLI flags (from `claude --help`, 2.1.158)
- Resume: `--resume [id]` (alias `-r`); id value is optional (bare = picker).
- Continue most-recent: `--continue` (alias `-c`).
- **[DIVERGENCE — fork]** Fork is the native flag **`--fork-session`** ("when
  resuming, create a new session ID instead of reusing the original; use with
  `--resume`/`--continue`"). The master plan's "copy `.jsonl` + data dir then
  `--resume`" (§18.2) is **superseded** — claude forks natively with no file copy.
- New: bare `claude`; prompt is a positional; `--print`/`-p`; `--model <m>`;
  `--session-id <uuid>` (must be a valid UUID); `--worktree`/`-w`.
- No `--cwd`. Session↔dir binding is via the process working directory at launch.

### opencode CLI (v1.15.12)
- List: `opencode session list --format json`.
- **[DIVERGENCE — no --roots]** There is **no `--roots` flag** (and no
  `{roots:true}`). `session list` flags are only: `-n/--max-count`, `--format
  table|json`, `--print-logs`, `--log-level`, `--pure`. The master plan's
  `{roots:true}` does not exist.
- **[DIVERGENCE — JSON shape]** `--format json` emits a **JSON array** of objects
  with flat fields `{id, title, updated, created, projectId, directory}`. `id` is
  `ses_`-prefixed. `created`/`updated` are flat **unix-ms numbers** (NOT
  `time_created`/nested/ISO; those are DB-only names). `projectId` is camelCase.
  Captured real bytes (the single session on this machine):
  ```json
  [
    {
      "id": "ses_18593fc84ffeg4oyInzAG2eLOL",
      "title": "Greeting",
      "updated": 1780170389857,
      "created": 1780170359675,
      "projectId": "global",
      "directory": "/home/miniature_pug"
    }
  ]
  ```
- **[DIVERGENCE — empty output]** An empty scope prints **empty stdout (zero
  bytes), not `[]`**, exit 0. The parser must treat empty/whitespace-only stdout
  AND a literal `[]` as zero-sessions-no-error.
- **[DIVERGENCE — scope, contradicts §18.3]** `session list` is **project-scoped**
  (project = the registered worktree root that cwd resolves to), **NOT global**.
  No flag broadens it; the only scoping lever is the process cwd. Reading the
  SQLite DB directly is forbidden (§4). Therefore enumerating sessions for a given
  directory means running the CLI **with that directory as cwd** → the reason
  `internal/proc` needs a cwd-aware run (M3-A). Cross-directory enumeration (loop
  over trees) is the M5 caller's job; M3 provides the single-directory primitive.
- Resume (interactive): top-level **`opencode --session <id>`** (alias `-s`), or
  `--continue`/`-c`. **[DIVERGENCE — resume form]** The master plan's `opencode run
  --session <id> "<prompt>"` is the **one-shot non-interactive** form; perch
  launches **interactive** agent panes, so resume uses the top-level TUI form, not
  `run`.
- Fork: `--fork` exists (top-level and on `run`, requires `--session`/`--continue`).
  **v1 scope decision (honor master plan):** opencode `ForkInto` returns an
  *unsupported* error — perch v1 starts a fresh session in the worktree. Document
  that `--fork` exists and enabling it is a small, deliberate post-v1 change, not a
  technical limitation.
- New: bare `opencode` (TUI); optional `[project]` positional path; `--model`/`-m`
  (`provider/model` format); `--agent`; `--prompt`.

### Reused existing code (do NOT recreate)
- `internal/model`: `Tool` (`ToolClaude`/`ToolOpencode`), and **`model.Session`**
  (`ID, Tool, Directory, Title string/Tool, Updated int64` unix seconds). Its
  doc-comment already states it is "populated by the adapters in internal/agent."
  **[DIVERGENCE — Session type]** Adapters return `[]model.Session`; the master
  plan §4's separate `agent.Session` (`Tool string`, `Updated time.Time`) is a
  pre-model sketch and is NOT created. Single source of truth, consistent with M2
  reusing `model.Project`/`model.Tree`.
- `internal/proc`: `Runner`, `ExecRunner`, `FakeRunner` (+ `Respond`). All
  shell-outs go through `Runner` (§20.1); no real processes in unit tests.
- `internal/config`: agent-binary resolution is global-only (`Agents map`);
  `.perch.toml` cannot override it (§8.2). M3 adapters take an injected binary
  path / lookup; they do not read project config.

---

## Tasks

### M3-A — `internal/proc`: cwd-aware run

**Why:** opencode session scope is set *only* by the process cwd (evidence above);
the `Runner` must be able to run a command in a chosen directory. git did not need
this (`git -C <root>`), opencode does.

**Do:**
1. Widen the `Runner` interface with:
   ```go
   RunInDir(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error)
   ```
   Keep `Run` in the interface; make `Run(ctx, name, args...)` delegate to
   `RunInDir(ctx, "", name, args...)`. `dir == ""` means "inherit the parent
   process cwd" (current behavior — unchanged for all existing git callers).
2. `ExecRunner.RunInDir`: set `cmd.Dir = dir` (only when non-empty). `ExecRunner`
   keeps the zero-value-usable contract.
3. `FakeRunner`:
   - Add `Dir string` to `Call` so tests can assert the cwd that was passed.
   - `RunInDir` records `Dir` and looks up the same canned response as `Run`
     (response keys remain name+args; cwd does not participate in the key — tests
     assert cwd via the recorded `Call`, not via response routing).
   - `Run` records `Dir: ""`.

**Tests (proc_test.go — extend, don't rewrite):**
- `RunInDir` records `Call.Dir`; `Run` records empty `Dir`.
- `Run` still delegates correctly (existing tests stay green unchanged).
- One `ExecRunner.RunInDir` real-process test: run a cwd-revealing command (e.g.
  `pwd` / `sh -c pwd`) in `t.TempDir()` and assert stdout is that dir. (This is the
  ExecRunner real-binary exception already used by the existing happy-path test.)

**Gates:** `make test` (race), `make lint`, `go vet`. Coverage on `internal/proc`
stays ≥ its current level (was 100%).

---

### M3-B — `internal/agent/adapter.go`: interface + types

**Do:**
- Define:
  ```go
  type Adapter interface {
      Name() string                                            // "claude" | "opencode"
      Detect() bool                                            // CLI resolvable?
      ListSessions(ctx context.Context) ([]model.Session, error)
      ResumeArgs(sessionID string) []string                    // interactive resume in cwd
      ForkInto(sessionID, targetDir string) ([]string, error)  // launch args; err if unsupported
      NewArgs(opts NewOpts) []string                           // fresh interactive session
  }

  type NewOpts struct {
      Model     string // claude --model / opencode -m (provider/model)
      Prompt    string // claude positional / opencode --prompt
      Agent     string // opencode --agent (claude ignores)
      SessionID string // claude --session-id (UUID); opencode ignores
  }
  ```
- Package doc comment: state the fault-tolerance contract (§4): adapters never
  panic, skip unparseable records, surface partial results; an adapter failure
  degrades to "that tool unavailable," never crashes perch.
- **[SCOPE — deferred, documented]** `InstallStatusHook`, `ReadyHeuristic`,
  `TrustPrompt` from master-plan §4 are **NOT** on the M3 interface: they have no
  honest implementation until the setup/TUI milestones (status hooks = §9/setup,
  M8). Adding them now forces a lying stub. Document the deferral in the interface
  doc comment so the spec reviewer sees it is intentional, not under-building.

**Tests:** none yet (no behavior); compile-time interface assertions live with the
implementations (M3-C/D).

---

### M3-C — `internal/agent/claude.go`

**Struct & constructor (dependencies injected for testability — no real FS/PATH in
unit tests):**
```go
type Claude struct {
    Home     string                  // <claudeHome>; default $CLAUDE_CONFIG_DIR or ~/.claude
    Bin      string                  // resolved claude binary (default "claude")
    Exists   func(path string) bool  // dir-exists probe for slug decode; default os.Stat-based
    LookPath func(string) (string, error) // default exec.LookPath, for Detect
}
var _ Adapter = Claude{} // compile-time interface guarantee
```
> Verify during impl: the production claude config dir env var name (likely
> `CLAUDE_CONFIG_DIR`) against `claude --help`/docs before defaulting `Home`.

**Slug decode — `decodeSlug(slug string, exists func(string) bool) string`** (pure
given `exists`):
- Algorithm: strip leading `-`; split remainder on `-` into tokens; greedily build
  the path from the left, at each position consuming the **longest** run of tokens
  (re-joined with `-`) that forms an existing directory; advance and repeat. If all
  tokens are consumed into existing dirs → return that path. If stuck at token `i`
  (no existing component starts there) → return the longest existing prefix joined
  with the remaining tokens via `/` (naive split).
- Satisfies all three §20.3 tiers + the clean case:
  - `-home-user-myproject` → `/home/user/myproject` (each token its own dir).
  - `-home-user-foo-bar`: tries `/home/user/foo-bar` first (merged, longest) → if it
    exists, returns it; else `/home/user/foo/bar`; else longest existing prefix
    (`/home/user`) + `/foo/bar`.
- **Known limitation (document in code, do NOT gold-plate):** greedy / no
  backtracking can over-merge when an unrelated `a-b` dir coincidentally exists
  alongside the intended `a/b/c`. Inherent to the lossy slug; degrade is graceful;
  the §20.3 enumerated cases are the spec.

**JSONL transcript parse — `parseTranscript(r io.Reader) (id, title string, err error)`**
(or equivalent; pure over a reader so tests feed fixtures directly):
- `id`: the session uuid is the **filename stem** (authoritative); the parser may
  also read `sessionId` but the filename is the key. (So `id` is supplied by the
  enumerator, not derived from line 1.)
- `title`: scan all records; take the **last** `ai-title` record's `aiTitle`. If
  none, fallback to the **first non-meta user message** text (skip `isMeta:true`
  and `<...>`-wrapped synthetic content). If neither → empty title (not an error).
- **Skip malformed JSONL lines** (log/count, continue); never panic. Empty file →
  no records → zero sessions for that file (per §20.3).
- Capture the first record `cwd` (if any) for Directory resolution.

**Pid-tracker parse — `readPidTrackers(home string) map[string]string`** (sessionId
→ cwd), fault-tolerant: read `<home>/sessions/*.json`, skip unreadable/malformed,
require `{sessionId, cwd}`.

**`ListSessions(ctx)`:**
- Enumerate `<Home>/projects/*/` slug dirs; within each, enumerate top-level
  `*.jsonl` (skip `<uuid>/` data dirs).
- For each transcript: `id` = filename stem; parse title + first-cwd; `Updated` =
  file mtime (unix seconds); `Tool = ToolClaude`.
- **Directory resolution precedence** (most → least authoritative):
  1. first `cwd` found in the transcript records (in-band, exact);
  2. pid-tracker cwd for this `id` (exact, for live sessions);
  3. `decodeSlug(slugDirName, c.Exists)` (fallback).
- Skip files that yield no records (empty); skip-and-continue on per-file errors;
  return partial list, never abort the whole listing for one bad file.

**Arg builders (pure):**
- `ResumeArgs(id)` → `["--resume", id]`.
- `ForkInto(id, targetDir)` → `["--resume", id, "--fork-session"], nil` (pure; the
  launch layer sets cwd = targetDir; no file copy — `--fork-session` is native).
- `NewArgs(opts)`: `[]` for bare interactive; append `--model <m>` if set,
  `--session-id <uuid>` if set; if `opts.Prompt != ""` append it as the trailing
  positional; `--print` only if a non-interactive opt is later added (omit for v1).
- `Detect()` → `LookPath(Bin)` succeeds.
- `Name()` → `"claude"`.

**Fixtures (`internal/agent/testdata/claude/`, derived from real records,
minimal/sanitized):**
- `projects/-home-user-myproject/<uuid>.jsonl` — a few real-shaped records incl. an
  `ai-title` record → title test.
- a transcript with **no** `ai-title` but a real user message → fallback-title test.
- a transcript whose **line 3 is invalid JSON** → malformed-skip test (lines 1,2,4
  parsed).
- an **empty** `.jsonl` → zero-sessions test.
- `sessions/<pid>.json` — a real-shaped pid record → pid-tracker cwd test; plus a
  malformed one → skip test.

**Tests (claude_test.go):**
- §20.3: slug decode clean; slug decode ambiguous (all three tiers, via fake
  `exists` sets); JSONL malformed-line skip; JSONL empty file.
- title = last `ai-title`.aiTitle; title fallback = first non-meta user message
  (covers the ~40% of real files with no `ai-title`); meta/`<...>` skip.
- Directory precedence: transcript-cwd wins over slug; pid-tracker cwd used when no
  transcript cwd; slug decode used when neither.
- arg builders: ResumeArgs, ForkInto (`--fork-session`, nil err), NewArgs variants.
- Detect via injected LookPath (no real PATH).
- `Updated` == file mtime.

---

### M3-D — `internal/agent/opencode.go`

**Struct & constructor:**
```go
type Opencode struct {
    Runner   proc.Runner
    Bin      string                       // default "opencode"
    Dir      string                       // cwd to scope the listing (per-tree; set by caller)
    LookPath func(string) (string, error) // default exec.LookPath
}
var _ Adapter = Opencode{} // compile-time interface guarantee
```

**Parse — `parseSessionList(raw []byte) ([]model.Session, error)`** (pure):
- Trim; if empty/whitespace **or** `[]` → return `nil, nil` (zero sessions, no
  error). (Empty scope emits zero bytes, not `[]` — handle both.)
- Else `json.Unmarshal` into `[]struct{ ID, Title, Directory string; Created,
  Updated int64; ProjectID string }` with the exact field/JSON names
  `id/title/directory/created/updated/projectId`.
- Malformed JSON → return the error (no panic).
- Map to `model.Session`: `ID`, `Title`, `Directory`, `Tool = ToolOpencode`,
  `Updated = updated_ms / 1000` (ms → unix seconds; model.Session is seconds).

**`ListSessions(ctx)`:** `RunInDir(ctx, o.Dir, o.Bin, "session", "list", "--format",
"json")` → `parseSessionList(stdout)`. A non-zero exit / runner error → return error
(adapter-unavailable; never panic). `Detect()` → `LookPath(Bin)`.

**`GroupByDirectory([]model.Session) map[string][]model.Session`** — small pure
helper (the master plan's "group by directory").

**Arg builders (pure):**
- `ResumeArgs(id)` → `["--session", id]` (top-level interactive TUI resume).
- `NewArgs(opts)`: `[]` bare TUI; append `--model <m>`, `--agent <a>`, `--prompt
  <p>` when set.
- `ForkInto(_, _)` → `nil, error` (unsupported in v1; documented — `--fork` exists,
  deferred by scope).
- `Name()` → `"opencode"`.

**Fixtures (`internal/agent/testdata/opencode/`):**
- `session-list.json` — real shape; **multi-session** (≥2 sessions across ≥2
  `directory` values) for the grouping test. Shape is the captured real bytes; the
  second entry is extended from that real shape (note in a test comment that the
  single live session was the capture source).
- `session-list-empty` — a literal **empty file** (zero bytes) — the real
  empty-scope output.
- `session-list-malformed.json` — `{broken`.

**Tests (opencode_test.go):**
- §20.3: valid fixture → N sessions with correct ID/dir/updated; empty (both the
  zero-byte file AND a `[]` literal) → zero sessions, no error; malformed → error,
  no panic.
- ms → s conversion asserted against a known value (guard the 1000× off-by).
- **`RunInDir` is passed `o.Dir`** — assert `FakeRunner.Calls[0].Dir == o.Dir`
  (this is the whole reason M3-A exists; parser tests pass regardless of cwd).
- `GroupByDirectory` groups correctly.
- arg builders: ResumeArgs (`--session`), NewArgs variants, ForkInto returns error.
- Detect via injected LookPath.

---

## Per-task gate (every task)

`make test` (`-race`), `make lint` (golangci-lint v2.11.4 via `go run …@pinned`),
`go vet`, `go build`. Coverage on `internal/` (excl. `internal/tui/`) ≥ 80% (§20.1);
target ≥ 90% on the new files. No real processes in unit tests except the existing
`ExecRunner` real-binary exception (M3-A `pwd` test only).

## Two-gate review (per task, per subagent-driven-development)

1. **Spec-compliance review against THIS document** (not the master plan). The
   reviewer is told: divergences from the master plan tagged **[DIVERGENCE]** here
   are correct-by-evidence; do not flag them as failures.
2. **Code-quality review** (only after spec ✅).

Fix-loop until both ✅ before the next task.

## DoD (master plan §16.3)

Unit tests parse real fixtures; sessions group by directory; §0 gate
(build/test/lint/vet) green; whole-M3 final review ✅.

## Closeout (after all tasks + final review)

1. Update the master plan (`2026-05-30-perch-v1-implementation.md`): mark M3 DONE
   with the commit chain + coverage, and **correct the falsified claims** so they
   do not mislead M5/M6/M8:
   - **§18.3**: opencode `session list` is **project-scoped**, not global (it is
     currently marked "confirmed global"). Note the M5 implication: enumeration is a
     **per-directory CLI loop** using `proc.RunInDir` (DB-read stays forbidden).
   - **§18.2**: claude fork is native **`--fork-session`** — no file copy. M6
     fork-into-worktree = launch `--resume <id> --fork-session` with cwd = target.
   - §4 Session type → `model.Session`; opencode no `--roots`; opencode JSON field
     names + ms timestamps; claude title source = `ai-title`.aiTitle; opencode
     resume = top-level `--session` (interactive); opencode `--fork` exists but is
     a deliberate v1 deferral.
2. Update `.tool-versions` / Makefile / docs only if M3 touched them (it should not
   add deps — stdlib `encoding/json` + existing `BurntSushi/toml` only).
3. Note any deferred items (InstallStatusHook/ReadyHeuristic/TrustPrompt → setup/
   TUI milestones; opencode `--fork` → post-v1).
4. Milestone boundary → pause for `/compact`.
