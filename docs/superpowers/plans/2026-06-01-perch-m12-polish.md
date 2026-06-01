# M12 — TUI Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Final TUI polish — readable contrast, accurate help/footer text (incl. the `prefix ←/→` refocus hint), a dedicated sidebar-collapse toggle, and modal/help rendered *over* a dimmed body (the sidebar stays visible behind a confirmation) instead of replacing it.

**Architecture:** Two tasks, cheap-and-safe first. **M12-A** (chrome): split a readable `colorMuted` from the border-only `colorSubtle`, refresh help text, add a `c` sidebar-collapse toggle aliased to the existing `modeFullPreview`. **M12-B** (compositing): a cell-accurate (ANSI-aware) `composite` helper overlays the modal/help box onto a dimmed body, preserving the messageLine+body+footer height invariant View already maintains.

**Tech Stack:** Go, lipgloss v1.1.0, `github.com/charmbracelet/x/ansi` v0.11.6 (already an indirect dep — promoted to direct in M12-B, no new code on the machine).

---

## Design decisions (locked)

- **Contrast: split, don't bump.** `colorSubtle` (#383838 dark) is correct as a *border* color but unreadable as *text*. Add `colorMuted` (~#999 dark / #6C6C6C light) for the text uses (footer, emptyState). Leave `colorSubtle` on borders so they don't get louder.
- **Sidebar toggle is an alias, not new machinery.** "Collapse sidebar" == `modeFullPreview` (list hidden, preview full-width) which `z`/`Z` already reach. The dedicated `c` key just toggles `mode` between `modeNormal` and `modeFullPreview` for discoverability. No new layout code.
- **Compositing is cell-accurate.** The splice offset and bg truncation are in terminal **cells** via `x/ansi` (emoji glyphs are width-2). Rune/byte slicing would ship visibly shifted. The discriminating test uses an emoji-bearing backdrop row.
- **Genuine dim = strip + re-render.** The body carries its own SGR (accent selected row, per-status glyph colors); a lipgloss "dim" wrapper won't override inner codes. To actually dim, `ansi.Strip` the body and re-render it in `colorMuted` while a modal/help is up (body colors are intentionally dropped behind the modal — acceptable and visually correct).
- **Height invariant preserved.** The composited frame keeps `MaxWidth/MaxHeight(bodyRegionHeight)` clamps so a short terminal clips the box rather than overflowing. Modal text stays plain-substring-findable so existing View tests survive.

---

## File Structure

- **M12-A:** `internal/tui/styles.go` (colorMuted), `internal/tui/keys.go` (CollapseSidebar binding + Enter help wording), `internal/tui/help.go` (refocus hint), `internal/tui/app.go` (the `c` case).
- **M12-B:** `internal/tui/overlay.go` (new — `composite`), `internal/tui/overlay_test.go` (new), `internal/tui/app.go` (View wiring + dim), `go.mod`/`go.sum`/`vendor/` (x/ansi → direct).

---

## Task A: Contrast, text, sidebar toggle

**Files:**
- Modify: `internal/tui/styles.go`, `internal/tui/keys.go`, `internal/tui/help.go`, `internal/tui/app.go`
- Test: `internal/tui/app_test.go` (toggle behavior); existing tests stay green.

- [ ] **Step 1: Write the failing sidebar-toggle test**

Add to `app_test.go` (or a new `screen_test.go` if cleaner — match the package's test layout):

```go
func TestCollapseSidebarToggle(t *testing.T) {
	m := New(nil)
	m.ready = true
	m.width, m.height = 100, 40
	if m.mode != modeNormal {
		t.Fatalf("precondition: mode = %v, want modeNormal", m.mode)
	}
	// First 'c' collapses the sidebar → modeFullPreview.
	mdl, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = mdl.(Model)
	if m.mode != modeFullPreview {
		t.Fatalf("after 'c': mode = %v, want modeFullPreview", m.mode)
	}
	// Second 'c' restores the two-pane layout.
	mdl, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = mdl.(Model)
	if m.mode != modeNormal {
		t.Fatalf("after second 'c': mode = %v, want modeNormal", m.mode)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/tui/ -run TestCollapseSidebarToggle`
Expected: FAIL — `'c'` falls through to the list, mode stays modeNormal.

- [ ] **Step 3: Add the `CollapseSidebar` binding (`keys.go`)**

Add field `CollapseSidebar key.Binding` to `keyMap`; in `defaultKeys`:

```go
		CollapseSidebar: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "collapse sidebar"),
		),
```

Also update the `Enter` binding help text (stale under the swap model):

```go
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("↵", "open"),
		),
```

- [ ] **Step 4: Add the `c` case in `Update` (`app.go`)**

In the normal-mode `switch` (e.g. after the `ScreenBack` case), add:

```go
		case key.Matches(msg, m.keys.CollapseSidebar):
			// Dedicated alias: toggle between the two-pane view and a collapsed
			// sidebar (preview full-width). Discoverability over the z/Z cycle.
			if m.mode == modeFullPreview {
				m.mode = modeNormal
			} else {
				m.mode = modeFullPreview
			}
			m.relayout()
			return m, m.previewCmd()
```

- [ ] **Step 5: Split `colorMuted` from `colorSubtle` (`styles.go`)**

Add to the palette:

```go
	// colorMuted is a readable dim used for secondary TEXT (footer, empty-state).
	// Distinct from colorSubtle, which is intentionally near-background for BORDERS.
	colorMuted = lipgloss.AdaptiveColor{Light: "#6C6C6C", Dark: "#999999"}
```

Change the text-bearing styles from `colorSubtle` to `colorMuted` (leave `leftPane`/`rightPane` borders on `colorSubtle`):

```go
	footer: lipgloss.NewStyle().
		Foreground(colorMuted).
		MarginTop(0),
	...
	emptyState: lipgloss.NewStyle().
		Foreground(colorMuted).
		Padding(1, 2),
```

- [ ] **Step 6: Add the refocus hint to help (`help.go`)**

Add `m.keys.CollapseSidebar` to `ShortHelp`'s tail group (after `ScreenFwd`) and to a `FullHelp` column. Then add a non-binding "how to refocus" note to the `?` overlay. Since `FullHelp` returns `[][]key.Binding`, add a synthetic binding for the prefix hint:

In `FullHelp`, add a final column:

```go
		{
			m.keys.CmdBar,
			m.keys.CollapseSidebar,
			key.NewBinding(
				key.WithKeys("prefix ←/→"),
				key.WithHelp("tmux prefix ←/→", "focus agent / sidebar"),
			),
		},
```

> The synthetic binding's keys aren't matched against input (it documents a tmux-level gesture); it exists only so the `?` overlay shows how to move focus between the sidebar and the live agent pane under the default-socket model.

- [ ] **Step 7: Run tests + lint**

```bash
go test ./internal/tui/ -v
golangci-lint run ./internal/tui/...
```
Expected: PASS, lint clean. Existing footer/help tests may assert text — update any that referenced the old "switch" wording or colorSubtle (adjust the test expectation, not the design).

- [ ] **Step 8: Commit**

```bash
git add internal/tui/styles.go internal/tui/keys.go internal/tui/help.go internal/tui/app.go internal/tui/*_test.go
git commit -m "feat(tui): readable contrast, c sidebar toggle, refreshed help text (M12-A)"
```

---

## Task B: Modal/help over a dimmed body

**Files:**
- Create: `internal/tui/overlay.go`, `internal/tui/overlay_test.go`
- Modify: `internal/tui/app.go` (`View`)
- Modify: `go.mod`, `go.sum`, `vendor/` (x/ansi indirect → direct)

- [ ] **Step 1: Promote x/ansi to a direct dependency**

```bash
go get github.com/charmbracelet/x/ansi@v0.11.6   # already the pinned version; records it direct
go mod tidy
go mod vendor
```
Expected: `go.mod` lists `github.com/charmbracelet/x/ansi v0.11.6` WITHOUT `// indirect`. No version change.

- [ ] **Step 2: Write the failing overlay tests (cell-accuracy is the point)**

Create `internal/tui/overlay_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestComposite_PreservesUncoveredLines(t *testing.T) {
	bg := "aaaa\nbbbb\ncccc\ndddd"
	fg := "XX"
	out := composite(bg, fg, 1, 1) // place "XX" at row 1, col 1
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("line count = %d, want 4", len(lines))
	}
	if lines[0] != "aaaa" || lines[2] != "cccc" || lines[3] != "dddd" {
		t.Errorf("uncovered lines changed: %q", lines)
	}
	if got := ansi.Strip(lines[1]); got != "bXXb" {
		t.Errorf("composited row = %q, want \"bXXb\"", got)
	}
}

func TestComposite_WideRuneOffsetIsCellBased(t *testing.T) {
	// Backdrop row has a width-2 emoji: cells → [🤖][🤖](0,1) [sp](2) [a](3) [b](4) [c](5)
	bg := "🤖 abc"
	fg := "|"
	out := composite(bg, fg, 4, 0) // overlay at CELL column 4 (the 'b')
	line := ansi.Strip(strings.Split(out, "\n")[0])
	idx := strings.Index(line, "|")
	if idx < 0 {
		t.Fatalf("overlay char not found in %q", line)
	}
	// The visible width BEFORE the overlay char must be exactly 4 cells.
	if w := ansi.StringWidth(line[:idx]); w != 4 {
		t.Errorf("overlay landed at cell width %d, want 4 (rune/byte slicing bug?)", w)
	}
}

func TestComposite_ClipsOverflowingFg(t *testing.T) {
	bg := "abcd\nefgh"
	fg := "WIDE" // 4 cells, placed at col 2 on a 4-cell bg → must clip to "WI"
	out := composite(bg, fg, 2, 0)
	first := strings.Split(out, "\n")[0]
	if w := ansi.StringWidth(first); w != 4 {
		t.Fatalf("composited width = %d, want 4 (fg must clip to bg width, not overflow)", w)
	}
	if got := ansi.Strip(first); got != "abWI" {
		t.Errorf("clipped row = %q, want \"abWI\"", got)
	}
}
```

- [ ] **Step 3: Write `composite` (`overlay.go`)**

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// composite overlays fg onto bg with fg's top-left corner at cell column x and
// row y. All horizontal math is in terminal cells (ANSI-aware) so wide runes
// (emoji) in bg do not shift the overlay. Rows of bg not covered by fg pass
// through unchanged; on covered rows, bg's left part (cells [0,x)) is kept, the
// fg line is spliced in, and bg's right part (cells [x+fgWidth, bgWidth)) is
// appended. fg is clipped to bg's width so the result never grows wider than bg
// — preserving View's height/width invariants on a short terminal.
func composite(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	for i, fgLine := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue // fg row outside bg vertical bounds → skip
		}
		bgLine := bgLines[row]
		bgWidth := ansi.StringWidth(bgLine)
		if x >= bgWidth {
			continue // overlay starts past the line's content → leave bg as-is
		}

		fgWidth := ansi.StringWidth(fgLine)

		// Left part: bg cells [0, x). Pad with spaces if bg is shorter than x.
		left := ansi.Cut(bgLine, 0, x)
		if lw := ansi.StringWidth(left); lw < x {
			left += strings.Repeat(" ", x-lw)
		}

		// Clip fg so left+fg never exceeds bg width.
		if x+fgWidth > bgWidth {
			fgLine = ansi.Truncate(fgLine, bgWidth-x, "")
			fgWidth = ansi.StringWidth(fgLine)
		}

		// Right part: bg cells [x+fgWidth, bgWidth). Empty when fg reaches the edge.
		right := ansi.Cut(bgLine, x+fgWidth, bgWidth)

		// Reset SGR around the fg splice so bg color codes don't bleed into fg
		// and vice versa. ansi.Cut re-opens active styles for `right` itself.
		bgLines[row] = left + ansi.ResetStyle + fgLine + ansi.ResetStyle + right
	}
	return strings.Join(bgLines, "\n")
}
```

> Verify `ansi.Cut`, `ansi.Truncate`, `ansi.StringWidth`, and `ansi.ResetStyle` exist with these signatures in x/ansi v0.11.6 (`go doc github.com/charmbracelet/x/ansi`). If `ansi.ResetStyle` is named differently (e.g. a const), use the literal `"\x1b[0m"`. If `ansi.Cut(s, left, right)` semantics differ, adapt — the contract you need is "return the substring spanning cell columns [left,right), re-emitting any active style at the cut."

- [ ] **Step 4: Run overlay tests**

Run: `go test ./internal/tui/ -run TestComposite -v`
Expected: PASS — including the wide-rune cell-offset test. If the emoji test fails with width 3, you used rune/byte indexing somewhere; fix to cell-based.

- [ ] **Step 5: Wire `composite` into `View` (`app.go`)**

Replace the `showHelp` and `modal` early-return blocks (currently they center the box and return *without* the body) with a composite over a dimmed body. Build the body once, dim it, center the box, and overlay:

```go
	bodyRegionHeight := max(0, m.height-lipgloss.Height(footer)-messageBarHeight)

	overlayBox := ""
	switch {
	case m.showHelp:
		overlayBox = styles.helpOverlay.Render(m.help.FullHelpView(m.FullHelp()))
	case m.modal.kind != modalNone:
		overlayBox = renderModal(m.modal)
	}

	if overlayBox != "" {
		// Dim the body: strip its own SGR and re-render muted so the overlay box
		// stands out. (Body colors are intentionally dropped while a modal is up.)
		dimmed := styles.dimmedBody.Render(ansi.Strip(body))
		dimmed = lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(bodyRegionHeight).Render(dimmed)

		box := lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(bodyRegionHeight).Render(overlayBox)
		boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)
		x := max(0, (m.width-boxW)/2)
		yOff := max(0, (bodyRegionHeight-boxH)/2)

		// Pad the dimmed body to the full region so composite has rows to write on.
		region := lipgloss.NewStyle().Width(m.width).Height(bodyRegionHeight).Render(dimmed)
		composited := composite(region, box, x, yOff)
		return lipgloss.JoinVertical(lipgloss.Left, messageLine, composited, footer)
	}

	body = lipgloss.NewStyle().MaxWidth(m.width).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, messageLine, body, footer)
```

Add the `ansi` import to `app.go` and a `dimmedBody` style to `styles.go`:

```go
	// dimmedBody renders the stripped body behind a modal/help overlay.
	dimmedBody lipgloss.Style
```
```go
	dimmedBody: lipgloss.NewStyle().Foreground(colorSubtle),
```

> `body` is produced by `bodyView()` earlier in `View`; ensure it's computed before this block (it already is — `body := m.bodyView()` near the top). The empty-state path (`m.emptyStateText()`) sets `body` too, so the overlay composites over the empty-state text when there are no sessions — correct.

- [ ] **Step 6: Run the full tui suite**

Run: `go test ./internal/tui/ -v`
Expected: PASS. Existing modal/help View tests assert on plain substrings (e.g. "Kill", help labels) — `ansi.Strip` of the composited frame must still contain them. If a test asserted the body was ABSENT during a modal, update it: the body is now present (dimmed) by design.

- [ ] **Step 7: Full gate + binary size**

```bash
go build ./... && go vet ./...
go test -race -tags=integration ./internal/tui/
golangci-lint run ./internal/tui/...
make build && ls -lh bin/perch   # confirm still ~5 MB (x/ansi was already linked in)
```
Expected: all green; binary ≈ 5.2 MB (no growth — x/ansi was already compiled via lipgloss).

- [ ] **Step 8: Commit**

```bash
git add internal/tui/overlay.go internal/tui/overlay_test.go internal/tui/app.go internal/tui/styles.go go.mod go.sum vendor/
git commit -m "feat(tui): modal/help composite over dimmed body, cell-accurate (M12-B)"
```

---

## Fallback (only if cell-accurate splicing proves unstable)

If `composite` can't be made reliable against the real rendered frame (SGR bleed you can't tame in x/ansi v0.11.6), fall back to a **vertical** overlay: keep body rows ABOVE and BELOW the centered box visible, replace only the box's own rows. This still keeps the sidebar partly visible and is splice-free. Document the choice in the commit. Do NOT add a new dependency for compositing.

---

## Self-review checklist

1. **Contrast:** footer/emptyState use readable `colorMuted`; borders keep `colorSubtle`. ✅
2. **Toggle:** `c` flips normal↔fullPreview; no new layout machinery. ✅
3. **Help:** Enter reads "open"; `?` shows the `prefix ←/→` refocus gesture and `c`/`:`. ✅
4. **Compositing:** cell-accurate (emoji test passes); height/width invariant preserved (clamps kept); modal text still substring-findable. ✅
5. **Dep hygiene:** x/ansi recorded direct, vendored; binary unchanged ~5 MB; vulncheck clean. ✅
