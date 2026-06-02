package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ── frame/swap primitives ─────────────────────────────────────────────────────
//
// These methods add the swap-pane, split-window, resize-window, resize-pane,
// refresh-client, and display-message (pane-size) primitives needed for the
// M11-0 persistent-frame switcher. All commands are routed through the
// ExecArgs/Runner seam (argv, no shell). Validation happens before any runner
// call so that FakeRunner tests can assert Calls is empty on rejection.

// validatePaneID returns an error if id is empty or starts with '-'. Pane IDs
// are '%'-prefixed (e.g. "%3") so a leading '-' always indicates a flag leak.
func validatePaneID(id, field string) error {
	if id == "" {
		return fmt.Errorf("tmux: %s pane ID must not be empty", field)
	}
	if id[0] == '-' {
		return fmt.Errorf("tmux: %s pane ID must not start with '-': %q", field, id)
	}
	return nil
}

// SwapPane exchanges the positions of two panes identified by their pane IDs
// (%%N format). It runs swap-pane -s <src> -t <dst>.
//
// Both src and dst are validated before the runner is touched: an empty value
// or a value starting with '-' is rejected to prevent flag injection.
func (o Tmux) SwapPane(ctx context.Context, src, dst string) error {
	if err := validatePaneID(src, "src"); err != nil {
		return err
	}
	if err := validatePaneID(dst, "dst"); err != nil {
		return err
	}
	_, stderr, err := o.runner().Run(ctx, o.bin(), o.args("swap-pane", "-s", src, "-t", dst)...)
	if err != nil {
		return fmt.Errorf("tmux swap-pane: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// SplitWindow creates a new pane by splitting the pane addressed by target.
// dir sets the starting directory (-c). When horizontal is true, the split is
// horizontal (-h); otherwise vertical (-v). The -d flag leaves the new pane
// de-focused. -P -F '#{pane_id}' causes tmux to print the new pane's ID.
// When cmd is non-empty it is appended as the shell command to run in the pane.
//
// Returns the pane ID (%%N) of the newly created pane, trimmed of whitespace.
func (o Tmux) SplitWindow(ctx context.Context, target, dir string, horizontal bool, cmd string) (string, error) {
	orientation := "-v"
	if horizontal {
		orientation = "-h"
	}
	sub := []string{
		"split-window",
		"-d",
		orientation,
		"-P", "-F", "#{pane_id}",
		"-t", target,
		"-c", dir,
	}
	if cmd != "" {
		sub = append(sub, cmd)
	}
	stdout, stderr, err := o.runner().Run(ctx, o.bin(), o.args(sub...)...)
	if err != nil {
		return "", fmt.Errorf("tmux split-window: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(string(stdout)), nil
}

// ResizeWindow resizes the window that contains the session/target to exactly
// w columns and h rows. target is any tmux target token that addresses the
// session or window (e.g. "=perch" or "=perch:=frame"). The resize is applied
// with resize-window -t <target> -x <w> -y <h>.
func (o Tmux) ResizeWindow(ctx context.Context, target string, w, h int) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("resize-window", "-t", target, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h))...)
	if err != nil {
		return fmt.Errorf("tmux resize-window: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// ResizePane resizes paneID to exactly w columns and h rows using
// resize-pane -t <paneID> -x <w> -y <h>.
func (o Tmux) ResizePane(ctx context.Context, paneID string, w, h int) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(),
		o.args("resize-pane", "-t", paneID, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h))...)
	if err != nil {
		return fmt.Errorf("tmux resize-pane: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// RefreshClient sends a refresh-client command. This nudges all attached
// clients to repaint, which is needed after a swap-pane to ensure the newly
// displayed agent re-renders at the correct terminal dimensions.
func (o Tmux) RefreshClient(ctx context.Context) error {
	_, stderr, err := o.runner().Run(ctx, o.bin(), o.args("refresh-client")...)
	if err != nil {
		return fmt.Errorf("tmux refresh-client: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// PaneSize returns the current width and height of paneID by running
// display-message -p -t <paneID> '#{pane_width}\x1f#{pane_height}'. The fields
// are split on the 0x1f (ASCII unit separator) and parsed as integers — the
// same delimiter convention used in paneFormat and CurrentClientWindow.
func (o Tmux) PaneSize(ctx context.Context, paneID string) (w, h int, err error) {
	stdout, stderr, rerr := o.runner().Run(ctx, o.bin(),
		o.args("display-message", "-p", "-t", paneID, "#{pane_width}\x1f#{pane_height}")...)
	if rerr != nil {
		return 0, 0, fmt.Errorf("tmux display-message: %w: %s", rerr, strings.TrimSpace(string(stderr)))
	}
	out := strings.TrimSpace(string(stdout))
	parts := strings.SplitN(out, FieldDelim, 2)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("tmux display-message: unexpected output %q", out)
	}
	wVal, werr := strconv.Atoi(strings.TrimSpace(parts[0]))
	if werr != nil {
		return 0, 0, fmt.Errorf("tmux display-message: parse width from %q: %w", out, werr)
	}
	hVal, herr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if herr != nil {
		return 0, 0, fmt.Errorf("tmux display-message: parse height from %q: %w", out, herr)
	}
	return wVal, hVal, nil
}
