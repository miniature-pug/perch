package pty

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// launchCase is a shell, its startup file and its environment, for which a
// launch line typed after WaitShellReady must run.
type launchCase struct {
	name string
	bin  string // bash or fish
	rc   string
	// term is the TERM entry for the child, or "-" for no TERM at all.
	term string
	// inputrc is the content of $INPUTRC (bash only).
	inputrc string
}

// spawnLaunchCase starts the shell for c, or skips the test.
func spawnLaunchCase(t *testing.T, c launchCase) *shellUnderTest {
	t.Helper()
	bin, err := exec.LookPath(c.bin)
	if err != nil {
		t.Skipf("%s not installed", c.bin)
	}
	dir := t.TempDir()
	s := &shellUnderTest{out: &outputCollector{}, marker: filepath.Join(dir, "LAUNCHED")}
	env := []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "SHELL=" + bin, "LANG=C.UTF-8"}
	if c.term != "-" {
		env = append(env, "TERM="+c.term)
	}
	var argv []string
	switch c.bin {
	case "bash":
		writeFile(t, filepath.Join(dir, ".bashrc"), "PS1='$ '\n"+c.rc+"\n")
		writeFile(t, filepath.Join(dir, "inputrc"), c.inputrc)
		argv = []string{bin, "--noprofile", "--rcfile", filepath.Join(dir, ".bashrc"), "-i"}
		env = append(env, "INPUTRC="+filepath.Join(dir, "inputrc"))
	case "fish":
		cfg := filepath.Join(dir, "config")
		writeFile(t, filepath.Join(cfg, "fish", "config.fish"), "set -g fish_greeting ''\n"+c.rc+"\n")
		argv = []string{bin, "-l"}
		env = append(env, "XDG_CONFIG_HOME="+cfg, "XDG_DATA_HOME="+filepath.Join(dir, "data"),
			"XDG_CACHE_HOME="+filepath.Join(dir, "cache"))
	}
	s.b, err = Spawn(context.Background(), dir, argv, env, "d", "x", s.out.emit, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.b.Close() })
	return s
}

// A launch line must run, and WaitShellReady must say ShellReady, when the
// shell is at a normal prompt even though readline never sent the bracketed
// paste marker (no usable TERM, `bind` in .bashrc), or a background job
// started by the rc file shares the shell's process group.
func TestWaitShellReady_LaunchRunsAtNormalPrompt(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns real shells")
	}
	const xterm = "xterm-256color"
	cases := []launchCase{
		{name: "bash/TERM unset", bin: "bash", term: "-"},
		{name: "bash/TERM empty", bin: "bash", term: ""},
		{name: "bash/TERM=dumb", bin: "bash", term: "dumb"},
		{name: "bash/TERM without terminfo", bin: "bash", term: "nosuchterm-perch"},
		{name: "bash/bind bracketed paste off", bin: "bash", term: xterm,
			rc: `bind 'set enable-bracketed-paste off'`},
		{name: "bash/background job", bin: "bash", term: xterm, rc: "sleep 300 &"},
		{name: "bash/disowned background job", bin: "bash", term: xterm, rc: "sleep 300 & disown"},
		{name: "bash/coproc", bin: "bash", term: xterm, rc: "coproc sleep 300"},
		{name: "bash/stdout through tee", bin: "bash", term: xterm, rc: `exec > >(tee -a "$HOME/log") 2>&1`},
		{name: "bash/background job and bind", bin: "bash", term: xterm,
			rc: "bind 'set enable-bracketed-paste off'\nsleep 300 &"},
		{name: "fish/background job", bin: "fish", term: xterm, rc: "sleep 300 &"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := spawnLaunchCase(t, c)
			start := time.Now()
			r := s.b.WaitShellReady(context.Background(), gatedMaxWait)
			t.Logf("%v after %v", r, time.Since(start).Round(time.Millisecond))
			wantResult(t, r, ShellReady)
			if _, err := s.b.Write(s.launchLine()); err != nil {
				t.Fatal(err)
			}
			if !s.launched(5 * time.Second) {
				t.Errorf("launch line did not run; output %q", s.out.String())
			}
		})
	}
}

// The pselect6 rule must not let bash 5.1+ with a lost marker mistake an rc
// builtin cbreak read for its prompt: that prompt must stay busy.
func TestWaitShellReady_MarkerLostBashStillVetoesRcPrompts(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("spawns real shells")
	}
	for name, rc := range map[string]string{
		"read -n1":       `read -r -n 1 -p 'Start tmux? [y/N] ' _a`,
		"read -n1 -t 30": `read -r -n 1 -t 30 -p 'Start tmux? [y/N] ' _a`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Classified marker-only: usable TERM, no inputrc; the rc file
			// turns the marker off at run time, as in the case above.
			s := spawnLaunchCase(t, launchCase{bin: "bash", term: "xterm-256color",
				rc: "bind 'set enable-bracketed-paste off'\n" + rc})
			wantResult(t, s.b.WaitShellReady(context.Background(), 2500*time.Millisecond), ShellBusy)
		})
	}
}

// --- bash --version probe ---------------------------------------------------

func TestBashProbe_HangingProbeHonoursCtxAndIsNotCached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow", "bash")
	// The sleep child keeps the stdout pipe open after the shell is killed.
	writeScript(t, slow, "#!/bin/sh\nsleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, known := bashHasBracketedPaste(ctx, slow); known {
		t.Fatal("a hung probe reported a known answer")
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("probe ignored ctx for %v", d)
	}
	// The same path, now answering, is probed again.
	writeScript(t, slow, "#!/bin/sh\necho 'GNU bash, version 5.2.0(1)-release'\n")
	if has, known := bashHasBracketedPaste(context.Background(), slow); !known || !has {
		t.Fatalf("retry: has=%v known=%v, want true true", has, known)
	}
	writeScript(t, slow, "#!/bin/sh\nexit 1\n") // a cached answer ignores the file
	if has, known := bashHasBracketedPaste(context.Background(), slow); !known || !has {
		t.Fatalf("cached: has=%v known=%v, want true true", has, known)
	}
}

func TestBashProbe_SlowPathDoesNotBlockOthers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow", "bash")
	fast := filepath.Join(dir, "fast", "bash")
	writeScript(t, slow, "#!/bin/sh\nsleep 30\n")
	writeScript(t, fast, "#!/bin/sh\necho 'GNU bash, version 4.4.20(1)-release'\n")
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		started.Store(true)
		bashHasBracketedPaste(ctx, slow)
	}()
	for !started.Load() {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // let it be inside the exec
	start := time.Now()
	if has, known := bashHasBracketedPaste(context.Background(), fast); !known || has {
		t.Errorf("fast: has=%v known=%v, want false true", has, known)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("a probe of another path waited %v behind the hung one", d)
	}
	cancel()
	<-done
}

// A hung probe must not make WaitShellReady ignore its ctx, and its failure
// must leave the raw-mode fallback enabled.
func TestWaitShellReady_ProbeFailureAllowsFallbackAndCancel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	slow := filepath.Join(dir, "bash")
	writeScript(t, slow, "#!/bin/sh\nsleep 30\n")
	writeFile(t, filepath.Join(dir, "ti", "x", "xterm-test"), "")
	newBridge := func() (*Bridge, *readyGate) {
		f := &fakeTTY{pgrp: fakePID}
		g := newReadyGate(fakePID, f.probe)
		g.spec = shellSpec{path: slow, env: []string{
			"HOME=" + dir, "INPUTRC=/nonexistent", "TERM=xterm-test", "TERMINFO=" + filepath.Join(dir, "ti")}}
		return &Bridge{gate: g}, g
	}

	b, _ := newBridge()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	wantResult(t, b.WaitShellReady(ctx, time.Minute), ShellCancelled)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("cancelled WaitShellReady took %v", d)
	}

	b, g := newBridge()
	wantResult(t, b.WaitShellReady(context.Background(), 10*time.Second), ShellReady)
	if g.traitsKnown {
		t.Error("a failed probe was recorded as a known classification")
	}
}
