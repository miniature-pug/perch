package app

import (
	"fmt"
	"strings"

	"github.com/miniature-pug/perch/internal/envsync"
	"github.com/miniature-pug/perch/internal/safe"
)

// perchEnvPrefix marks perch-internal variables. The captured env-sync overlay
// must never override a perch-internal variable. The exit sentinel
// (PERCH_EXIT_*) and the env-sync (PERCH_ENVSYNC_*) handles are perch
// plumbing. perch injects them at spawn. They are not user environment
// variables.
const perchEnvPrefix = "PERCH_"

// evtWorkspaceRelaunch tells the frontend to remount a workspace's agent
// terminal with a fresh xterm. This relaunch keeps the conversation. The
// value must match EVT_WORKSPACE_RELAUNCH in frontend/src/lib/wails.ts.
const evtWorkspaceRelaunch = "workspace:relaunch"

// envsyncPaneEnv returns the KEY=VALUE process-environment entries a
// per-workspace drawer needs. The `perch reload` command uses these entries
// to authenticate to the env-sync endpoint by name, not by a value typed
// into a line. The envsync package defines the variable names once. The
// `perch reload` command that reads them shares this same source.
func envsyncPaneEnv(url, token, workspaceID string) []string {
	return []string{
		envsync.EnvURL + "=" + url,
		envsync.EnvToken + "=" + token,
		envsync.EnvWS + "=" + workspaceID,
	}
}

// mergeEnv builds the process environment for a spawned pane. It layers three
// slices of KEY=VALUE entries. A later layer wins on a key collision:
//
//	base     - os.Environ(), the app's inherited environment
//	injected - pane-specific perch plumbing (exit sentinel or env-sync handles)
//	overlay  - the env delta a `perch reload` command captured for this workspace
//
// The precedence order is overlay, then injected, then base. mergeEnv removes
// duplicate keys: a later entry with the same key replaces the earlier one.
// The overlay must never touch a PERCH_-prefixed key. This keeps the exit
// sentinel and the PERCH_ENVSYNC_* handles safe across a reload. The
// captured delta already excludes PERCH_* entries. mergeEnv skips malformed
// entries that have no '=' or an empty key.
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
			continue // the overlay must never overwrite perch plumbing
		}
		put(e)
	}
	return out
}

// overlayFor returns the in-memory env overlay captured for a workspace. It
// returns nil when no capture exists. a.mu guards this function.
func (a *App) overlayFor(workspaceID string) []string {
	overlay, _ := a.envFor(workspaceID)
	return overlay
}

// envFor returns the env overlay and the unset list captured for a
// workspace by its last `perch reload`. Both are nil when no capture exists.
func (a *App) envFor(workspaceID string) (overlay, unset []string) {
	if workspaceID == "" {
		return nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.envOverlay[workspaceID], a.envUnset[workspaceID]
}

// withoutKeys returns env minus every KEY=VALUE entry whose KEY is in keys.
// It never drops a PERCH_-prefixed key: perch plumbing is not user
// environment, and the env-sync delta never lists it anyway. With no keys it
// returns env unchanged.
func withoutKeys(env, keys []string) []string {
	if len(keys) == 0 {
		return env
	}
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if !strings.HasPrefix(k, perchEnvPrefix) {
			drop[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if _, gone := drop[key]; gone {
			continue
		}
		out = append(out, e)
	}
	return out
}

// forgetEnv drops a workspace's captured env state and revokes its env-sync
// token, so no process that inherited a drawer's environment can trigger a
// relaunch or mint state for it any more.
func (a *App) forgetEnv(workspaceID string, dropOverlay bool) {
	if dropOverlay {
		a.mu.Lock()
		delete(a.envOverlay, workspaceID)
		delete(a.envUnset, workspaceID)
		a.mu.Unlock()
	}
	if a.envsync != nil {
		a.envsync.Revoke(workspaceID)
	}
}

// onEnvSync is the env-sync endpoint's callback. It stores the captured
// delta (the variables to set and the ones to drop) as the workspace's
// in-memory overlay, replacing the previous capture: each reload reports the
// terminal's full difference from the baseline. onEnvSync never persists the
// overlay, because the payload may hold secrets.
//
// When the workspace's agent pane is live, onEnvSync then relaunches it,
// keeping the conversation. When the session is closed it only stores the
// overlay, which the next open picks up: a stray `perch reload` from a
// process that outlived its drawer never reopens a closed session in the
// background.
//
// The relaunch must never run inline in the HTTP handler. OpenWorkspace
// takes a.mu, and tears down and rebuilds the pane. A synchronous call would
// stall the env-sync handler, because the call would hold the request open,
// and could cause reentrancy with the event pump. onEnvSync stores the
// overlay before it starts the goroutine. This guarantees the relaunch sees
// the fresh delta.
func (a *App) onEnvSync(workspaceID string, d envsync.Delta) {
	a.mu.Lock()
	if a.envOverlay == nil {
		a.envOverlay = map[string][]string{}
	}
	if a.envUnset == nil {
		a.envUnset = map[string][]string{}
	}
	a.envOverlay[workspaceID] = d.Set
	a.envUnset[workspaceID] = d.Unset
	_, live := a.bridges[paneIDFor(workspaceID)]
	a.mu.Unlock()
	if !live {
		return
	}

	// Tell the frontend to remount this workspace's agent terminal with a fresh
	// xterm. Do this before the relaunch respawns the pty. The respawn reuses
	// the same paneID. With no remount, the new `claude --resume` command
	// redraws over the old buffer and keeps a stale grid size. This causes the
	// garbled screen seen after a reload. Bumping the terminal epoch rebuilds
	// an empty xterm, the same as the user-reopen path already does. The
	// fresh mount then resends resizePty, so the agent redraws clean at the
	// right size. perch emits this event before the goroutine starts, so the
	// fresh pane is ready when the agent draws. If the relaunch then fails,
	// the old agent keeps running in the remounted terminal (its next redraw
	// repaints it) and a blocking notice says why.
	a.emit(evtWorkspaceRelaunch, map[string]any{"workspaceId": workspaceID})

	go func() {
		defer safe.Recover("envsync-relaunch")
		if err := a.relaunchWorkspace(workspaceID); err != nil {
			a.emit("notify", map[string]any{
				"tier":        "blocking",
				"title":       "Reload failed",
				"body":        fmt.Sprintf("Could not relaunch the agent with the new environment: %v", err),
				"workspaceId": workspaceID,
			})
		}
	}()
}

// relaunchWorkspace reopens workspaceID only if its agent pane is still live
// once the lifecycle lock is held, so a close that won the race is never
// undone.
func (a *App) relaunchWorkspace(workspaceID string) error {
	if err := validateSessionID(workspaceID); err != nil {
		return fmt.Errorf("invalid workspace id: %w", err)
	}
	unlock := a.lockWorkspace(workspaceID)
	defer unlock()
	a.mu.Lock()
	_, live := a.bridges[paneIDFor(workspaceID)]
	a.mu.Unlock()
	if !live {
		return nil
	}
	return a.openWorkspace(workspaceID)
}

// shellQuote wraps s in single quotes so a shell command line can safely
// include s when typed into a pty. shellQuote uses the standard POSIX escape
// for an embedded single quote: close the quote, add a backslash-escaped
// literal quote, then reopen the quote. An absolute path with spaces or
// single quotes survives being typed into the drawer shell intact, and
// reaches the shell as one argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ReloadAgentEnv is the bound method that the drawer's reload button calls.
// ReloadAgentEnv resolves the workspace id from the drawer pane id, and
// types `perch reload` into that drawer's shell. This way, the button and
// the manual command share exactly one code path. ReloadAgentEnv is
// unavailable on the home drawer, because the home drawer has no workspace.
func (a *App) ReloadAgentEnv(paneID string) error {
	if err := validateSessionID(paneID); err != nil {
		return fmt.Errorf("invalid pane id: %w", err)
	}
	if paneID == homeShellPaneID {
		return fmt.Errorf("reload is not available on the home shell")
	}
	workspaceID := workspaceIDForShellPane(paneID)
	if workspaceID == "" {
		return fmt.Errorf("not a workspace drawer pane: %q", paneID)
	}
	a.mu.Lock()
	br, ok := a.bridges[paneID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown pane %q", paneID)
	}
	// The perch binary lives at bin/perch and starts by absolute path, so the
	// binary is not on PATH. When perch knows its own absolute path,
	// ReloadAgentEnv types the shell-quoted absolute path. This way the
	// button works even when a login profile overrides PATH. Otherwise
	// ReloadAgentEnv falls back to a bare `perch` command, so there is no
	// regression when os.Executable fails.
	line := "perch reload\n"
	if a.perchBin != "" {
		line = shellQuote(a.perchBin) + " reload\n"
	}
	_, err := br.Write([]byte(line))
	return err
}
