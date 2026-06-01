# M11-1 — `:` Command Bar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a vim-style `:` command bar to the perch TUI — a focused text-input overlay (separate from the `/` list filter) that dispatches `:q`, `:new`, `:attach <q>`, `:proj <q>`, `:setup [--replace]`, `:doctor`, `:resurrect`, `:help`.

**Architecture:** A pure parser (`parseCommand`) turns the typed line into a `cmdSpec`; a thin `dispatchCommand` performs side effects. `:attach`/`:proj` resolve a fuzzy match against the loaded list rows and reuse the existing Enter activation path (no duplicated frame/attach branching). `:setup`/`:doctor`/`:resurrect` suspend the TUI via `tea.ExecProcess` and run the running perch binary; `:resurrect` is **gated to refuse inside the persistent frame** (its reconcile was audited against normal topology, not the parked-placeholder-in-home frame, and ExecProcess-pause would staleify `displayedPaneID`).

**Tech Stack:** Go, bubbletea v1.3.10, bubbles v1.0.0 (`textinput`), `github.com/sahilm/fuzzy` (already a dep).

---

## Design decisions (locked)

- **Parse boundary is pure and exhaustively tested.** `parseCommand(string) cmdSpec` imports only `strings`. Bad args / unknown verbs return a `parseErr` string → surfaced as a toast, no action taken.
- **`:attach` = `:proj` + activate.** `:proj <q>` resolves+selects (navigate only). `:attach <q>` resolves+selects then calls the **same** `activateSelected()` the Enter key uses — so the in-frame swap-vs-legacy-attach decision lives in exactly one place.
- **`:resurrect` refuses when `inFrame()`** with a toast pointing at startup / `perch resurrect` from a shell. `:setup` and `:doctor` do not mutate frame tmux topology, so they exec safely in any mode.
- **`:` never activates while filtering, while a modal is open, or while the help overlay is up** — the existing guard ordering in `Update` enforces this (the cmd-bar routing check is added after those guards).

---

## File Structure

- **Create:** `internal/tui/cmdbar.go` — `cmdKind`, `cmdSpec`, `parseCommand` (pure, T1); then `updateCmdline`, `dispatchCommand`, `execPerch`, `execFinishedMsg`, `resolveItem`, `activateSelected` are added here in T2/T3 (keeps all command-bar logic in one file).
- **Create:** `internal/tui/cmdbar_test.go` — parser table tests (T1) + dispatch/state tests (T3).
- **Modify:** `internal/tui/app.go` — Model fields, `New` init, key routing, View footer, `execFinishedMsg` handling, Enter case → `activateSelected`.
- **Modify:** `internal/tui/keys.go` — `CmdBar` binding.
- **Modify:** `internal/tui/help.go` — advertise `:` in ShortHelp + FullHelp.
- **Modify:** `internal/tui/run.go` — `Config.ExecPath` + `.WithExecPath`.
- **Modify:** `cmd/perch/main.go` — set `cfg.ExecPath` from `os.Executable()` in `handleTUI` and `sidebar`.

---

## Task 1: Pure command parser

**Files:**
- Create: `internal/tui/cmdbar.go`
- Test: `internal/tui/cmdbar_test.go`

- [ ] **Step 1: Write the failing test**

```go
package tui

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  cmdSpec
	}{
		{"quit short", "q", cmdSpec{kind: cmdQuit}},
		{"quit long", "quit", cmdSpec{kind: cmdQuit}},
		{"quit case-insensitive", "Quit", cmdSpec{kind: cmdQuit}},
		{"quit rejects args", "q now", cmdSpec{parseErr: "quit takes no arguments"}},
		{"new", "new", cmdSpec{kind: cmdNew}},
		{"new rejects args", "new x", cmdSpec{parseErr: "new takes no arguments"}},
		{"attach with query", "attach my repo", cmdSpec{kind: cmdAttach, arg: "my repo"}},
		{"attach preserves inner spacing", "attach  foo  bar", cmdSpec{kind: cmdAttach, arg: "foo  bar"}},
		{"attach needs query", "attach", cmdSpec{parseErr: "attach needs a query: :attach <text>"}},
		{"proj", "proj alpha", cmdSpec{kind: cmdProj, arg: "alpha"}},
		{"project alias", "project alpha", cmdSpec{kind: cmdProj, arg: "alpha"}},
		{"proj needs name", "proj", cmdSpec{parseErr: "proj needs a name: :proj <text>"}},
		{"setup bare", "setup", cmdSpec{kind: cmdSetup}},
		{"setup replace", "setup --replace", cmdSpec{kind: cmdSetup, replace: true}},
		{"setup bad flag", "setup --force", cmdSpec{parseErr: "setup: unknown flag --force"}},
		{"doctor", "doctor", cmdSpec{kind: cmdDoctor}},
		{"resurrect", "resurrect", cmdSpec{kind: cmdResurrect}},
		{"help", "help", cmdSpec{kind: cmdHelp}},
		{"help question", "?", cmdSpec{kind: cmdHelp}},
		{"empty is silent cancel", "", cmdSpec{kind: cmdUnknown}},
		{"whitespace is silent cancel", "   ", cmdSpec{kind: cmdUnknown}},
		{"unknown verb", "frobnicate", cmdSpec{parseErr: "unknown command: frobnicate"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCommand(tt.input)
			if got != tt.want {
				t.Fatalf("parseCommand(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestParseCommand -v`
Expected: FAIL — `undefined: parseCommand` / `cmdSpec`.

- [ ] **Step 3: Write the parser**

```go
package tui

import "strings"

// cmdKind enumerates the recognised ':' command-bar verbs.
type cmdKind int

const (
	cmdUnknown cmdKind = iota
	cmdQuit
	cmdNew
	cmdAttach
	cmdProj
	cmdSetup
	cmdDoctor
	cmdResurrect
	cmdHelp
)

// cmdSpec is the parsed result of a command-bar line. When parseErr is non-empty
// the command is invalid: the dispatcher surfaces parseErr as a toast and takes
// no action (kind stays cmdUnknown). An empty/whitespace line yields cmdUnknown
// with no parseErr — a silent cancel.
type cmdSpec struct {
	kind     cmdKind
	arg      string // free-form query for attach/proj (everything after the verb)
	replace  bool   // setup --replace
	parseErr string
}

// parseCommand parses a command-bar line (the text typed after the leading ':')
// into a cmdSpec. Verbs are matched case-insensitively; the query argument for
// attach/proj preserves the user's original casing and inner spacing.
func parseCommand(input string) cmdSpec {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return cmdSpec{kind: cmdUnknown}
	}
	fields := strings.Fields(trimmed)
	verb := strings.ToLower(fields[0])
	rest := fields[1:]
	// arg is everything after the first token, original case + inner spacing.
	arg := strings.TrimSpace(trimmed[len(fields[0]):])

	switch verb {
	case "q", "quit":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "quit takes no arguments"}
		}
		return cmdSpec{kind: cmdQuit}
	case "new":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "new takes no arguments"}
		}
		return cmdSpec{kind: cmdNew}
	case "attach":
		if arg == "" {
			return cmdSpec{parseErr: "attach needs a query: :attach <text>"}
		}
		return cmdSpec{kind: cmdAttach, arg: arg}
	case "proj", "project":
		if arg == "" {
			return cmdSpec{parseErr: "proj needs a name: :proj <text>"}
		}
		return cmdSpec{kind: cmdProj, arg: arg}
	case "setup":
		spec := cmdSpec{kind: cmdSetup}
		for _, tok := range rest {
			switch tok {
			case "--replace":
				spec.replace = true
			default:
				return cmdSpec{parseErr: "setup: unknown flag " + tok}
			}
		}
		return spec
	case "doctor":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "doctor takes no arguments"}
		}
		return cmdSpec{kind: cmdDoctor}
	case "resurrect":
		if len(rest) > 0 {
			return cmdSpec{parseErr: "resurrect takes no arguments"}
		}
		return cmdSpec{kind: cmdResurrect}
	case "help", "h", "?":
		return cmdSpec{kind: cmdHelp}
	default:
		return cmdSpec{parseErr: "unknown command: " + verb}
	}
}
```

> NOTE on `arg`: `trimmed[len(fields[0]):]` slices the original (case-preserving) remainder because `fields[0]` is taken from `trimmed`, so its byte length is a valid prefix length of `trimmed`. The subsequent `TrimSpace` drops the gap after the verb while preserving inner spacing (`"attach  foo  bar"` → `"foo  bar"`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestParseCommand -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/cmdbar.go internal/tui/cmdbar_test.go
git commit -m "feat(tui): command-bar parser (M11-1 T1)"
```

---

## Task 2: Extract `activateSelected` + add `resolveItem`

**Files:**
- Modify: `internal/tui/app.go` (Enter case ~428-453; add two methods)
- Modify: `internal/tui/cmdbar.go` (add `resolveItem`)
- Test: `internal/tui/cmdbar_test.go` (add resolver test); existing app tests must stay green.

- [ ] **Step 1: Write the failing test for `resolveItem`**

Add to `cmdbar_test.go`:

```go
import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
)

func TestResolveItem(t *testing.T) {
	items := []list.Item{
		item{project: "perch", tree: "main", title: "perch", tool: "claude", isSession: true},
		item{project: "kb", tree: "feat-x", title: "kb", tool: "opencode", isSession: true},
		item{project: "perch", tree: "feat-y", title: "perch", tool: "claude", isSession: false}, // not a session
	}
	m := New(items)

	if got := m.resolveItem("kb"); got != 1 {
		t.Fatalf("resolveItem(kb) = %d, want 1", got)
	}
	if got := m.resolveItem("feat-y"); got != -1 {
		t.Fatalf("resolveItem(feat-y) = %d, want -1 (non-session must not match)", got)
	}
	if got := m.resolveItem("zzzzz"); got != -1 {
		t.Fatalf("resolveItem(zzzzz) = %d, want -1", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/tui/ -run TestResolveItem -v`
Expected: FAIL — `m.resolveItem undefined`.

- [ ] **Step 3: Add `resolveItem` to `cmdbar.go`**

Add the import `"github.com/sahilm/fuzzy"` and the method:

```go
// resolveItem fuzzy-matches query against the session rows currently in the list
// and returns the index of the best match, or -1 when nothing matches. Matching
// uses the same FilterValue (project + tree + title + tool) as the '/' filter and
// skips non-session rows (the "start new" placeholders).
func (m Model) resolveItem(query string) int {
	listItems := m.list.Items()
	var (
		idxs    []int
		targets []string
	)
	for i, li := range listItems {
		it, ok := li.(item)
		if !ok || !it.isSession {
			continue
		}
		idxs = append(idxs, i)
		targets = append(targets, it.FilterValue())
	}
	matches := fuzzy.Find(query, targets)
	if len(matches) == 0 {
		return -1
	}
	return idxs[matches[0].Index]
}
```

- [ ] **Step 4: Extract `activateSelected` in `app.go`**

Add the method (place it just below `Update` or near the other Cmd helpers):

```go
// activateSelected opens the highlighted row: in frame mode it swaps a live agent
// into the main slot; in legacy mode it hands off the terminal; for an idle
// session it launches a resume. It is the shared implementation behind the Enter
// key and the ':attach' command, so the frame-vs-legacy decision lives in one
// place.
func (m Model) activateSelected() (tea.Model, tea.Cmd) {
	it, ok := m.selectedItem()
	if !ok || !it.isSession {
		return m.withToast("not a session — nothing to open")
	}
	if it.live && it.liveTarget != "" {
		if m.inFrame() {
			// swapInCmd sets m.swapping via pointer receiver; hoist out of the
			// return tuple to guarantee mutation order.
			cmd := m.swapInCmd(it.captureTarget)
			return m, cmd
		}
		// Legacy mode: switch-client / attach-session terminal handover.
		// NEVER relaunch a live session: concurrent --resume can corrupt the
		// shared transcript.
		return m.attachTo(it.liveTarget)
	}
	return m, m.launchCmd(launchSpec{
		tool:        it.tool,
		sessionID:   it.id,
		branch:      it.tree,
		treePath:    it.treePath,
		projectPath: it.projectPath,
		resume:      true,
	})
}
```

Then replace the Enter case body (currently `app.go:428-453`) with:

```go
		case key.Matches(msg, m.keys.Enter):
			return m.activateSelected()
```

- [ ] **Step 5: Run the full tui suite (behavior-preserving refactor)**

Run: `go test ./internal/tui/ -v`
Expected: PASS — existing Enter-key tests still green, `TestResolveItem` green.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app.go internal/tui/cmdbar.go internal/tui/cmdbar_test.go
git commit -m "refactor(tui): extract activateSelected; add resolveItem (M11-1 T2)"
```

---

## Task 3: Wire the command bar (state, routing, dispatch, View, help)

**Files:**
- Modify: `internal/tui/app.go` (Model fields, `New` init, key routing, View footer, `execFinishedMsg` case)
- Modify: `internal/tui/keys.go` (`CmdBar` binding)
- Modify: `internal/tui/help.go` (advertise `:`)
- Modify: `internal/tui/run.go` (`Config.ExecPath`, `WithExecPath`)
- Modify: `cmd/perch/main.go` (set `cfg.ExecPath`)
- Modify: `internal/tui/cmdbar.go` (`updateCmdline`, `dispatchCommand`, `execPerch`, `execFinishedMsg`)
- Test: `internal/tui/cmdbar_test.go`

- [ ] **Step 1: Write failing dispatch/state tests**

Add to `cmdbar_test.go`:

```go
import tea "github.com/charmbracelet/bubbletea"

// typeCmd feeds ':' then the given runes then Enter, returning the resulting Model.
func typeCmd(t *testing.T, m Model, line string) Model {
	t.Helper()
	mdl, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	m = mdl.(Model)
	if !m.cmdActive {
		t.Fatalf("':' did not activate the command bar")
	}
	for _, r := range line {
		mdl, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mdl.(Model)
	}
	mdl, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return mdl.(Model)
}

func readyModel(items []list.Item) Model {
	m := New(items)
	m.ready = true
	m.width, m.height = 100, 40
	return m
}

func TestCmdBarActivateAndCancel(t *testing.T) {
	m := readyModel(nil)
	mdl, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	m = mdl.(Model)
	if !m.cmdActive {
		t.Fatal("expected cmdActive after ':'")
	}
	mdl, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mdl.(Model)
	if m.cmdActive {
		t.Fatal("esc must cancel the command bar")
	}
}

func TestCmdBarUnknownToasts(t *testing.T) {
	m := typeCmd(t, readyModel(nil), "frobnicate")
	if m.cmdActive {
		t.Fatal("bar should close after Enter")
	}
	if m.toast == "" {
		t.Fatal("unknown command should set a toast")
	}
}

func TestCmdBarProjJumps(t *testing.T) {
	items := []list.Item{
		item{project: "alpha", title: "alpha", isSession: true},
		item{project: "bravo", title: "bravo", isSession: true},
	}
	m := typeCmd(t, readyModel(items), "proj bravo")
	if m.list.Index() != 1 {
		t.Fatalf("proj bravo: list index = %d, want 1", m.list.Index())
	}
}

func TestCmdBarResurrectGatedInFrame(t *testing.T) {
	m := readyModel(nil)
	m.placeholderPaneID = "%9" // inFrame() == true
	m = typeCmd(t, m, "resurrect")
	if m.toast == "" {
		t.Fatal("resurrect inside the frame must refuse with a toast")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/tui/ -run TestCmdBar -v`
Expected: FAIL — `m.cmdActive undefined`, key not handled, etc.

- [ ] **Step 3: Add Model fields + `New` init (`app.go`)**

Add the `textinput` import to `app.go`'s import block:

```go
	"github.com/charmbracelet/bubbles/textinput"
```

Add fields to the `Model` struct (after `swapping bool`):

```go
	// cmdline is the ':' command-bar text input; receives keys only while cmdActive.
	cmdline textinput.Model
	// cmdActive is true while the ':' command bar owns keyboard input.
	cmdActive bool
	// execPath is the absolute path to the running perch binary, used to run
	// `perch setup|doctor|resurrect` via tea.ExecProcess. Empty in test mode →
	// those commands toast instead of exec'ing.
	execPath string
```

In `New`, build the input before the `return` and add it to the struct literal:

```go
	ti := textinput.New()
	ti.Prompt = ":"
	ti.CharLimit = 256

	return Model{
		list:    l,
		preview: viewport.New(0, 0),
		keys:    defaultKeys(),
		help:    help.New(),
		cmdline: ti,
		refresh: time.Second, // default; overridable via WithRefresh
	}
```

- [ ] **Step 4: Add the `CmdBar` key binding (`keys.go`)**

Add field `CmdBar key.Binding` to `keyMap`, and in `defaultKeys`:

```go
		CmdBar: key.NewBinding(
			key.WithKeys(":"),
			key.WithHelp(":", "command"),
		),
```

- [ ] **Step 5: Add activation + routing in `Update` (`app.go`)**

Insert the routing check immediately after the modal guard (after `app.go:404`, before the normal `switch`):

```go
		// While the ':' command bar is active it owns all keys.
		if m.cmdActive {
			return m.updateCmdline(msg)
		}
```

Add the activation case inside the normal `switch` (e.g. right after the `Help` case):

```go
		case key.Matches(msg, m.keys.CmdBar):
			m.cmdActive = true
			m.cmdline.SetValue("")
			return m, m.cmdline.Focus()
```

Add the `execFinishedMsg` handling among the other message cases in `Update`:

```go
	case execFinishedMsg:
		if msg.err != nil {
			return m.withToast("command failed: " + msg.err.Error())
		}
		if m.loader != nil {
			return m, m.reloadCmd()
		}
		return m, nil
```

- [ ] **Step 6: Add `updateCmdline`, `dispatchCommand`, `execPerch`, `execFinishedMsg` (`cmdbar.go`)**

Add imports to `cmdbar.go`: `"os/exec"` and `tea "github.com/charmbracelet/bubbletea"`.

```go
// execFinishedMsg is delivered after a tea.ExecProcess command (:setup/:doctor/
// :resurrect) returns and the TUI resumes.
type execFinishedMsg struct{ err error }

// updateCmdline handles keys while the ':' command bar is active. Esc cancels;
// Enter parses and dispatches; everything else feeds the text input.
func (m Model) updateCmdline(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.cmdActive = false
		m.cmdline.Blur()
		m.cmdline.SetValue("")
		return m, nil
	case tea.KeyEnter:
		input := m.cmdline.Value()
		m.cmdActive = false
		m.cmdline.Blur()
		m.cmdline.SetValue("")
		return m.dispatchCommand(parseCommand(input))
	}
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(msg)
	return m, cmd
}

// dispatchCommand performs the side effect for a parsed command. Parse errors
// and unknown commands surface as a toast; an empty line is a silent cancel.
func (m Model) dispatchCommand(spec cmdSpec) (tea.Model, tea.Cmd) {
	if spec.parseErr != "" {
		return m.withToast(spec.parseErr)
	}
	switch spec.kind {
	case cmdUnknown:
		return m, nil // empty input → silent cancel
	case cmdQuit:
		if m.inFrame() {
			return m, m.quitFrameCmd()
		}
		return m, tea.Quit
	case cmdHelp:
		m.showHelp = !m.showHelp
		return m, nil
	case cmdNew:
		it, ok := m.selectedItem()
		if !ok {
			return m.withToast("no selection — nothing to start")
		}
		return m, m.launchCmd(launchSpec{
			tool:        it.tool,
			branch:      it.tree,
			treePath:    it.treePath,
			projectPath: it.projectPath,
			resume:      false,
		})
	case cmdProj:
		idx := m.resolveItem(spec.arg)
		if idx < 0 {
			return m.withToast("no match for " + spec.arg)
		}
		m.list.Select(idx)
		return m, m.previewCmd()
	case cmdAttach:
		idx := m.resolveItem(spec.arg)
		if idx < 0 {
			return m.withToast("no match for " + spec.arg)
		}
		m.list.Select(idx)
		return m.activateSelected()
	case cmdDoctor:
		return m.execPerch("doctor")
	case cmdSetup:
		args := []string{"setup"}
		if spec.replace {
			args = append(args, "--replace")
		}
		return m.execPerch(args...)
	case cmdResurrect:
		if m.inFrame() {
			return m.withToast("resurrect runs at startup or from a shell (perch resurrect) — not inside the frame")
		}
		return m.execPerch("resurrect")
	}
	return m, nil
}

// execPerch suspends the TUI and runs `perch <args...>` attached to the terminal
// via tea.ExecProcess, reloading the list on resume. Shared by :doctor, :setup,
// and (outside the frame only) :resurrect.
func (m Model) execPerch(args ...string) (tea.Model, tea.Cmd) {
	if m.execPath == "" {
		return m.withToast("perch binary path unavailable")
	}
	c := exec.Command(m.execPath, args...) //nolint:gosec // execPath is os.Executable, args are fixed verbs
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return execFinishedMsg{err: err}
	})
}
```

- [ ] **Step 7: Render the command line in `View` (`app.go`)**

Replace the footer-text block (`app.go:551-557`) with:

```go
	var footerText string
	switch {
	case m.cmdActive:
		footerText = m.cmdline.View()
	case m.modal.kind != modalNone:
		footerText = modalFooterHint(m.modal.kind)
	default:
		m.help.Width = m.width
		footerText = m.help.ShortHelpView(m.ShortHelp())
	}
```

- [ ] **Step 8: Advertise `:` in help (`help.go`)**

In `ShortHelp`, add `m.keys.CmdBar` to the always-on tail group:

```go
	b = append(b, m.keys.Filter, m.keys.CmdBar, m.keys.ScreenFwd, m.keys.Help, m.keys.Quit)
```

In `FullHelp`, add `m.keys.CmdBar` to the filter column:

```go
		{m.keys.Filter, m.keys.ClearFilter, m.keys.CmdBar},
```

- [ ] **Step 9: Wire `ExecPath` (`run.go` + `main.go`)**

In `run.go`, add to `Config`:

```go
	// ExecPath is the absolute path to the running perch binary (os.Executable),
	// used by the ':' command bar to run setup/doctor/resurrect via ExecProcess.
	ExecPath string
```

Add the option to `app.go` (near `WithRefresh`):

```go
// WithExecPath returns a copy of m with the perch binary path set, enabling the
// command bar's :setup/:doctor/:resurrect commands.
func (m Model) WithExecPath(p string) Model {
	m.execPath = p
	return m
}
```

In `run.go` `Run`, chain it:

```go
	m := New(nil).WithLoader(ldr).WithRefresh(time.Duration(cfg.RefreshMs) * time.Millisecond).WithExecPath(cfg.ExecPath)
```

In `cmd/perch/main.go`, set `ExecPath` in both `handleTUI` (the `cfg := tui.Config{...}` at ~101) and `sidebar` (the `cfg := tui.Config{...}` at ~263). After each struct literal:

```go
	if exe, exeErr := os.Executable(); exeErr == nil {
		cfg.ExecPath = exe
	}
```

- [ ] **Step 10: Run the full suite + lint + build**

```bash
go test ./internal/tui/ -v
go build ./cmd/perch
golangci-lint run ./internal/tui/... ./cmd/perch/...
```
Expected: all PASS, build OK, lint clean.

- [ ] **Step 11: Commit**

```bash
git add internal/tui/cmdbar.go internal/tui/cmdbar_test.go internal/tui/app.go internal/tui/keys.go internal/tui/help.go internal/tui/run.go cmd/perch/main.go
git commit -m "feat(tui): : command bar — quit/new/attach/proj/setup/doctor/resurrect (M11-1)"
```

---

## Self-review checklist (run before declaring done)

1. **Spec coverage:** `:q`, `:new`, `:attach`, `:proj`, `:setup [--replace]`, `:doctor`, `:resurrect`, `:help` all dispatched; `:resurrect` gated in-frame; help advertises `:`. ✅
2. **Placeholder scan:** no TODO/TBD; every step has full code. ✅
3. **Type consistency:** `cmdSpec` fields (`kind`/`arg`/`replace`/`parseErr`) identical across T1/T3; `resolveItem` returns `int` (−1 sentinel) used consistently; `activateSelected` signature `(tea.Model, tea.Cmd)` matches Enter usage. ✅
4. **No real-config writes in tests:** dispatch tests never reach `execPerch` exec (execPath "" → toast); `:setup`/`:doctor`/`:resurrect` are never exercised against a real binary in unit tests. ✅
