package app

import (
	"fmt"
	"strings"

	"github.com/miniature-pug/perch/internal/envsync"
	"github.com/miniature-pug/perch/internal/safe"
)

// perchEnvPrefix marks perch-internal variables. The captured env-sync overlay
// must never override one: the exit sentinel (PERCH_EXIT_*) and env-sync
// (PERCH_ENVSYNC_*) handles are perch plumbing injected at spawn, not user
// environment.
const perchEnvPrefix = "PERCH_"

// envsyncPaneEnv returns the KEY=VALUE process-environment entries a per-workspace
// drawer needs so `perch reload` can authenticate to the env-sync endpoint by
// name (never inlined into any typed line). The variable names are single-sourced
// in the envsync package, shared with the `perch reload` command that reads them.
func envsyncPaneEnv(url, token, workspaceID string) []string {
	return []string{
		envsync.EnvURL + "=" + url,
		envsync.EnvToken + "=" + token,
		envsync.EnvWS + "=" + workspaceID,
	}
}

// mergeEnv composes the process environment for a spawned pane by layering three
// slices of KEY=VALUE entries with later layers winning on a key collision:
//
//	base     — os.Environ(), the app's inherited environment
//	injected — pane-specific perch plumbing (exit sentinel or env-sync handles)
//	overlay  — the env delta a `perch reload` captured for this workspace
//
// Precedence is therefore overlay > injected > base. Keys are deduplicated (a
// later entry with the same key replaces the earlier one). The overlay is barred
// from touching any PERCH_-prefixed key, so the exit sentinel and the
// PERCH_ENVSYNC_* handles always survive a reload — the captured delta already
// excludes PERCH_*, and this makes that guarantee structural. Malformed entries
// (no '=' or empty key) are skipped.
func mergeEnv(base, injected, overlay []string) []string {
	idx := make(map[string]int, len(base)+len(injected)+len(overlay))
	out := make([]string, 0, len(base)+len(injected)+len(overlay))
	put := func(entry string) {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return
		}
		if pos, exists := idx[key]; exists {
			out[pos] = entry
			return
		}
		idx[key] = len(out)
		out = append(out, entry)
	}
	for _, e := range base {
		put(e)
	}
	for _, e := range injected {
		put(e)
	}
	for _, e := range overlay {
		key, _, ok := strings.Cut(e, "=")
		if !ok || key == "" || strings.HasPrefix(key, perchEnvPrefix) {
			continue // overlay never clobbers perch plumbing
		}
		put(e)
	}
	return out
}

// overlayFor returns the in-memory env overlay captured for a workspace, or nil
// when none has been captured. Guarded by a.mu.
func (a *App) overlayFor(workspaceID string) []string {
	if workspaceID == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.envOverlay[workspaceID]
}

// onEnvSync is the env-sync endpoint's callback. It stores the captured delta as
// the workspace's in-memory overlay (NEVER persisted — the payload may hold
// secrets) and dispatches a conversation-preserving relaunch ASYNCHRONOUSLY.
//
// The relaunch must never run inline in the HTTP handler: OpenWorkspace takes
// a.mu and tears down and rebuilds the pane, so a synchronous call would stall
// the env-sync handler (holding the request open) and risk reentrancy with the
// event pump. Storing the overlay before spawning the goroutine guarantees the
// relaunch sees the fresh delta.
func (a *App) onEnvSync(workspaceID string, delta []string) {
	a.mu.Lock()
	if a.envOverlay == nil {
		a.envOverlay = map[string][]string{}
	}
	a.envOverlay[workspaceID] = delta
	a.mu.Unlock()

	go func() {
		defer safe.Recover("envsync-relaunch")
		_ = a.OpenWorkspace(workspaceID)
	}()
}

// shellQuote wraps s in single quotes for safe insertion into a shell command
// line typed into a pty, using the standard POSIX escape for an embedded single
// quote: close the quote, add a backslash-escaped literal quote, then reopen the
// quote. An absolute path containing spaces or single quotes therefore survives
// being typed into the drawer shell intact and reaches the shell as one argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ReloadAgentEnv is the bound method the drawer's reload button calls. It resolves
// the workspace id from the drawer pane id and types `perch reload` into that
// drawer's shell, so the button and the manual command share exactly one code
// path. It is unavailable on the home drawer, which has no workspace.
func (a *App) ReloadAgentEnv(paneID string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	if paneID == homeShellPaneID {
		return fmt.Errorf("reload is not available on the home shell")
	}
	workspaceID := strings.TrimPrefix(paneID, "shell-")
	if workspaceID == paneID || workspaceID == "" {
		return fmt.Errorf("not a workspace drawer pane: %q", paneID)
	}
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	// The binary lives at bin/perch and is launched by absolute path, so it is NOT
	// on PATH. When we know our own absolute path, type the shell-quoted absolute
	// path so the button works even if a login profile clobbers PATH; otherwise
	// fall back to a bare `perch` (no regression when os.Executable failed).
	line := "perch reload\n"
	if a.perchBin != "" {
		line = shellQuote(a.perchBin) + " reload\n"
	}
	_, err := br.Write([]byte(line))
	return err
}
