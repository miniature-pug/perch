# M2 — Discovery + git (sub-plan)

Sub-plan for milestone **M2** of the perch v1 build. Parent: `2026-05-30-perch-v1-implementation.md`.
Spec refs: `plan.md` §5 (Discovery & scope), §6.3 (frecency), §7.1 (porcelain parse), §20.1 (Runner principle), §20.2 (git fixtures), §20.3 (discover/state test cases), §14 (layout), milestone item 2.

## Objective

Bounded repo scan + authoritative worktree enumeration + frecency-ordered project list,
exposed via a hidden `perch debug discover [path]` command. **No tmux needed.**

## DoD (milestone item 2)

A debug command lists projects + trees under a root — **fast, pruned, frecency-ordered**.
All M1 gates stay green (build, `make test -race`, fmt/vet/lint, vendor/verify, coverage).

## Architecture decision: the entry[0]-keyed pipeline

`git worktree list --porcelain` lists the **main worktree first, regardless of which
worktree the command is run from** (verified against real git output at fixture-gen time).
This is the linchpin of the design:

- **`internal/discover`** is a *candidate finder*. It walks the filesystem and returns the
  set of paths where a `.git` entry (dir **or** file) exists. It does NOT build final
  `model.Project`s and does NOT know about repo names or worktree topology.
- **`internal/git`** parses porcelain. `entry[0].Path` is the main checkout = the canonical
  project root; `Project.Name` derives from *that* path; `IsMain` = first entry.
- **`internal/discover` + integration** dedup projects by the canonical main-repo path.

Why this matters (resolves three things at once):
1. A linked worktree's `.git`-as-file lives *inside* the scan root. If discover emitted
   Projects directly it would list that worktree as a standalone project with the wrong
   name (the worktree handle, not the repo name). Keying by porcelain `entry[0]` collapses
   it back onto its owning repo automatically.
2. The §20.3 `.git`-as-file case "just works": porcelain run from a linked worktree still
   reports the real repo as entry[0].
3. Nested independent repos have different `entry[0]` paths → distinct Projects.

## Spec tension (resolved, documented per evidence rule)

§5 prose says "stop descending once a repo root is found"; §20.3 enumerates an explicit
test "nested repos → repo inside repo → outer found, **inner found as a separate project**".
An enumerated test with defined output is authoritative over prose. **Resolution: continue
descending after finding a repo, while always pruning heavy dirs (`node_modules`, `vendor`,
`.git`) and honoring max-depth.** Heavy-dir pruning + max-depth preserve the "fast"
requirement (dependency folders are the cost; ordinary source walks are cheap). Flag this
to the user at the M2 checkpoint — not a blocking question.

---

## Task M2-A — `internal/proc` (shared command Runner)

**Files:** `internal/proc/proc.go`, `internal/proc/proc_test.go`.

Per §20.1: every shell-out (git, tmux, claude, opencode) goes through **one** `Runner`
interface; unit tests use a `FakeRunner` that records every invocation and returns canned
stdout/stderr/exit-codes; the real `ExecRunner` is used only in integration tests and the
production binary. Build it now so M2's git package proves it before M3 (tmux) / M4 (agent).

**Package name is `proc`, NOT `exec`** — a package named `exec` collides with the stdlib
`os/exec` import inside the real runner.

Requirements:
- `type Runner interface { Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) }`.
  Context-first for cancellation/timeouts.
- `type ExecRunner struct{}` implementing `Runner` via `os/exec` + `exec.CommandContext`.
  Capture stdout and stderr separately (two buffers). Return the command's error verbatim
  (callers inspect `*exec.ExitError` for exit codes). Never panic.
- `type FakeRunner struct{ ... }` implementing `Runner`:
  - Records every call (name + args, in order) on an exported/queryable field so tests can
    assert exact invocations.
  - Returns canned results. Support keying a response by the full command line (e.g. a
    `map[string]FakeResult` where the key is `name + " " + strings.Join(args, " ")`) AND a
    default/fallback. A `FakeResult` carries `Stdout []byte`, `Stderr []byte`, `Err error`.
  - Defensive: an unmatched call with no default returns a clear error, not a panic.
- A small constructor for `FakeRunner` if it needs initialized maps.

Tests (`proc_test.go`, table-driven where natural):
- FakeRunner records calls in order; multiple calls accumulate.
- FakeRunner returns the canned stdout/stderr/err for a matched key.
- FakeRunner unmatched-call behavior (error, no panic).
- ExecRunner happy path against a trivial real binary that is guaranteed present
  (`/bin/echo` or `go env` — pick one that exists on the build host; gate behind a
  `testing.Short()` skip OR keep it a plain unit test if the binary is reliably present).
  Assert stdout captured. Keep this minimal; the heavy real-process coverage is git's job.

Self-explaining code. Comments only for non-obvious decisions (e.g. why two buffers, why
context-first). Cite §20.1.

---

## Task M2-B — `internal/git` (porcelain parser + worktree enumeration)

**Files:** `internal/git/git.go`, `internal/git/git_test.go`,
`testdata/git/worktree-list-porcelain-{multi,single,linked}.txt`.

### Fixtures (generate from REAL git, then sanitize)

Do NOT hand-write these — `git worktree list --porcelain` format drift would make the
tests lie. Generate against throwaway real repos in a temp dir, then sanitize absolute
paths to a stable placeholder root (e.g. `/repos/...`). Required three:
- `worktree-list-porcelain-multi.txt` — a project with **two** worktrees (main + 1 linked,
  or main + 2). Confirms entry[0] = main and linked entries follow.
- `worktree-list-porcelain-single.txt` — main checkout only.
- `worktree-list-porcelain-linked.txt` — output captured **while cwd is the linked
  worktree** (the `.git`-as-file case). This is the load-bearing fixture: it must STILL
  show the main repo as entry[0]. If real git does not behave this way, STOP and report —
  the whole design keys on it.

While generating, also capture (in a comment in the test or fixture header) what a `bare`
and a `detached` entry look like, and add at least one fixture or inline-string test
covering `bare`/`detached` so the parser is exercised on them.

### Types + parser

```go
// Worktree is the parsed form of one porcelain record. It captures every attribute
// git emits so later milestones (§7.2 locked-worktree removal) need not re-parse.
type Worktree struct {
    Path     string // absolute worktree path (porcelain "worktree" line)
    Head     string // commit SHA ("HEAD" line); empty for bare
    Branch   string // short branch name, refs/heads/ prefix stripped; empty if detached/bare
    Bare     bool   // "bare" attr
    Detached bool   // "detached" attr
    Locked   bool   // "locked" attr (value, if any, may follow)
    Prunable bool   // "prunable" attr
}
```

- `func ParsePorcelain(raw []byte) ([]Worktree, error)` — **pure**, no I/O. Records are
  separated by blank lines. Each record: a `worktree <path>` line then attribute lines
  (`HEAD <sha>`, `branch refs/heads/<name>`, `bare`, `detached`, `locked [reason]`,
  `prunable [reason]`). Strip `refs/heads/` to short branch name. Defensive: tolerate
  trailing/leading blank lines, unknown attribute lines (skip, don't error), CRLF; never
  panic on malformed input — return a clear error only for structurally broken input
  (e.g. an attribute line before any `worktree` line), otherwise skip-and-continue.
- `func ListWorktrees(ctx context.Context, r proc.Runner, repoRoot string) ([]Worktree, error)`
  — runs `git -C <repoRoot> worktree list --porcelain` via the Runner (so NO cwd field
  needed), then `ParsePorcelain`. Wrap errors with context.
- Mapping helper to `model.Tree`:
  `func ToTrees(wts []Worktree, project *model.Project) []model.Tree` — `IsMain` = first
  non-bare entry (index 0 in normal repos); `Branch` from `Worktree.Branch`; `Path` from
  `Worktree.Path`; `Project` pointer set. Bare repos contribute no working tree (skip the
  bare entry or mark appropriately — bare has no checkout). Document the IsMain rule.
- Helper to derive the canonical project root + name from a worktree set:
  `func MainWorktree(wts []Worktree) (Worktree, bool)` returning entry[0] (the first
  non-bare worktree), or ok=false for a bare-only repo. `Project.Name` = `filepath.Base`
  of that path. (Integration uses this to build/dedup Projects.)

Tests (`git_test.go`, table-driven; read fixtures via `testdata/`):
- Parse multi: N worktrees, correct paths/branches, entry[0] is main.
- Parse single: one worktree, IsMain.
- Parse linked fixture: entry[0] is the **main repo**, not the linked dir (the design
  linchpin — assert explicitly).
- Parse bare: `Bare` true, empty branch, contributes no Tree.
- Parse detached: `Detached` true, empty branch.
- Parse malformed: attribute-before-worktree → error, no panic; blank/garbage lines
  tolerated.
- `branch refs/heads/feature` → `Branch == "feature"`.
- `ListWorktrees` with a `proc.FakeRunner` returning a fixture: asserts the exact git
  invocation (`git -C <root> worktree list --porcelain`) AND the parsed result. No real
  git in unit tests.
- `ToTrees`: IsMain mapping, bare skipped, Project pointer wired.

---

## Task M2-C — `internal/discover` (bounded candidate scan)

**Files:** `internal/discover/discover.go`, `internal/discover/discover_test.go`.
Test fixtures are built in-test with `t.TempDir()` (filesystem trees), NOT committed
testdata.

`discover` is the candidate finder. It returns paths containing a `.git` entry; it does
not build Projects (that's integration, via git entry[0]).

```go
type Options struct {
    MaxDepth int      // max directory depth below root to descend; root itself = depth 0
    Prune    []string // directory base names to never descend (defaults below)
}
// DefaultPrune = []string{"node_modules", "vendor", ".git"}
```

- `func Scan(root string, opts Options) ([]string, error)` — returns absolute paths of
  directories that are git repo working dirs (i.e. contain a `.git` dir **or** `.git`
  file). Behavior:
  - `.git` detection: a child named `.git` that is a directory (normal repo) OR a regular
    file (linked worktree / submodule) marks the containing dir as a candidate.
  - **Continue descending after a hit** (to find nested independent repos — §20.3 nested
    case is authoritative over §5 prose). Still prune `Prune` dirs and `.git` internals
    everywhere.
  - Max depth: root = depth 0. A repo whose `.git` sits at depth > MaxDepth is NOT found.
    Pin the off-by-one explicitly against the §20.3 case: "repo at depth 3, limit 2 → not
    found". (Interpretation: a `.git` located 3 levels below root is excluded when
    MaxDepth=2. Encode and test the exact boundary; document the chosen convention.)
  - **Do not follow symlinks** (`filepath.WalkDir` does not follow dir symlinks by default
    — confirm and rely on it; avoids cycle hangs). Document.
  - **Skip-and-continue on errors**: a permission/read error on one subtree must not abort
    the whole scan. Use the WalkDir error callback to return `fs.SkipDir` (or nil) for the
    offending entry and keep going. Never panic. Never fail the whole scan for one bad dir.
  - Deterministic output ordering is not required from Scan (integration sorts), but stable
    is nice; WalkDir visits lexically.

Tests (`discover_test.go` — §20.3 discover cases, all via `t.TempDir()`):
- **Max depth**: repo at depth 3, limit 2 → not found; same repo, limit 3 → found
  (pins the boundary both ways).
- **Prune node_modules**: `node_modules/.git` present → `node_modules` never descended,
  that inner repo not reported.
- **Prune vendor**: same.
- **`.git` as file**: a dir with a `.git` *file* (linked-worktree shape) → detected as a
  candidate.
- **Non-git dir**: a directory tree with no `.git` anywhere → empty result, no crash.
  (Note: this is the *scan* path — it finds zero git repos. The escape-hatch "run a
  session in a non-git dir" is a separate, later code path; do not conflate.)
- **Nested repos**: repo inside a subdir of another repo → BOTH candidate paths reported
  (outer and inner), inner not swallowed by the outer hit.
- **Defensive**: an unreadable subdir (chmod 000, skip on platforms where unsupported)
  does not abort the scan — repos elsewhere still found. (Guard with a skip if the test
  cannot drop perms in the CI environment.)

---

## Task M2-D — frecency ordering + `perch debug discover`

**Files:** `cmd/perch/main.go` (add hidden `debug` verb), `cmd/perch/main_test.go`
(routing test), and a small integration/ordering helper — placed in `internal/discover`
(it composes discover + git + state) as e.g. `discover.Projects(...)`. Reuse
`state.SortedPaths`; **no new ordering algorithm** (verified: all-zero ranks → score 0 →
alphabetical tiebreak = cold-start, exactly §20.3 "cold start → alphabetical").

### Ordering (reuse, do not rebuild)

To order discovered project paths by frecency with alphabetical fallback:
1. Take the persisted frecency stats map (empty map if no persistence loader exists yet
   in `state` — check; M1-D may have shipped the algorithm only). **Copy** it.
2. Zero-fill: for every discovered project path not already a key, insert
   `ProjectStat{Rank: 0}` into the **copy**.
3. Call `state.SortedPaths(copy, now)`. All-zero → alphabetical (cold start); mixed →
   scored first, statless fall to alphabetical among themselves.
4. **Do not persist the zero-filled copy** — debug is read-only.

Where `now` comes from: `Date.now()`/`time.Now()` are fine in production code (this is the
real binary, not a workflow script). Use `time.Now().Unix()`.

### Build the canonical project list

`func Projects(ctx context.Context, r proc.Runner, root string, opts Options, now int64) ([]ProjectWithTrees, error)`
(name/shape at implementer's discretion; keep it cohesive):
1. `Scan(root, opts)` → candidate paths.
2. For each candidate, `git.ListWorktrees(ctx, r, candidate)`; derive the canonical main
   path via `git.MainWorktree`. **Dedup** projects by canonical main path (a linked
   worktree and its main checkout collapse to one Project). Skip-and-continue if git fails
   on a candidate (log/skip, don't abort) — defensive.
3. Build `model.Project{Path: main, Name: base(main), IsGit: true}` and its
   `[]model.Tree` via `git.ToTrees`.
4. Order the Projects by the frecency procedure above (cold-start alphabetical).
5. Return projects each with their trees, in frecency order.

### `perch debug discover [path]` (hidden verb)

- Add a `case "debug":` to the `run` dispatch in `main.go`. `debug` is a **hidden**
  parent verb (NOT added to `printUsage`; it is a dev/diagnostic surface, distinct from the
  locked 7-verb public CLI). Sub-dispatch: `debug discover [path]` → run the scan;
  unknown `debug` subcommand → usage to stderr, exit 2.
- `path` defaults to cwd (matches §5: "Root = the directory perch is launched in"),
  overridable by the positional arg. Validate it is an existing directory (reuse the
  `os.Stat` + `IsDir` pattern already in `handlePathArg`).
- Use a default `Options` (sensible `MaxDepth` — pick a documented default, e.g. 8 or
  per any config field; `DefaultPrune`). Use `proc.ExecRunner{}` and real git.
- Output: human-readable list — each project (name + path), then its trees indented
  (branch + path, mark the main checkout). Keep it greppable and simple; this is a debug
  surface, not the TUI.
- It must degrade gracefully: a root with zero git repos prints an empty/"no projects
  found" message and exits 0; git/permission errors on individual candidates are skipped,
  not fatal.

Tests:
- `discover.Projects` (or equivalent) with a `proc.FakeRunner` + a `t.TempDir()` tree:
  dedup of a main+linked pair into one Project; cold-start alphabetical ordering across
  multiple repos; trees attached correctly. No real git.
- Ordering: explicit cold-start test (multiple repos, empty stats → alphabetical) and a
  mixed test (one repo with a stat outranks the rest).
- `main_test.go`: `perch debug discover <tempdir>` routes and exits 0; unknown
  `debug <x>` → exit 2 + usage on stderr; `debug` is absent from `printUsage` output.

---

## Cross-cutting (every task)

- **Defensive-by-contract**: never panic; distinguish `os.ErrNotExist`/`fs.ErrNotExist`
  from real errors; skip-and-continue; degrade gracefully.
- **No surprise I/O**: all git shell-outs go through `proc.Runner`; unit tests use
  `proc.FakeRunner`; real git only in the binary and any integration-tagged tests.
- **Comments** only to explain a decision or non-obvious logic. Self-explaining names.
- **Per-task gates** (the implementer runs before reporting DONE): `make fmt`, `go vet`,
  `golangci-lint run`, `make test` (`-race`), and `go mod verify`. New code keeps
  package coverage healthy.
- **Two-gate review** per task: spec-compliance review THEN code-quality review; fix loops
  until both approve before marking the task complete.

## Closeout (after M2-D approved)

- Update the master plan (`2026-05-30-perch-v1-implementation.md`): check the M2 boxes,
  append a `✅ DONE` + verification note (coverage numbers, gates green, the §5/§20.3
  tension resolution).
- Update `.tool-versions`/docs only if anything changed (no new deps expected — `proc`,
  `git`, `discover` are stdlib-only; `BurntSushi/toml` remains the sole dependency).
- Run `make vendor` only if go.mod changed (it should not).
- Checkpoint with the user before M3. Note the §5-vs-§20.3 resolution in one line.
